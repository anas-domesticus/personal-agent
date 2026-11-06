package tools_test

import (
	"context"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/anas-domesticus/personal-agent/internal/config"
	"github.com/anas-domesticus/personal-agent/internal/tools"
	toolsmcp "github.com/anas-domesticus/personal-agent/internal/tools/mcp"
)

func names(t *testing.T, r *tools.Registry, list []string, admin bool) map[string]bool {
	t.Helper()
	ts, err := r.For(list, admin)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tl := range ts {
		info, _ := tl.Info(context.Background())
		out[info.Name] = true
	}
	return out
}

func TestRegistryWithMCP(t *testing.T) {
	s := server.NewMCPServer("test", "1.0")
	for _, n := range []string{"alpha", "beta"} {
		s.AddTool(mcp.NewTool(n), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		})
	}
	ts := server.NewTestStreamableHTTPServer(s)
	defer ts.Close()

	set := toolsmcp.Connect(context.Background(), config.MCP{Servers: map[string]config.MCPServer{
		"srv": {Name: "srv", Transport: config.TransportHTTP, URL: ts.URL + "/mcp", Timeout: 5 * time.Second},
	}})
	defer set.Close()

	// The catalog is only used when memory or admin tools are invoked.
	r, err := tools.New(tools.Deps{MCP: set})
	if err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]bool{
		"srv__alpha": true, "mcp:srv": true, "current_time": true,
		"mcp:gone": false, "gone__tool": false, "create_agent": false,
	} {
		if got := r.Assignable(name); got != want {
			t.Errorf("Assignable(%q) = %v, want %v", name, got, want)
		}
	}

	if got := names(t, r, []string{"mcp:srv", "srv__alpha"}, false); len(got) != 2 || !got["srv__alpha"] || !got["srv__beta"] {
		t.Fatalf("mcp:srv expanded to %v", got)
	}
	// A configured server that is down must not stop the agent building.
	if got := names(t, r, []string{"current_time", "gone__tool", "mcp:gone"}, false); len(got) != 1 {
		t.Fatalf("unavailable mcp tools should be skipped: %v", got)
	}
	if _, err := r.For([]string{"no_such_tool"}, false); err == nil {
		t.Fatal("unknown non-MCP tools should still be an error")
	}

	listed := map[string]bool{}
	for _, i := range r.General() {
		listed[i.Name] = true
	}
	if !listed["mcp:srv"] || !listed["srv__beta"] || listed["create_agent"] {
		t.Fatalf("General() = %v", listed)
	}
}
