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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/config"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools"
	"github.com/anas-domesticus/personal-agent/internal/tools/clock"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	toolsmcp "github.com/anas-domesticus/personal-agent/internal/tools/mcp"
)

var _ tools.MCPSource = (*toolsmcp.Set)(nil)

// names returns the names of the tools a config resolves to.
func names(t *testing.T, r *tools.Toolset, cfg domain.ToolConfig, admin bool) []string {
	t.Helper()
	out := []string{}
	for _, tl := range r.For(cfg, admin) {
		out = append(out, kit.Name(tl))
	}
	return out
}

// namedTool builds a real tool that does nothing but has a name.
func namedTool(t *testing.T, name string) tool.InvokableTool {
	t.Helper()
	tl, err := kit.New(name, name+" tool.\nDetails.", func(context.Context, struct{}) (string, error) { return "", nil })
	require.NoError(t, err)
	return tl
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

// fixture is a registry of current_time, a "limited" tool with settings, and
// the create_agent admin tool, over a mocked MCP source.
type fixture struct {
	reg   *tools.Registry
	ts    *tools.Toolset
	mcp   *tools.MockMCPSource
	built chan int // the limit each "limited" tool was built with
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{mcp: tools.NewMockMCPSource(t), built: make(chan int, 10)}

	general, err := kit.Specs(clock.Tools())
	require.NoError(t, err)
	general = append(general, kit.Spec{
		Name: "limited", Summary: "Limited.", SettingsHelp: `{"limit": int}`,
		Settings: func() any { return &limitSettings{Limit: 5} },
		New: func(v any) (tool.InvokableTool, error) {
			f.built <- v.(*limitSettings).Limit
			return namedTool(t, "limited"), nil
		},
	}, kit.Spec{Name: "subagent", Summary: "Built by the agent builder."}) // no constructor

	adminSpecs, err := kit.Specs([]tool.InvokableTool{namedTool(t, "create_agent")}, nil)
	require.NoError(t, err)
	adminSpecs[0].Admin = true

	f.reg, err = tools.New(general, []string{"create_agent"}, f.mcp)
	require.NoError(t, err)
	f.ts, err = f.reg.WithAdmin(adminSpecs)
	require.NoError(t, err)
	return f
}

func TestNew(t *testing.T) {
	t.Parallel()
	plain := kit.Spec{Name: "plain"}
	tests := []struct {
		name    string
		general []kit.Spec
		admin   []string
		wantErr string
	}{
		{name: "valid", general: []kit.Spec{plain}, admin: []string{"create_agent"}},
		{name: "admin spec", general: []kit.Spec{{Name: "x", Admin: true}}, wantErr: "admin tools go through WithAdmin"},
		{name: "mcp prefix", general: []kit.Spec{{Name: "mcp:x"}}, wantErr: "uses MCP naming"},
		{name: "mcp separator", general: []kit.Spec{{Name: "a__b"}}, wantErr: "uses MCP naming"},
		{name: "general and admin", general: []kit.Spec{plain}, admin: []string{"plain"}, wantErr: "both general and admin"},
		{name: "duplicate", general: []kit.Spec{plain, plain}, wantErr: `duplicate tool "plain"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := tools.New(tt.general, tt.admin, tools.NewMockMCPSource(t))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestWithAdmin(t *testing.T) {
	t.Parallel()
	adminSpec := func(name string) kit.Spec {
		s := kit.Static("x", namedTool(t, name))
		s.Admin = true
		return s
	}
	tests := []struct {
		name    string
		admin   []kit.Spec
		wantErr string
	}{
		{name: "matching", admin: []kit.Spec{adminSpec("create_agent")}},
		{name: "not marked admin", admin: []kit.Spec{kit.Static("x", namedTool(t, "create_agent"))}, wantErr: "is not marked admin"},
		{name: "general name", admin: []kit.Spec{adminSpec("plain")}, wantErr: "both general and admin"},
		{name: "mcp name", admin: []kit.Spec{adminSpec("a__b")}, wantErr: "both general and admin"},
		{name: "missing admin tool", admin: nil, wantErr: "don't match the registry's admin names"},
		{name: "extra admin tool", admin: []kit.Spec{adminSpec("create_agent"), adminSpec("other")}, wantErr: "don't match"},
		{
			name: "constructor fails",
			admin: []kit.Spec{{Name: "create_agent", Admin: true, New: func(any) (tool.InvokableTool, error) {
				return nil, errors.New("cannot build")
			}}},
			wantErr: "cannot build",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reg, err := tools.New([]kit.Spec{{Name: "plain"}}, []string{"create_agent"}, tools.NewMockMCPSource(t))
			require.NoError(t, err)
			_, err = reg.WithAdmin(tt.admin)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		tool    string
		value   string
		setup   func(m *tools.MockMCPSource)
		wantErr string
	}{
		{name: "general tool on", tool: "current_time", value: "true"},
		{name: "general tool off", tool: "current_time", value: "false"},
		{name: "settings on a tool that takes none", tool: "current_time", value: `{"x": 1}`, wantErr: "takes no settings"},
		{name: "valid settings", tool: "limited", value: `{"limit": 3}`},
		{name: "invalid settings", tool: "limited", value: `{"limit": -1}`, wantErr: "must not be negative"},
		{name: "unknown setting", tool: "limited", value: `{"limt": 3}`, wantErr: "unknown field"},
		{name: "settings off ignores settings", tool: "limited", value: "false"},
		{name: "admin tool turned on", tool: "create_agent", value: "true", wantErr: "come from the admin flag"},
		{name: "admin tool turned off", tool: "create_agent", value: "false"},
		{name: "unknown tool", tool: "no_such_tool", value: "true", wantErr: "unknown tool"},
		{
			name: "mcp server", tool: "mcp:srv", value: "true",
			setup: func(m *tools.MockMCPSource) { m.EXPECT().Configured("srv").Return(true).Once() },
		},
		{name: "mcp server with settings", tool: "mcp:srv", value: `{"a": 1}`, wantErr: "MCP tools take no settings"},
		{
			name: "unconfigured mcp server", tool: "mcp:gone", value: "true", wantErr: `no MCP server "gone" is configured`,
			setup: func(m *tools.MockMCPSource) { m.EXPECT().Configured("gone").Return(false).Once() },
		},
		{
			name: "mcp tool", tool: "srv__alpha", value: "true",
			setup: func(m *tools.MockMCPSource) {
				m.EXPECT().Configured("srv").Return(true).Once()
				m.EXPECT().ServerTools("srv").Return(nil, true).Once()
				m.EXPECT().Tool("srv__alpha").Return(nil, true).Once()
			},
		},
		{
			name: "missing mcp tool", tool: "srv__nope", value: "true", wantErr: `MCP server "srv" has no tool "srv__nope"`,
			setup: func(m *tools.MockMCPSource) {
				m.EXPECT().Configured("srv").Return(true).Once()
				m.EXPECT().ServerTools("srv").Return(nil, true).Once()
				m.EXPECT().Tool("srv__nope").Return(nil, false).Once()
			},
		},
		{
			name: "mcp tool on a disconnected server is accepted", tool: "srv__alpha", value: "true",
			setup: func(m *tools.MockMCPSource) {
				m.EXPECT().Configured("srv").Return(true).Once()
				m.EXPECT().ServerTools("srv").Return(nil, false).Once()
			},
		},
		{
			name: "mcp tool on an unconfigured server", tool: "gone__tool", value: "true", wantErr: `no MCP server "gone" is configured`,
			setup: func(m *tools.MockMCPSource) { m.EXPECT().Configured("gone").Return(false).Once() },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			if tt.setup != nil {
				tt.setup(f.mcp)
			}
			err := f.reg.Check(tt.tool, json.RawMessage(tt.value))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestFor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cfg   domain.ToolConfig
		admin bool
		setup func(t *testing.T, m *tools.MockMCPSource)
		want  []string
	}{
		{name: "nothing on", cfg: nil, want: []string{}},
		{name: "general tool", cfg: domain.On("current_time"), want: []string{"current_time"}},
		{name: "false turns a tool off", cfg: domain.ToolConfig{"current_time": json.RawMessage("false")}, want: []string{}},
		{name: "tool without a constructor is left to the builder", cfg: domain.On("subagent"), want: []string{}},
		{name: "bad settings skip the tool", cfg: domain.ToolConfig{"limited": json.RawMessage(`{"limit": -1}`)}, want: []string{}},
		{
			name: "mcp server expands, without duplicating a named tool",
			cfg:  domain.On("mcp:srv", "srv__alpha"),
			setup: func(t *testing.T, m *tools.MockMCPSource) {
				t.Helper()
				alpha := namedTool(t, "srv__alpha")
				m.EXPECT().ServerTools("srv").Return([]tool.InvokableTool{alpha, namedTool(t, "srv__beta")}, true).Once()
				m.EXPECT().Tool("srv__alpha").Return(alpha, true).Once()
			},
			want: []string{"srv__alpha", "srv__beta"},
		},
		{
			name: "unavailable and unknown tools are skipped",
			cfg:  domain.On("current_time", "gone__tool", "mcp:gone", "no_such_tool"),
			setup: func(t *testing.T, m *tools.MockMCPSource) {
				t.Helper()
				m.EXPECT().ServerTools("gone").Return(nil, false).Once()
				m.EXPECT().Tool("gone__tool").Return(nil, false).Once()
				m.EXPECT().Tool("no_such_tool").Return(nil, false).Once()
			},
			want: []string{"current_time"},
		},
		{name: "admin tools come with the flag", cfg: nil, admin: true, want: []string{"create_agent"}},
		{name: "admin tools can be turned off", cfg: domain.ToolConfig{"create_agent": json.RawMessage("false")}, admin: true, want: []string{}},
		{
			name: "non-admin agents never get admin tools", cfg: domain.On("create_agent"),
			setup: func(t *testing.T, m *tools.MockMCPSource) {
				t.Helper()
				m.EXPECT().Tool("create_agent").Return(nil, false).Once()
			},
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			if tt.setup != nil {
				tt.setup(t, f.mcp)
			}
			assert.ElementsMatch(t, tt.want, names(t, f.ts, tt.cfg, tt.admin))
		})
	}
}

func TestForBuildsWithSettings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		cfg       domain.ToolConfig
		wantLimit int
	}{
		{name: "settings are passed to the tool", cfg: domain.ToolConfig{"limited": json.RawMessage(`{"limit": 2}`)}, wantLimit: 2},
		{name: "true builds with default settings", cfg: domain.On("limited"), wantLimit: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			assert.Equal(t, []string{"limited"}, names(t, f.ts, tt.cfg, false))
			assert.Equal(t, tt.wantLimit, <-f.built)
		})
	}
}

func TestAssignable(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mcp.EXPECT().Status().Return([]toolsmcp.Status{
		{Server: "srv", Connected: true, Tools: 2},
		{Server: "down", Error: "connection refused"},
	}).Once()
	f.mcp.EXPECT().Tools().Return([]tool.InvokableTool{namedTool(t, "srv__alpha")}).Once()

	clockTools, err := clock.Tools()
	require.NoError(t, err)
	clockInfo, err := clockTools[0].Info(context.Background())
	require.NoError(t, err)

	want := []kit.Info{
		{Name: "current_time", Description: clockInfo.Desc},
		{Name: "limited", Description: "Limited.", Settings: `{"limit": int}`},
		{Name: "mcp:down", Description: "Every tool from the down MCP server, which is currently unavailable: connection refused"},
		{Name: "mcp:srv", Description: "Every tool from the srv MCP server (2 tools)."},
		{Name: "srv__alpha", Description: "srv__alpha tool."},
		{Name: "subagent", Description: "Built by the agent builder."},
	}
	assert.Equal(t, want, f.reg.Assignable())
}

// TestRegistryWithMCPServer checks the registry against a real *mcp.Set and
// MCP server, end to end.
func TestRegistryWithMCPServer(t *testing.T) {
	t.Parallel()
	s := server.NewMCPServer("test", "1.0")
	for _, n := range []string{"alpha", "beta"} {
		s.AddTool(mcp.NewTool(n), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		})
	}
	srv := server.NewTestStreamableHTTPServer(s)
	t.Cleanup(srv.Close)

	set := toolsmcp.Connect(context.Background(), config.MCP{Servers: map[string]config.MCPServer{
		"srv": {Name: "srv", Transport: config.TransportHTTP, URL: srv.URL + "/mcp", Timeout: 5 * time.Second},
	}})
	t.Cleanup(set.Close)

	reg, err := tools.New(nil, nil, set)
	require.NoError(t, err)
	ts, err := reg.WithAdmin(nil)
	require.NoError(t, err)

	t.Run("check", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, reg.Check("srv__alpha", json.RawMessage("true")))
		require.NoError(t, reg.Check("mcp:srv", json.RawMessage("true")))
		require.ErrorContains(t, reg.Check("srv__nope", json.RawMessage("true")), "has no tool")
		require.ErrorContains(t, reg.Check("mcp:gone", json.RawMessage("true")), "is configured")
	})
	t.Run("for", func(t *testing.T) {
		t.Parallel()
		assert.ElementsMatch(t, []string{"srv__alpha", "srv__beta"}, names(t, ts, domain.On("mcp:srv", "srv__alpha"), false))
	})
	t.Run("assignable", func(t *testing.T) {
		t.Parallel()
		var got []string
		for _, i := range reg.Assignable() {
			got = append(got, i.Name)
		}
		assert.Equal(t, []string{"mcp:srv", "srv__alpha", "srv__beta"}, got)
	})
}

func TestConfigJSONIsCanonical(t *testing.T) {
	t.Parallel()
	cfg, err := domain.ParseAgentConfig([]byte(`{"tools": {"web_search": { "max_results" : 3 }, "read": true, "shell": false}, "max_iterations": 5}`))
	require.NoError(t, err)
	b, err := json.Marshal(cfg)
	require.NoError(t, err)
	// Byte for byte, not just equivalent JSON: the stored form is canonical.
	want := []byte(`{"max_iterations":5,"tools":{"read":true,"shell":false,"web_search":{"max_results":3}}}`)
	assert.Equal(t, want, b)

	_, err = domain.ParseAgentConfig([]byte(`{"tools": {"read": "yes"}}`))
	require.Error(t, err, "a non-boolean, non-object tool value should be rejected")
}
