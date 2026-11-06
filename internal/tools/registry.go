// Package tools is the registry of every native tool. The tools themselves
// live in submodules:
//
//   - clock:  time
//   - memory: long-term memory search and writes
//   - mcp:    tools from the MCP servers in the MCP config
//   - admin:  managing agents, workflows and runs
//
// General tools (clock, memory, MCP) are opted into by listing their names on
// an agent version; "mcp:<server>" lists every tool from one MCP server.
// Admin tools can't be listed by name; they are injected only when the agent
// has the admin flag, which only a human can set.
package tools

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/tools/admin"
	"github.com/anas-domesticus/personal-agent/internal/tools/clock"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/tools/mcp"
	"github.com/anas-domesticus/personal-agent/internal/tools/memory"
)

type Deps struct {
	Catalog *catalog.Catalog
	MCP     *mcp.Set // optional
}

type Registry struct {
	general map[string]tool.InvokableTool
	admin   []tool.InvokableTool
	mcp     *mcp.Set
}

func New(d Deps) (*Registry, error) {
	r := &Registry{general: map[string]tool.InvokableTool{}, mcp: d.MCP}

	for _, set := range []func() ([]tool.InvokableTool, error){
		clock.Tools,
		func() ([]tool.InvokableTool, error) { return memory.Tools(d.Catalog) },
		func() ([]tool.InvokableTool, error) {
			if d.MCP == nil {
				return nil, nil
			}
			return d.MCP.Tools(), nil
		},
	} {
		ts, err := set()
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			name := toolName(t)
			if _, dup := r.general[name]; dup {
				return nil, fmt.Errorf("duplicate tool %q", name)
			}
			r.general[name] = t
		}
	}

	var err error
	r.admin, err = admin.Tools(admin.Deps{Catalog: d.Catalog, Assignable: r.General})
	if err != nil {
		return nil, err
	}
	// Admin tool names must not collide with general ones, or listing a
	// general tool name could smuggle in an admin tool.
	for _, t := range r.admin {
		if _, dup := r.general[toolName(t)]; dup {
			return nil, fmt.Errorf("tool %q is both general and admin", toolName(t))
		}
	}
	return r, nil
}

// Assignable reports whether an agent version may list this tool by name.
// MCP tools are only assignable while their server is connected.
func (r *Registry) Assignable(name string) bool {
	if server, ok := strings.CutPrefix(name, mcp.ServerPrefix); ok {
		if r.mcp == nil {
			return false
		}
		_, connected := r.mcp.ServerTools(server)
		return connected
	}
	_, ok := r.general[name]
	return ok
}

// For returns an agent's tools: the named general tools, plus every admin
// tool if admin is set.
func (r *Registry) For(names []string, admin bool) ([]tool.BaseTool, error) {
	var out []tool.BaseTool
	seen := map[string]bool{}
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			out = append(out, r.general[n])
		}
	}
	for _, n := range names {
		if server, ok := strings.CutPrefix(n, mcp.ServerPrefix); ok {
			if tools, connected := r.serverTools(server); connected {
				for _, t := range tools {
					add(t)
				}
			} else {
				slog.Warn("mcp server not connected; its tools are unavailable", "server", server)
			}
			continue
		}
		if _, ok := r.general[n]; !ok {
			// An MCP server being down shouldn't make the agent unusable.
			if mcp.IsMCPName(n) {
				slog.Warn("mcp tool unavailable", "tool", n)
				continue
			}
			return nil, fmt.Errorf("unknown tool %q", n)
		}
		add(n)
	}
	if admin {
		for _, t := range r.admin {
			out = append(out, t)
		}
	}
	return out, nil
}

// General lists the assignable tools, including an "mcp:<server>" entry per
// connected MCP server.
func (r *Registry) General() []kit.Info {
	out := make([]kit.Info, 0, len(r.general))
	for name, t := range r.general {
		info, _ := t.Info(context.Background())
		out = append(out, kit.Info{Name: name, Description: info.Desc})
	}
	if r.mcp != nil {
		for _, st := range r.mcp.Status() {
			if st.Error == "" {
				out = append(out, kit.Info{
					Name:        mcp.ServerPrefix + st.Server,
					Description: fmt.Sprintf("Every tool from the %s MCP server (%d tools).", st.Server, st.Tools),
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// WithAgent records which agent is calling tools.
func WithAgent(ctx context.Context, name string) context.Context { return kit.WithAgent(ctx, name) }

func (r *Registry) serverTools(server string) ([]string, bool) {
	if r.mcp == nil {
		return nil, false
	}
	return r.mcp.ServerTools(server)
}

func toolName(t tool.InvokableTool) string {
	info, _ := t.Info(context.Background())
	return info.Name
}
