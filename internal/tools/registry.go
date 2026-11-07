// Package tools is the registry of every tool a workflow agent can be given.
// Native tools live in subpackages; outbound MCP tools are discovered at
// runtime. Agent versions enable them through config's "tools" object.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/tools/mcp"
)

// MCPSource is the configured MCP servers and their tools. *mcp.Set implements it.
type MCPSource interface {
	Configured(server string) bool
	Tool(name string) (tool.InvokableTool, bool)
	ServerTools(server string) ([]tool.InvokableTool, bool)
	Tools() []tool.InvokableTool
	Status() []mcp.Status
}

// Registry holds the general tools available to workflow agents.
type Registry struct {
	specs map[string]kit.Spec
	mcp   MCPSource
}

// New builds the registry from general tool specs.
func New(general []kit.Spec, mcpSet MCPSource) (*Registry, error) {
	r := &Registry{specs: map[string]kit.Spec{}, mcp: mcpSet}
	for _, s := range general {
		if mcp.IsMCPName(s.Name) {
			return nil, fmt.Errorf("native tool %q uses MCP naming", s.Name)
		}
		if _, dup := r.specs[s.Name]; dup {
			return nil, fmt.Errorf("duplicate tool %q", s.Name)
		}
		r.specs[s.Name] = s
	}
	return r, nil
}

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
	if mcp.IsMCPName(name) {
		if isObject {
			return errors.New("MCP tools take no settings; use true or false")
		}
		return r.checkMCP(name)
	}
	return errors.New("unknown tool; see list_tools")
}

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
		return nil
	}
	if _, exists := r.mcp.Tool(name); !exists {
		return fmt.Errorf("MCP server %q has no tool %q", server, name)
	}
	return nil
}

// Assignable lists tools versions can turn on, with their settings.
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
			if err != nil {
				slog.Warn("skipping tool with invalid settings", "tool", name, "err", err)
				continue
			}
			t, err := s.New(settings)
			if err != nil {
				slog.Warn("skipping tool that can't be built", "tool", name, "err", err)
				continue
			}
			add(t)
			continue
		}
		if server, ok := strings.CutPrefix(name, mcp.ServerPrefix); ok {
			ts, _ := r.mcp.ServerTools(server)
			for _, t := range ts {
				add(t)
			}
			continue
		}
		if t, ok := r.mcp.Tool(name); ok {
			add(t)
		}
	}
	return out
}

// Toolset is the set of tools workflow agents are built from.
type Toolset struct {
	general *Registry
}

// NewToolset returns the toolset backed by this registry.
func (r *Registry) NewToolset() *Toolset { return &Toolset{general: r} }

// Assignable lists tools agent versions can turn on.
func (t *Toolset) Assignable() []kit.Info { return t.general.Assignable() }

// For returns the tools a config turns on.
func (t *Toolset) For(cfg domain.ToolConfig) []tool.BaseTool {
	return t.general.resolve(cfg)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
