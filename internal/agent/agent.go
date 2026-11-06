// Package agent turns stored agents into runnable Eino agents, and runs
// subagents.
package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/llm"
)

// AdminName is the agent the TUI talks to.
const AdminName = "admin"

//go:embed admin_prompt.md
var adminPrompt string

// Runnable is a built agent, ready to run.
type Runnable interface {
	Info() Info
	// Run sends the history to the agent and collects everything it
	// produces. onMessage, if set, sees each message as it completes.
	Run(ctx context.Context, history []*schema.Message, onMessage func(*schema.Message)) (Turn, error)
}

// Info identifies what an agent was built from.
type Info struct {
	Name         string
	AgentID      uuid.UUID // uuid.Nil for an ad-hoc subagent
	Version      int32     // 0 for an ad-hoc subagent
	Model        string
	OutputSchema json.RawMessage // nil if the version has none
	Tools        []string
}

// Source loads an agent and its current version. *catalog.Agents implements it.
type Source interface {
	Get(ctx context.Context, name string) (domain.AgentDetail, error)
}

// Toolset builds the tools a config turns on. *tools.Toolset implements it.
type Toolset interface {
	For(cfg domain.ToolConfig, admin bool) []tool.BaseTool
}

// ModelFactory builds a chat model.
type ModelFactory func(ctx context.Context, spec llm.Spec) (model.ToolCallingChatModel, error)

// ModelCatalog checks a model can be used by an agent. *llm.Catalog
// implements it.
type ModelCatalog interface {
	Check(ctx context.Context, slug string) error
}

// Builder builds agents.
type Builder struct {
	Agents Source
	Tools  Toolset
	Models ModelFactory
	// Catalog checks the models subagents are asked to use.
	Catalog ModelCatalog
	// Subagents stores subagent conversations.
	Subagents SubagentStore
}

// Build builds an agent's current version as a top-level agent: admin agents
// get the admin tools.
func (b *Builder) Build(ctx context.Context, name string) (Runnable, error) {
	return b.BuildSpec(ctx, domain.SubagentSpec{Agent: name}, 0)
}

// BuildSpec builds a stored agent (optionally on another model) or an ad-hoc
// worker, at a subagent depth. Only top-level agents (depth 0) get admin
// tools.
func (b *Builder) BuildSpec(ctx context.Context, spec domain.SubagentSpec, depth int) (Runnable, error) {
	if spec.Agent == "" {
		if spec.Model == "" {
			return nil, fmt.Errorf("an ad-hoc subagent needs a model")
		}
		return b.assemble(ctx, assembly{
			info:   Info{Name: "subagent", Model: spec.Model},
			desc:   "An ad-hoc worker for one delegated task.",
			prompt: subagentPrompt,
			cfg:    domain.AgentConfig{Tools: spec.Tools},
			depth:  depth,
		})
	}

	d, err := b.Agents.Get(ctx, spec.Agent)
	if err != nil {
		return nil, err
	}
	if d.Current == nil {
		return nil, fmt.Errorf("agent %q has no current version", spec.Agent)
	}
	v := d.Current
	modelSlug := v.Model
	if spec.Model != "" {
		modelSlug = spec.Model
	}
	return b.assemble(ctx, assembly{
		info:   Info{Name: d.Name, AgentID: d.ID, Version: v.Version, Model: modelSlug, OutputSchema: v.OutputSchema},
		desc:   d.Description,
		prompt: v.SystemPrompt,
		cfg:    v.Config,
		admin:  d.Admin && depth == 0,
		depth:  depth,
	})
}

type assembly struct {
	info   Info
	desc   string
	prompt string
	cfg    domain.AgentConfig
	admin  bool
	depth  int
}

func (b *Builder) assemble(ctx context.Context, a assembly) (Runnable, error) {
	ts := b.Tools.For(a.cfg.Tools, a.admin)
	if st, ok := b.subagentTool(a.cfg.Tools, a.depth); ok {
		ts = append(ts, st)
	}

	m, err := b.Models(ctx, llm.Spec{Model: a.info.Model, Temperature: a.cfg.Temperature, MaxTokens: a.cfg.MaxTokens})
	if err != nil {
		return nil, err
	}
	ag, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          a.info.Name,
		Description:   a.desc,
		Instruction:   a.prompt,
		Model:         m,
		ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: ts}},
		MaxIterations: a.cfg.MaxIterations, // 0 means Eino's default of 20
	})
	if err != nil {
		return nil, fmt.Errorf("build agent %q: %w", a.info.Name, err)
	}
	for _, t := range ts {
		if info, err := t.Info(ctx); err == nil {
			a.info.Tools = append(a.info.Tools, info.Name)
		}
	}
	return &built{agent: ag, info: a.info}, nil
}

// Seeder creates an agent if it's missing. The catalog implements it.
type Seeder interface {
	Ensure(ctx context.Context, name, description string, admin bool, v domain.AgentVersionInput) error
}

// AdminTools are the general tools the admin agent is seeded with. Admin
// tools come from its admin flag.
var AdminTools = []string{
	"current_time", "search_memories", "remember",
	"web_search", "web_fetch", "subagent",
	"read", "write", "edit", "glob", "grep", "shell",
}

// EnsureAdmin creates the admin agent on first start. Later versions, whether
// written by a human or by the admin agent itself, are left alone.
func EnsureAdmin(ctx context.Context, s Seeder, model string) error {
	cfg, err := json.Marshal(domain.AgentConfig{MaxIterations: 30, Tools: domain.On(AdminTools...)})
	if err != nil {
		return fmt.Errorf("encode admin config: %w", err)
	}
	return s.Ensure(ctx, AdminName,
		"The main agent. Talks to the user and manages the other agents and workflows.",
		true,
		domain.AgentVersionInput{
			SystemPrompt: adminPrompt,
			Model:        model,
			Config:       cfg,
			Notes:        "Seeded on first start.",
		})
}
