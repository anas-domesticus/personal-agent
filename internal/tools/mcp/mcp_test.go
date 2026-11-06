package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/anas-domesticus/personal-agent/internal/config"
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
	if status["test"].Tools != 2 || status["test"].Error != "" {
		t.Fatalf("test server: %+v", status["test"])
	}
	if status["limited"].Tools != 1 {
		t.Fatalf("allowlist not applied: %+v", status["limited"])
	}
	if status["down"].Error == "" {
		t.Fatal("unreachable server should report an error")
	}
	if !strings.Contains(status["nokey"].Error, "API_KEY") {
		t.Fatalf("missing variable not reported: %+v", status["nokey"])
	}

	names, ok := set.ServerTools("test")
	if !ok || strings.Join(names, ",") != "test__echo,test__fail" {
		t.Fatalf("tool names = %v", names)
	}
	if _, ok := set.ServerTools("down"); ok {
		t.Fatal("down server should not be listed as connected")
	}

	byName := map[string]func(string) string{}
	for _, tl := range set.Tools() {
		tl := tl
		info, _ := tl.Info(context.Background())
		byName[info.Name] = func(args string) string {
			out, err := tl.InvokableRun(context.Background(), args)
			if err != nil {
				t.Fatalf("%s returned a Go error instead of a result: %v", info.Name, err)
			}
			return out
		}
	}

	if out := byName["test__echo"](`{"text": "hi"}`); !strings.Contains(out, "echo: hi") {
		t.Fatalf("echo = %s", out)
	}

	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(byName["test__fail"](`{}`)), &res); err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.Contains(res.Error, "always fails") {
		t.Fatalf("fail result = %+v", res)
	}
}
