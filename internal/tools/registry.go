// Package tools is the registry of every tool an agent can be given. The
// tools themselves live in submodules:
//
//   - clock:  time
//   - memory: long-term memory search and writes
//   - files:  read, write, edit, glob, grep and shell, through a workspace
//   - web:    web search and fetch
//   - mcp:    tools from the MCP servers in the MCP config
//   - admin:  managing agents, workflows and runs
//
// The subagent tool is built by the agent builder (it builds agents), but is
// registered here like any other so it can be configured and validated.
//
// An agent version turns tools on in its config's "tools" object: true, a
// settings object, or false. "mcp:<server>" turns on every tool from one MCP
// server. Admin tools are given to agents with the admin flag (which only
// code can set) and can only be turned off, never on.
//
// Building happens in two steps so nothing needs late binding: the Registry
// of general tools comes first (the agent catalog uses it to validate
// versions), then WithAdmin adds the admin tools, which need the catalog, to
// make the Toolset that agents are built from.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/tools/mcp"
)

// MCPSource is the configured MCP servers and their tools. *mcp.Set
// implements it.
type MCPSource interface {
	Configured(server string) bool
	Tool(name string) (tool.InvokableTool, bool)
	ServerTools(server string) ([]tool.InvokableTool, bool)
	Tools() []tool.InvokableTool
	Status() []mcp.Status
}

// Registry holds the general tools and knows the admin tools' names.
type Registry struct {
	specs map[string]kit.Spec
	admin []string
	mcp   MCPSource
}

// New builds the registry from the general tool specs. adminNames are the
// admin tools' names, so versions can turn them off.
func New(general []kit.Spec, adminNames []string, mcpSet MCPSource) (*Registry, error) {
	r := &Registry{specs: map[string]kit.Spec{}, admin: slices.Clone(adminNames), mcp: mcpSet}
	for _, s := range general {
		switch {
		case s.Admin:
			return nil, fmt.Errorf("tool %q: admin tools go through WithAdmin", s.Name)
		case mcp.IsMCPName(s.Name):
			return nil, fmt.Errorf("native tool %q uses MCP naming", s.Name)
		case slices.Contains(r.admin, s.Name):
			return nil, fmt.Errorf("tool %q is both general and admin", s.Name)
		}
		if _, dup := r.specs[s.Name]; dup {
			return nil, fmt.Errorf("duplicate tool %q", s.Name)
		}
		r.specs[s.Name] = s
	}
	return r, nil
}

var errFlagOnly = errors.New("admin tools come from the admin flag; they can only be turned off (false)")

// Check validates one entry of a version's tools config.
func (r *Registry) Check(name string, value json.RawMessage) error {
	on := string(value) != "false"
	isObject := len(value) > 0 && value[0] == '{'
	if s, ok := r.specs[name]; ok {
		if !on {
			return nil
		}
		_, err := s.DecodeSettings(value)
		return err
	}
	if slices.Contains(r.admin, name) {
		if on {
			return errFlagOnly
		}
		return nil
	}
	if mcp.IsMCPName(name) {
		if isObject {
			return errors.New("MCP tools take no settings; use true or false")
		}
		return r.checkMCP(name)
	}
	return errors.New("unknown tool; see list_tools")
}

// checkMCP judges MCP names by the config, so a version can name a server's
// tools while the server is down.
func (r *Registry) checkMCP(name string) error {
	if server, ok := strings.CutPrefix(name, mcp.ServerPrefix); ok {
		if !r.mcp.Configured(server) {
			return fmt.Errorf("no MCP server %q is configured", server)
		}
		return nil
	}
	server, _, _ := strings.Cut(name, mcp.Separator)
	if !r.mcp.Configured(server) {
		return fmt.Errorf("no MCP server %q is configured", server)
	}
	if _, connected := r.mcp.ServerTools(server); !connected {
		return nil // can't check the tool name until the server is back
	}
	if _, exists := r.mcp.Tool(name); !exists {
		return fmt.Errorf("MCP server %q has no tool %q", server, name)
	}
	return nil
}

