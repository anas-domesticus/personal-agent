// Package mcp connects to the MCP servers in the MCP config and exposes their
// tools as general tools.
//
// Each tool is named "<server>__<tool>". Agent versions list them by that
// name, or list "mcp:<server>" for every tool the server offers.
//
// Servers connect in parallel at startup. One that is down is skipped, and
// Maintain keeps trying to reconnect it; a connected server that stops
// answering pings is reconnected the same way.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/anas-domesticus/personal-agent/internal/config"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

// Separator joins a server name and a tool name.
const Separator = "__"

// ServerPrefix marks a tool entry meaning "every tool from this server".
const ServerPrefix = "mcp:"

// toolNamePattern is what model APIs accept for function names.
var toolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// inheritedEnv is all a stdio server gets from this process's environment,
// besides its configured env. Secrets such as the OpenRouter key and the
// database URL are never passed on.
var inheritedEnv = []string{"PATH", "HOME", "USER", "LANG", "TMPDIR", "TERM"}

// Set is the configured servers and the tools of those connected.
type Set struct {
	// life bounds every connection, including reconnects. It is the context
	// Connect was called with.
	life context.Context

	mu      sync.RWMutex
	servers map[string]*server
	closed  bool
}

// pingFailures is how many pings in a row must fail before a connected
// server is presumed dead and reconnected.
const pingFailures = 3

type server struct {
	cfg    config.MCPServer
	client *client.Client // nil while disconnected
	tools  []tool.InvokableTool
	byName map[string]tool.InvokableTool
	err    string
	fails  int // consecutive failed pings
}

// Status reports a server's connection state.
type Status struct {
	Server    string `json:"server"`
	Connected bool   `json:"connected"`
	Tools     int    `json:"tools"`
	Error     string `json:"error,omitempty"`
}

// Connect connects to every enabled server in parallel. Connections last
// until ctx ends or Close is called. A server that fails is logged and
// skipped so one broken server can't stop the agent starting.
func Connect(ctx context.Context, cfg config.MCP) *Set {
	s := &Set{life: ctx, servers: map[string]*server{}}
	for _, srv := range cfg.Enabled() {
		s.servers[srv.Name] = &server{cfg: srv}
	}
	var wg sync.WaitGroup
	for name := range s.servers {
		wg.Go(func() { s.reconnect(name) })
	}
	wg.Wait()
	return s
}

// Maintain pings connected servers and reconnects ones that are down, every
// interval, until ctx ends. A connected server is reconnected after
// pingFailures failed pings in a row.
func (s *Set) Maintain(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var wg sync.WaitGroup
		for _, name := range s.names() {
			wg.Go(func() { s.check(ctx, name) })
		}
		wg.Wait()
	}
}

func (s *Set) check(ctx context.Context, name string) {
	s.mu.RLock()
	srv := s.servers[name]
	cli, missing := srv.client, len(srv.cfg.Missing) > 0
	s.mu.RUnlock()
	if missing {
		return // can't connect until the config is fixed and the server restarts
	}
	if cli != nil {
		pctx, cancel := context.WithTimeout(ctx, srv.cfg.Timeout)
		err := cli.Ping(pctx)
		cancel()
		// A server that doesn't implement ping still answered, so it's alive.
		if err == nil || strings.Contains(err.Error(), "-32601") || strings.Contains(strings.ToLower(err.Error()), "method not found") {
			s.setFails(name, 0)
			return
		}
		if n := s.setFails(name, -1); n < pingFailures {
			slog.Debug("mcp ping failed", "server", name, "failures", n, "err", err)
			return
		}
		slog.Warn("mcp server stopped responding; reconnecting", "server", name, "err", err)
	}
	s.reconnect(name)
}

// setFails sets a server's consecutive ping failures, or increments them if
// n is negative, and returns the new count.
func (s *Set) setFails(name string, n int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv := s.servers[name]
	if n < 0 {
		n = srv.fails + 1
	}
	srv.fails = n
	return n
}

// reconnect replaces a server's connection with a fresh one.
func (s *Set) reconnect(name string) {
	s.mu.RLock()
	cfg := s.servers[name].cfg
	s.mu.RUnlock()

	// Connect first, so the old connection is only dropped once there's a
	// working replacement or the server has proven to be down.
	cli, tools, err := connect(s.life, cfg)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		if cli != nil {
			_ = cli.Close()
		}
		return
	}
	srv := s.servers[name]
	if srv.client != nil {
		_ = srv.client.Close()
	}
	srv.client, srv.tools, srv.byName, srv.err, srv.fails = cli, tools, map[string]tool.InvokableTool{}, "", 0
	for _, t := range tools {
		srv.byName[kit.Name(t)] = t
	}
	if err != nil {
		srv.err = err.Error()
	}
	s.mu.Unlock()

	if err != nil {
		slog.Warn("mcp server unavailable", "server", name, "err", err)
	} else {
		slog.Info("mcp server connected", "server", name, "tools", len(tools))
	}
}

