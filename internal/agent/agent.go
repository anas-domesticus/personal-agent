// Package agent turns stored agents into runnable Eino agents, and runs
// subagents.
package agent

import (
	"context"
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

// Runnable is a built agent, ready to run.
type Runnable interface {
	Info() Info
	// Run sends the history to the agent and collects everything it
	// produces, reporting it to obs as it happens.
	Run(ctx context.Context, history []*schema.Message, obs Observer) (Turn, error)
}

// Observer watches a turn as it happens. Every field is optional.
type Observer struct {
	// Started is called once, before the model is first called.
	Started func(Info)
	// Delta sees fragments of each assistant message as the model streams
	// it. Setting it makes the model stream; Message still sees the
	// complete message afterwards.
	Delta func(chunk *schema.Message)
	// Message sees each message once it is complete.
	Message func(*schema.Message)
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
	For(cfg domain.ToolConfig) []tool.BaseTool
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

// Build builds an agent's current version.
func (b *Builder) Build(ctx context.Context, name string) (Runnable, error) {
	return b.BuildSpec(ctx, domain.SubagentSpec{Agent: name}, 0)
}

// BuildSpec builds a stored agent (optionally on another model) or an ad-hoc
// worker at a subagent depth.
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
		depth:  depth,
	})
}

type assembly struct {
	info   Info
	desc   string
	prompt string
	cfg    domain.AgentConfig
	depth  int
}

func (b *Builder) assemble(ctx context.Context, a assembly) (Runnable, error) {
	ts := b.Tools.For(a.cfg.Tools)
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
	return newBuilt(ag, a.info), nil
}