// Assignable lists the tools versions can turn on, with their settings: each
// general tool, an "mcp:<server>" entry per configured server, and each
// connected MCP tool.
func (r *Registry) Assignable() []kit.Info {
	out := make([]kit.Info, 0, len(r.specs))
	for _, s := range r.specs {
		out = append(out, kit.Info{Name: s.Name, Description: s.Summary, Settings: s.SettingsHelp})
	}
	for _, st := range r.mcp.Status() {
		desc := fmt.Sprintf("Every tool from the %s MCP server (%d tools).", st.Server, st.Tools)
		if !st.Connected {
			desc = fmt.Sprintf("Every tool from the %s MCP server, which is currently unavailable: %s", st.Server, st.Error)
		}
		out = append(out, kit.Info{Name: mcp.ServerPrefix + st.Server, Description: desc})
	}
	for _, t := range r.mcp.Tools() {
		info, err := t.Info(context.Background())
		if err == nil {
			out = append(out, kit.Info{Name: info.Name, Description: firstLine(info.Desc)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Spec returns a general tool's spec.
func (r *Registry) Spec(name string) (kit.Spec, bool) {
	s, ok := r.specs[name]
	return s, ok
}

// resolve builds the general and MCP tools a config turns on. Tools that
// can't be built, because an MCP server is down, a tool was removed, or its
// settings no longer decode, are logged and skipped: a missing tool must not
// stop an agent, least of all the admin agent that could fix it, from
// running. Specs without a constructor (subagent) are left to the builder.
func (r *Registry) resolve(cfg domain.ToolConfig) []tool.BaseTool {
	var out []tool.BaseTool
	seen := map[string]bool{}
	add := func(t tool.InvokableTool) {
		if n := kit.Name(t); !seen[n] {
			seen[n] = true
			out = append(out, t)
		}
	}
	for _, name := range cfg.Enabled() {
		if s, ok := r.specs[name]; ok {
			if s.New == nil {
				continue
			}
			settings, err := s.DecodeSettings(cfg[name])
			if err == nil {
				var t tool.InvokableTool
				if t, err = s.New(settings); err == nil {
					add(t)
					continue
				}
			}
			slog.Warn("tool can't be built; skipping it", "tool", name, "err", err)
			continue
		}
		if server, ok := strings.CutPrefix(name, mcp.ServerPrefix); ok {
			ts, connected := r.mcp.ServerTools(server)
			if !connected {
				slog.Warn("mcp server not connected; its tools are unavailable", "server", server)
			}
			for _, t := range ts {
				add(t)
			}
			continue
		}
		if t, ok := r.mcp.Tool(name); ok {
			add(t)
			continue
		}
		if !slices.Contains(r.admin, name) {
			slog.Warn("tool unavailable; skipping it", "tool", name)
		}
	}
	return out
}

// Toolset is the general tools plus the admin tools.
type Toolset struct {
	general *Registry
	admin   []tool.InvokableTool
}

// WithAdmin makes the Toolset agents are built from. The admin tools must be
// exactly the ones named when the Registry was made.
func (r *Registry) WithAdmin(admin []kit.Spec) (*Toolset, error) {
	ts := &Toolset{general: r}
	var names []string
	for _, s := range admin {
		if !s.Admin {
			return nil, fmt.Errorf("tool %q is not marked admin", s.Name)
		}
		if _, dup := r.specs[s.Name]; dup || mcp.IsMCPName(s.Name) {
			return nil, fmt.Errorf("tool %q is both general and admin", s.Name)
		}
		t, err := s.New(nil)
		if err != nil {
			return nil, err
		}
		ts.admin = append(ts.admin, t)
		names = append(names, s.Name)
	}
	sort.Strings(names)
	want := slices.Sorted(slices.Values(r.admin))
	if !slices.Equal(names, want) {
		return nil, fmt.Errorf("admin tools %v don't match the registry's admin names %v", names, want)
	}
	return ts, nil
}

// For returns the tools a config turns on, plus the admin tools (except those
// turned off) if admin is set.
func (t *Toolset) For(cfg domain.ToolConfig, admin bool) []tool.BaseTool {
	out := t.general.resolve(cfg)
	if admin {
		for _, a := range t.admin {
			if listed, on := cfg.State(kit.Name(a)); listed && !on {
				continue
			}
			out = append(out, a)
		}
	}
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
