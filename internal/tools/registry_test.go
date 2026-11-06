package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/anas-domesticus/personal-agent/internal/config"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools"
	"github.com/anas-domesticus/personal-agent/internal/tools/clock"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	toolsmcp "github.com/anas-domesticus/personal-agent/internal/tools/mcp"
)

func names(t *testing.T, r *tools.Toolset, cfg domain.ToolConfig, admin bool) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, tl := range r.For(cfg, admin) {
		info, _ := tl.Info(context.Background())
		out[info.Name] = true
	}
	return out
}

// limitSettings is a settings type for a fake tool.
type limitSettings struct {
	Limit int `json:"limit"`
}

func (s *limitSettings) Validate() error {
	if s.Limit < 0 {
		return errors.New("limit must not be negative")
	}
	return nil
}

func TestRegistry(t *testing.T) {
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

	general, err := kit.Specs(clock.Tools())
	if err != nil {
		t.Fatal(err)
	}
	// A tool with settings, whose name reports the limit it was built with.
	var built []int
	general = append(general, kit.Spec{
		Name: "limited", Summary: "x",
		Settings: func() any { return &limitSettings{Limit: 5} },
		New: func(v any) (tool.InvokableTool, error) {
			built = append(built, v.(*limitSettings).Limit)
			return kit.New("limited", "x", func(context.Context, struct{}) (string, error) { return "", nil })
		},
	})
	adminSpecs, err := kit.Specs(kit.Build(func() (tool.InvokableTool, error) {
		return kit.New("create_agent", "x", func(context.Context, struct{}) (string, error) { return "", nil })
	}))
	if err != nil {
		t.Fatal(err)
	}
	adminSpecs[0].Admin = true

	reg, err := tools.New(general, []string{"create_agent"}, set)
	if err != nil {
		t.Fatal(err)
	}
	r, err := reg.WithAdmin(adminSpecs)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name, value string
		ok          bool
	}{
		{"current_time", "true", true},
		{"current_time", "false", true},
		{"current_time", `{"x": 1}`, false}, // takes no settings
		{"limited", `{"limit": 3}`, true},
		{"limited", `{"limit": -1}`, false},
		{"limited", `{"limt": 3}`, false}, // unknown setting
		{"srv__alpha", "true", true},
		{"mcp:srv", "true", true},
		{"mcp:srv", `{"a": 1}`, false},
		{"srv__nope", "true", false},
		{"mcp:gone", "true", false},
		{"gone__tool", "true", false},
		{"create_agent", "true", false}, // admin tools only come from the flag
		{"create_agent", "false", true}, // but can be turned off
		{"no_such_tool", "true", false},
	} {
		err := reg.Check(c.name, json.RawMessage(c.value))
		if (err == nil) != c.ok {
			t.Errorf("Check(%s, %s) = %v, want ok=%v", c.name, c.value, err, c.ok)
		}
	}

	if got := names(t, r, domain.On("mcp:srv", "srv__alpha"), false); len(got) != 2 || !got["srv__alpha"] || !got["srv__beta"] {
		t.Fatalf("mcp:srv expanded to %v", got)
	}
	// Unavailable and unknown tools are skipped, not fatal.
	if got := names(t, r, domain.On("current_time", "gone__tool", "mcp:gone", "no_such_tool"), false); len(got) != 1 {
		t.Fatalf("unavailable tools should be skipped: %v", got)
	}
	// false turns a tool off.
	cfg := domain.ToolConfig{"current_time": json.RawMessage("false"), "limited": json.RawMessage(`{"limit": 2}`)}
	if got := names(t, r, cfg, false); len(got) != 1 || !got["limited"] {
		t.Fatalf("got %v", got)
	}
	if built[len(built)-1] != 2 {
		t.Fatalf("settings not passed to the tool: %v", built)
	}
	names(t, r, domain.On("limited"), false)
	if built[len(built)-1] != 5 {
		t.Fatalf("true should build with default settings: %v", built)
	}

	// Admin tools come with the flag, unless turned off.
	if got := names(t, r, nil, true); !got["create_agent"] {
		t.Fatalf("admin tools missing: %v", got)
	}
	if got := names(t, r, domain.ToolConfig{"create_agent": json.RawMessage("false")}, true); got["create_agent"] {
		t.Fatal("admin tool turned off but still given")
	}
	if got := names(t, r, domain.On("create_agent"), false); got["create_agent"] {
		t.Fatal("non-admin agent got an admin tool")
	}

	listed := map[string]kit.Info{}
	for _, i := range reg.Assignable() {
		listed[i.Name] = i
	}
	if _, ok := listed["mcp:srv"]; !ok || listed["create_agent"].Name != "" {
		t.Fatalf("Assignable() = %v", listed)
	}
	if _, ok := listed["srv__beta"]; !ok {
		t.Fatalf("Assignable() = %v", listed)
	}
}

func TestConfigJSONIsCanonical(t *testing.T) {
	cfg, err := domain.ParseAgentConfig([]byte(`{"tools": {"web_search": { "max_results" : 3 }, "read": true, "shell": false}, "max_iterations": 5}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"max_iterations":5,"tools":{"read":true,"shell":false,"web_search":{"max_results":3}}}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
	if _, err := domain.ParseAgentConfig([]byte(`{"tools": {"read": "yes"}}`)); err == nil {
		t.Fatal("a non-boolean, non-object tool value should be rejected")
	}
}
