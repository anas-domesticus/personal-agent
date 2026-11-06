package mcp_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/config"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	toolsmcp "github.com/anas-domesticus/personal-agent/internal/tools/mcp"
)

// startServer runs a real streamable-HTTP MCP server with two tools.
func startServer(t *testing.T) string {
	t.Helper()
	s := server.NewMCPServer("test", "1.0")
	s.AddTool(mcp.NewTool("echo", mcp.WithDescription("Echo text back."), mcp.WithString("text", mcp.Required())),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			text, err := req.RequireString("text")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText("echo: " + text), nil
		})
	s.AddTool(mcp.NewTool("fail", mcp.WithDescription("Always fails.")),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultError("this tool always fails"), nil
		})
	ts := server.NewTestStreamableHTTPServer(s)
	t.Cleanup(ts.Close)
	return ts.URL + "/mcp"
}

func connect(t *testing.T, servers map[string]config.MCPServer) *toolsmcp.Set {
	t.Helper()
	for name, s := range servers {
		s.Name = name
		if s.Timeout == 0 {
			s.Timeout = 5 * time.Second
		}
		servers[name] = s
	}
	set := toolsmcp.Connect(context.Background(), config.MCP{Servers: servers})
	t.Cleanup(set.Close)
	return set
}

func TestConnect(t *testing.T) {
	t.Parallel()
	url := startServer(t)
	set := connect(t, map[string]config.MCPServer{
		"test":    {Transport: config.TransportHTTP, URL: url},
		"limited": {Transport: config.TransportHTTP, URL: url, Tools: []string{"echo"}},
		"down":    {Transport: config.TransportHTTP, URL: "http://127.0.0.1:1/mcp", Timeout: time.Second},
		"nokey":   {Transport: config.TransportHTTP, URL: url, Missing: []string{"API_KEY"}},
	})

	status := map[string]toolsmcp.Status{}
	for _, s := range set.Status() {
		status[s.Server] = s
	}
	assert.Equal(t, 2, status["test"].Tools)
	assert.Empty(t, status["test"].Error)
	assert.Equal(t, 1, status["limited"].Tools, "allowlist not applied")
	assert.NotEmpty(t, status["down"].Error, "unreachable server should report an error")
	assert.Contains(t, status["nokey"].Error, "API_KEY", "missing variable not reported")

	ts, ok := set.ServerTools("test")
	require.True(t, ok)
	var names []string
	for _, tl := range ts {
		names = append(names, kit.Name(tl))
	}
	assert.Equal(t, []string{"test__echo", "test__fail"}, names)

	// Configured reports every enabled server, connected or not.
	assert.True(t, set.Configured("down"))
	assert.False(t, set.Configured("nope"))
	_, ok = set.ServerTools("down")
	assert.False(t, ok, "down server should not be listed as connected")

	tests := []struct {
		name         string
		tool         string
		args         string
		wantContains string
		wantResult   string // JSON, if set
	}{
		{name: "echo", tool: "test__echo", args: `{"text": "hi"}`, wantContains: "echo: hi"},
		{name: "tool error becomes a result", tool: "test__fail", args: `{}`, wantResult: `{"ok":false,"error":"this tool always fails"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tl, ok := set.Tool(tt.tool)
			require.True(t, ok)
			out, err := tl.InvokableRun(context.Background(), tt.args)
			require.NoError(t, err, "a tool error should be a result, not a Go error")
			if tt.wantContains != "" {
				assert.Contains(t, out, tt.wantContains)
			}
			if tt.wantResult != "" {
				var res struct {
					OK    bool   `json:"ok"`
					Error string `json:"error"`
				}
				require.NoError(t, json.Unmarshal([]byte(out), &res))
				assert.False(t, res.OK)
				assert.Contains(t, res.Error, "always fails")
			}
		})
	}
	assert.Len(t, set.Tools(), 3, "test's two tools and limited's echo")
}

// TestReconnect isn't parallel: it gives up a port and expects to get it back.
func TestReconnect(t *testing.T) {
	// Reserve a port, then start the server on it only after connecting
	// has failed once.
	s := server.NewMCPServer("late", "1.0")
	s.AddTool(mcp.NewTool("ping"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("pong"), nil
	})
	ts := server.NewTestStreamableHTTPServer(s)
	url := ts.URL + "/mcp"
	ts.Close()

	set := connect(t, map[string]config.MCPServer{"late": {Transport: config.TransportHTTP, URL: url, Timeout: time.Second}})
	_, ok := set.ServerTools("late")
	require.False(t, ok, "server should start disconnected")

	ts = restartOn(t, s, url)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		set.Maintain(ctx, 50*time.Millisecond)
	}()
	t.Cleanup(func() { cancel(); <-done })

	require.Eventually(t, func() bool {
		_, ok := set.Tool("late__ping")
		return ok
	}, 10*time.Second, 50*time.Millisecond, "not reconnected")

	tl, _ := set.Tool("late__ping")
	out, err := tl.InvokableRun(context.Background(), `{}`)
	require.NoError(t, err)
	assert.Contains(t, out, "pong")
}

// restartOn serves s at the host and port of url.
func restartOn(t *testing.T, s *server.MCPServer, url string) *httptest.Server {
	t.Helper()
	addr := strings.TrimSuffix(strings.TrimPrefix(url, "http://"), "/mcp")
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		t.Skipf("port %s was taken: %v", addr, err)
	}
	ts := httptest.NewUnstartedServer(server.NewStreamableHTTPServer(s))
	_ = ts.Listener.Close()
	ts.Listener = l
	ts.Start()
	return ts
}

func TestOutputIsCapped(t *testing.T) {
	t.Parallel()
	s := server.NewMCPServer("big", "1.0")
	s.AddTool(mcp.NewTool("dump"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(strings.Repeat("x", 3*kit.MaxOutput)), nil
	})
	ts := server.NewTestStreamableHTTPServer(s)
	t.Cleanup(ts.Close)
	set := connect(t, map[string]config.MCPServer{"big": {Transport: config.TransportHTTP, URL: ts.URL + "/mcp"}})
	tl, ok := set.Tool("big__dump")
	require.True(t, ok, "tool missing")

	out, err := tl.InvokableRun(context.Background(), `{}`)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(out), kit.MaxOutput+300)
	assert.Contains(t, out, "truncated")
}

// TestStdioEnvIsMinimal isn't parallel: it sets an environment variable.
func TestStdioEnvIsMinimal(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "secret-should-not-leak")
	cmd, err := toolsmcp.MinimalEnvCommand(context.Background(), "env", []string{"WANTED=1"}, nil)
	require.NoError(t, err)
	env := strings.Join(cmd.Env, "\n")
	assert.NotContains(t, env, "secret-should-not-leak", "stdio server inherited OPENROUTER_API_KEY")
	assert.Contains(t, env, "WANTED=1")
	assert.Contains(t, env, "PATH=")
}

func TestIsMCPName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "server", in: "mcp:srv", want: true},
		{name: "namespaced tool", in: "srv__tool", want: true},
		{name: "native tool", in: "web_search", want: false},
		{name: "single underscore", in: "a_b", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, toolsmcp.IsMCPName(tt.in))
		})
	}
}