func connect(life context.Context, srv config.MCPServer) (*client.Client, []tool.InvokableTool, error) {
	if len(srv.Missing) > 0 {
		return nil, nil, fmt.Errorf("unset environment variables: %s", strings.Join(srv.Missing, ", "))
	}

	// The connection lives as long as life; only the handshake and tool
	// listing are bounded by the timeout.
	cli, err := newClient(life, srv)
	if err != nil {
		return nil, nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = cli.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(life, srv.Timeout)
	defer cancel()

	if _, err := cli.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "personal-agent", Version: "0.1"},
	}}); err != nil {
		return nil, nil, fmt.Errorf("initialize: %w", err)
	}

	raw, err := einomcp.GetTools(ctx, &einomcp.Config{Cli: cli, ToolNameList: srv.Tools})
	if err != nil {
		return nil, nil, fmt.Errorf("list tools: %w", err)
	}

	var wrapped []tool.InvokableTool
	seen := map[string]bool{}
	for _, bt := range raw {
		it, isInvokable := bt.(tool.InvokableTool)
		if !isInvokable {
			continue
		}
		info, err := it.Info(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("describe tool: %w", err)
		}
		name := srv.Name + Separator + info.Name
		switch {
		case !toolNamePattern.MatchString(name):
			slog.Warn("skipping mcp tool with an unusable name", "server", srv.Name, "tool", info.Name)
			continue
		case seen[name]:
			slog.Warn("skipping duplicate mcp tool", "server", srv.Name, "tool", info.Name)
			continue
		}
		seen[name] = true
		wrapped = append(wrapped, &namedTool{inner: it, info: &schema.ToolInfo{
			Name: name, Desc: info.Desc, ParamsOneOf: info.ParamsOneOf, Extra: info.Extra,
		}})
	}
	ok = true
	return cli, wrapped, nil
}

func newClient(ctx context.Context, srv config.MCPServer) (*client.Client, error) {
	switch srv.Transport {
	case config.TransportStdio:
		env := make([]string, 0, len(srv.Env))
		for k, v := range srv.Env {
			env = append(env, k+"="+v)
		}
		cli, err := client.NewStdioMCPClientWithOptions(srv.Command, env, srv.Args,
			transport.WithCommandFunc(minimalEnvCommand))
		if err != nil {
			return nil, fmt.Errorf("start %s: %w", srv.Command, err)
		}
		return cli, nil

	case config.TransportHTTP:
		cli, err := client.NewStreamableHttpClient(srv.URL, transport.WithHTTPHeaders(srv.Headers))
		if err != nil {
			return nil, fmt.Errorf("new http client: %w", err)
		}
		if err := cli.Start(ctx); err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
		return cli, nil

	case config.TransportSSE:
		cli, err := client.NewSSEMCPClient(srv.URL, transport.WithHeaders(srv.Headers))
		if err != nil {
			return nil, fmt.Errorf("new sse client: %w", err)
		}
		if err := cli.Start(ctx); err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
		return cli, nil
	}
	return nil, fmt.Errorf("unknown transport %q", srv.Transport)
}

// minimalEnvCommand starts a stdio server with only inheritedEnv from this
// process plus its configured env.
func minimalEnvCommand(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = make([]string, 0, len(inheritedEnv)+len(env))
	for _, k := range inheritedEnv {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, env...)
	return cmd, nil
}

// Configured reports whether a server is in the config and enabled, whether
// or not it is connected.
func (s *Set) Configured(server string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.servers[server]
	return ok
}

// Tool returns a connected tool by its full name.
func (s *Set) Tool(name string) (tool.InvokableTool, bool) {
	server, _, ok := strings.Cut(name, Separator)
	if !ok || s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	srv, ok := s.servers[server]
	if !ok {
		return nil, false
	}
	t, ok := srv.byName[name]
	return t, ok
}

// ServerTools returns a server's tools, or false if it isn't connected.
func (s *Set) ServerTools(server string) ([]tool.InvokableTool, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	srv, ok := s.servers[server]
	if !ok || srv.client == nil {
		return nil, false
	}
	return append([]tool.InvokableTool(nil), srv.tools...), true
}

// Tools returns every connected tool, ordered by server.
func (s *Set) Tools() []tool.InvokableTool {
	var out []tool.InvokableTool
	for _, name := range s.names() {
		ts, _ := s.ServerTools(name)
		out = append(out, ts...)
	}
	return out
}

// Status reports each configured server's state, ordered by name.
func (s *Set) Status() []Status {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Status, 0, len(s.servers))
	for name, srv := range s.servers {
		out = append(out, Status{Server: name, Connected: srv.client != nil, Tools: len(srv.tools), Error: srv.err})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	return out
}

// Close disconnects every server.
func (s *Set) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, srv := range s.servers {
		if srv.client != nil {
			_ = srv.client.Close()
			srv.client, srv.tools, srv.byName = nil, nil, nil
		}
	}
}

func (s *Set) names() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.servers))
	for n := range s.servers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// IsMCPName reports whether a tool entry refers to MCP: a namespaced tool or
// a whole server.
func IsMCPName(name string) bool {
	return strings.HasPrefix(name, ServerPrefix) || strings.Contains(name, Separator)
}

// namedTool renames an MCP tool, caps its output, and turns its errors into
// results the model can read instead of errors that would end the agent's run.
type namedTool struct {
	inner tool.InvokableTool
	info  *schema.ToolInfo
}

func (t *namedTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

func (t *namedTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	out, err := t.inner.InvokableRun(ctx, args, opts...)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("mcp tool %s: %w", t.info.Name, ctx.Err())
		}
		b, _ := json.Marshal(kit.Result[struct{}]{Error: err.Error()})
		return string(b), nil
	}
	return kit.Truncate(out), nil
}
