// Package agent turns stored agents into runnable Eino agents.
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
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/tools"
)

// AdminName is the agent the TUI talks to.
const AdminName = "admin"

//go:embed admin_prompt.md
var adminPrompt string

// ModelFactory builds a chat model from an OpenRouter slug.
type ModelFactory func(ctx context.Context, slug string) (model.ToolCallingChatModel, error)

type Builder struct {
	Catalog *catalog.Catalog
	Tools   *tools.Registry
	Models  ModelFactory
}

// Built is a runnable agent and the version it was built from.
type Built struct {
	Agent        adk.Agent
	Name         string
	AgentID      uuid.UUID
	Version      int32
	OutputSchema json.RawMessage // nil if the version has none
	Tools        []tool.BaseTool
}

// Build loads the agent's current version and wires up its model and tools.
// Admin agents get the admin tools injected.
func (b *Builder) Build(ctx context.Context, name string) (*Built, error) {
	d, err := b.Catalog.GetAgent(ctx, name)
	if err != nil {
		return nil, err
	}
	if d.Current == nil {
		return nil, fmt.Errorf("agent %q has no current version", name)
	}
	v := d.Current

	ts, err := b.Tools.For(v.Tools, d.Agent.Admin)
	if err != nil {
		return nil, fmt.Errorf("agent %q v%d: %w", name, v.Version, err)
	}

	m, err := b.Models(ctx, v.Model)
	if err != nil {
		return nil, err
	}

	cfg := struct {
		MaxIterations int `json:"max_iterations"`
	}{}
	_ = json.Unmarshal(v.Config, &cfg)

	a, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          name,
		Description:   d.Agent.Description,
		Instruction:   v.SystemPrompt,
		Model:         m,
		ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: ts}},
		MaxIterations: cfg.MaxIterations, // 0 means Eino's default of 20
	})
	if err != nil {
		return nil, fmt.Errorf("build agent %q: %w", name, err)
	}
	return &Built{Agent: a, Name: name, AgentID: d.Agent.ID, Version: v.Version, OutputSchema: v.OutputSchema, Tools: ts}, nil
}

// EnsureAdmin creates the admin agent on first start. Later versions, whether
// written by a human or by the admin agent itself, are left alone.
func EnsureAdmin(ctx context.Context, c *catalog.Catalog, model string) error {
	return c.EnsureAgent(ctx, AdminName,
		"The main agent. Talks to the user and manages the other agents and workflows.",
		true,
		catalog.AgentVersionInput{
			SystemPrompt: adminPrompt,
			Model:        model,
			Tools:        []string{"current_time", "search_memories", "remember"},
			Config:       json.RawMessage(`{"max_iterations": 30}`),
			Notes:        "Seeded on first start.",
		})
}
