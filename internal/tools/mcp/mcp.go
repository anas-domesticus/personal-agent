// Package mcp connects to the MCP servers in the MCP config and exposes their
// tools as general tools.
//
// Each tool is named "<server>__<tool>". Agent versions list them by that
// name, or list "mcp:<server>" for every tool the server offers.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"

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

// Set is the connected servers and their tools.
type Set struct {
	mu      sync.Mutex
	clients []*client.Client
	servers map[string][]tool.InvokableTool
	status  []Status
}

// Status reports how connecting to one server went.
type Status struct {
	Server string `json:"server"`
	Tools  int    `json:"tools"`
	Error  string `json:"error,omitempty"`
}

// Connect connects to every enabled server. Connections last until ctx ends or
// Close is called. A server that fails is logged and skipped so one broken
// server can't stop the agent starting.
func Connect(ctx context.Context, cfg config.MCP) *Set {
	s := &Set{servers: map[string][]tool.InvokableTool{}}
	for _, srv := range cfg.Enabled() {
		n, err := s.connect(ctx, srv)
		st := Status{Server: srv.Name, Tools: n}
		if err != nil {
			st.Error = err.Error()
			slog.Warn("mcp server unavailable", "server", srv.Name, "err", err)
		} else {
			slog.Info("mcp server connected", "server", srv.Name, "tools", n)
		}
		s.status = append(s.status, st)
	}
	return s
}

func (s *Set) connect(ctx context.Context, srv config.MCPServer) (int, error) {
	if len(srv.Missing) > 0 {
		return 0, fmt.Errorf("unset environment variables: %s", strings.Join(srv.Missing, ", "))
	}

	// The connection lives as long as the caller's context; only the
	// handshake and tool listing are bounded by the timeout.
	cli, err := newClient(ctx, srv)
	if err != nil {
		return 0, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = cli.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, srv.Timeout)
	defer cancel()

	if _, err := cli.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "personal-agent", Version: "0.1"},
	}}); err != nil {
		return 0, fmt.Errorf("initialize: %w", err)
	}

	raw, err := einomcp.GetTools(ctx, &einomcp.Config{Cli: cli, ToolNameList: srv.Tools})
	if err != nil {
		return 0, err
	}

	var wrapped []tool.InvokableTool
	for _, bt := range raw {
		it, isInvokable := bt.(tool.InvokableTool)
		if !isInvokable {
			continue
		}
		info, err := it.Info(ctx)
		if err != nil {
			return 0, err
		}
		name := srv.Name + Separator + info.Name
		if !toolNamePattern.MatchString(name) {
			slog.Warn("skipping mcp tool with an unusable name", "server", srv.Name, "tool", info.Name)
			continue
		}
		wrapped = append(wrapped, &namedTool{inner: it, info: &schema.ToolInfo{
			Name: name, Desc: info.Desc, ParamsOneOf: info.ParamsOneOf, Extra: info.Extra,
		}})
	}

	s.mu.Lock()
	s.clients = append(s.clients, cli)
	s.servers[srv.Name] = wrapped
	s.mu.Unlock()
	ok = true
	return len(wrapped), nil
}

func newClient(ctx context.Context, srv config.MCPServer) (*client.Client, error) {
	switch srv.Transport {
	case config.TransportStdio:
		env := make([]string, 0, len(srv.Env))
		for k, v := range srv.Env {
			env = append(env, k+"="+v)
		}
		cli, err := client.NewStdioMCPClient(srv.Command, env, srv.Args...)
		if err != nil {
			return nil, fmt.Errorf("start %s: %w", srv.Command, err)
		}
		return cli, nil

	case config.TransportHTTP:
		cli, err := client.NewStreamableHttpClient(srv.URL, transport.WithHTTPHeaders(srv.Headers))
		if err != nil {
			return nil, err
		}
		if err := cli.Start(ctx); err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
		return cli, nil

	case config.TransportSSE:
		cli, err := client.NewSSEMCPClient(srv.URL, transport.WithHeaders(srv.Headers))
		if err != nil {
			return nil, err
		}
		if err := cli.Start(ctx); err != nil {
			return nil, fmt.Errorf("connect: %w", err)
		}
		return cli, nil
	}
	return nil, fmt.Errorf("unknown transport %q", srv.Transport)
}

// Tools returns every connected tool.
func (s *Set) Tools() []tool.InvokableTool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []tool.InvokableTool
	for _, name := range s.serverNames() {
		out = append(out, s.servers[name]...)
	}
	return out
}

// ServerTools returns a connected server's tool names, or false if the server
// isn't connected.
func (s *Set) ServerTools(server string) ([]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, ok := s.servers[server]
	if !ok {
		return nil, false
	}
	names := make([]string, len(ts))
	for i, t := range ts {
		info, _ := t.Info(context.Background())
		names[i] = info.Name
	}
	return names, true
}

// Status reports each configured server's connection result.
func (s *Set) Status() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Status(nil), s.status...)
}

// Close disconnects every server.
func (s *Set) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clients {
		_ = c.Close()
	}
	s.clients = nil
}

func (s *Set) serverNames() []string {
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

// namedTool renames an MCP tool and turns its errors into results the model
// can read, instead of errors that would end the agent's run.
type namedTool struct {
	inner tool.InvokableTool
	info  *schema.ToolInfo
}

func (t *namedTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

func (t *namedTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	out, err := t.inner.InvokableRun(ctx, args, opts...)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		b, _ := json.Marshal(kit.Result[struct{}]{Error: err.Error()})
		return string(b), nil
	}
	return out, nil
}
