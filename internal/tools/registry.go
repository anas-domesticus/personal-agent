// Package tools is the registry of every native tool. The tools themselves
// live in submodules:
//
//   - clock:  time
//   - memory: long-term memory search and writes
//   - admin:  managing agents, workflows and runs
//
// General tools (clock, memory) are opted into by listing their names on an
// agent version. Admin tools can't be listed by name; they are injected only
// when the agent has the admin flag, which only a human can set.
package tools

import (
	"context"
	"fmt"
	"sort"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/tools/admin"
	"github.com/anas-domesticus/personal-agent/internal/tools/clock"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/tools/memory"
)

type Deps struct {
	Catalog *catalog.Catalog
}

type Registry struct {
	general map[string]tool.InvokableTool
	admin   []tool.InvokableTool
}

func New(d Deps) (*Registry, error) {
	r := &Registry{general: map[string]tool.InvokableTool{}}

	for _, set := range []func() ([]tool.InvokableTool, error){
		clock.Tools,
		func() ([]tool.InvokableTool, error) { return memory.Tools(d.Catalog) },
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
func (r *Registry) Assignable(name string) bool {
	_, ok := r.general[name]
	return ok
}

// For returns an agent's tools: the named general tools, plus every admin
// tool if admin is set.
func (r *Registry) For(names []string, admin bool) ([]tool.BaseTool, error) {
	var out []tool.BaseTool
	seen := map[string]bool{}
	for _, n := range names {
		t, ok := r.general[n]
		if !ok {
			return nil, fmt.Errorf("unknown tool %q", n)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, t)
		}
	}
	if admin {
		for _, t := range r.admin {
			out = append(out, t)
		}
	}
	return out, nil
}

// General lists the assignable tools.
func (r *Registry) General() []kit.Info {
	out := make([]kit.Info, 0, len(r.general))
	for name, t := range r.general {
		info, _ := t.Info(context.Background())
		out = append(out, kit.Info{Name: name, Description: info.Desc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// WithAgent records which agent is calling tools.
func WithAgent(ctx context.Context, name string) context.Context { return kit.WithAgent(ctx, name) }

func toolName(t tool.InvokableTool) string {
	info, _ := t.Info(context.Background())
	return info.Name
}
