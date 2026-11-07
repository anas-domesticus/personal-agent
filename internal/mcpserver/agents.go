package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

type nameInput struct {
	Name string `json:"name" jsonschema:"Workflow or agent name."`
}

type versionInput struct {
	Name    string `json:"name"`
	Version int32  `json:"version" jsonschema:"Immutable version number to make current."`
}

type agentVersionInput struct {
	SystemPrompt string         `json:"system_prompt" jsonschema:"The agent's instructions."`
	Model        string         `json:"model" jsonschema:"OpenRouter model slug, such as anthropic/claude-sonnet-4.6."`
	OutputSchema map[string]any `json:"output_schema,omitempty" jsonschema:"JSON Schema for the object's structured output. Omit for free text."`
	Config       map[string]any `json:"config,omitempty" jsonschema:"Agent settings, including tools, max_iterations, temperature and max_tokens. Use list_tools to discover tools and their settings."`
	Notes        string         `json:"notes,omitempty" jsonschema:"Why this version exists."`
}

func (in agentVersionInput) version() domain.AgentVersionInput {
	return domain.AgentVersionInput{
		SystemPrompt: in.SystemPrompt,
		Model:        in.Model,
		Config:       rawObject(in.Config),
		OutputSchema: rawObject(in.OutputSchema),
		Notes:        in.Notes,
	}
}

type createAgentInput struct {
	Name        string `json:"name" jsonschema:"Unique name using lowercase letters, digits and hyphens."`
	Description string `json:"description" jsonschema:"What this agent does."`
	agentVersionInput
}

type addAgentVersionInput struct {
	Name     string `json:"name"`
	Activate bool   `json:"activate" jsonschema:"Make the new version current."`
	agentVersionInput
}

type agentsOutput struct {
	Agents []domain.Agent `json:"agents"`
}

type agentVersionsOutput struct {
	Versions []map[string]any `json:"versions"`
}

type toolsOutput struct {
	Tools []kit.Info `json:"tools"`
}

func registerAgentTools(s *mcp.Server, agents Agents, tools Tools) {
	add(s, "list_agents", "List agent definitions and their current versions.", readOnly, func(ctx context.Context, _ struct{}) (agentsOutput, error) {
		out, err := agents.List(ctx)
		return agentsOutput{Agents: out}, err
	})
	add(s, "get_agent", "Get an agent and its current prompt, model, tools and output schema.", readOnly, func(ctx context.Context, in nameInput) (map[string]any, error) {
		out, err := agents.Get(ctx, in.Name)
		if err != nil {
			return nil, err
		}
		return object(out)
	})
	add(s, "list_agent_versions", "List an agent's immutable versions, newest first.", readOnly, func(ctx context.Context, in nameInput) (agentVersionsOutput, error) {
		out, err := agents.Versions(ctx, in.Name)
		if err != nil {
			return agentVersionsOutput{}, err
		}
		versions, err := objects(out)
		return agentVersionsOutput{Versions: versions}, err
	})
	add(s, "create_agent", "Create an agent definition with its first immutable version.", additive, func(ctx context.Context, in createAgentInput) (map[string]any, error) {
		out, err := agents.Create(ctx, in.Name, in.Description, in.version())
		if err != nil {
			return nil, err
		}
		return object(out)
	})
	add(s, "add_agent_version", "Append an immutable agent version and optionally make it current.", additive, func(ctx context.Context, in addAgentVersionInput) (map[string]any, error) {
		out, err := agents.AddVersion(ctx, in.Name, in.version(), in.Activate)
		if err != nil {
			return nil, err
		}
		return object(out)
	})
	add(s, "set_agent_version", "Make an existing agent version current, for promotion or rollback.", closedID, func(ctx context.Context, in versionInput) (struct{}, error) {
		return struct{}{}, agents.SetVersion(ctx, in.Name, in.Version)
	})
	add(s, "list_tools", "List tools available to agent definitions, including their configuration.", readOnly, func(_ context.Context, _ struct{}) (toolsOutput, error) {
		return toolsOutput{Tools: tools.Assignable()}, nil
	})
}
