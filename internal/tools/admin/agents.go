package admin

import (
	"context"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

func agentTools(a Agents, tools Tools) ([]tool.InvokableTool, error) {
	return kit.Build(
		func() (tool.InvokableTool, error) {
			return kit.New("list_tools", "List the tools that can be turned on in an agent's config, with the settings each one takes.",
				func(_ context.Context, _ struct{}) ([]kit.Info, error) { return tools.Assignable(), nil })
		},
		func() (tool.InvokableTool, error) {
			return kit.New("list_agents", "List all agents with their current version.",
				func(ctx context.Context, _ struct{}) ([]domain.Agent, error) { return a.List(ctx) })
		},
		func() (tool.InvokableTool, error) {
			return kit.New("get_agent", "Get an agent and its current version's prompt, model, tools and output schema.",
				func(ctx context.Context, in nameIn) (domain.AgentDetail, error) { return a.Get(ctx, in.Name) })
		},
		func() (tool.InvokableTool, error) {
			return kit.New("list_agent_versions", "List every version of an agent, newest first.",
				func(ctx context.Context, in nameIn) ([]domain.AgentVersion, error) { return a.Versions(ctx, in.Name) })
		},
		func() (tool.InvokableTool, error) {
			return kit.New("create_agent", "Create a new agent. Its first version becomes current.",
				func(ctx context.Context, in createAgentIn) (domain.AgentVersion, error) {
					return a.Create(ctx, in.Name, in.Description, in.version())
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("add_agent_version",
				"Add a new version of an existing agent. Versions are immutable, so every change is a new version. Set activate to make it current.",
				func(ctx context.Context, in addAgentVersionIn) (domain.AgentVersion, error) {
					return a.AddVersion(ctx, in.Name, in.version(), in.Activate)
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("set_agent_version", "Make an existing agent version current, e.g. to roll back.",
				func(ctx context.Context, in setVersionIn) (struct{}, error) {
					return struct{}{}, a.SetVersion(ctx, in.Name, in.Version)
				})
		},
	)
}

type agentVersionFields struct {
	SystemPrompt string         `json:"system_prompt" jsonschema_description:"The agent's instructions."`
	Model        string         `json:"model" jsonschema_description:"OpenRouter model slug, e.g. deepseek/deepseek-v4.1-flash or anthropic/claude-sonnet-5.5."`
	OutputSchema map[string]any `json:"output_schema,omitempty" jsonschema_description:"JSON Schema (type object) for the object the agent outputs when run in a workflow. Omit for free text, which is passed on as {\"text\": ...}."`
	Config       map[string]any `json:"config,omitempty" jsonschema_description:"Settings object. tools: which tools the agent has, e.g. {\"web_search\": true, \"subagent\": {\"models\": [\"deepseek/deepseek-v4.1-flash\"]}, \"mcp:composio\": true}; each value is true, false, or that tool's settings (see list_tools). Tools not listed are off. Also max_iterations (model calls per turn, default 20), temperature (0-2), max_tokens."`
	Notes        string         `json:"notes,omitempty" jsonschema_description:"Why this version exists."`
}

func (f agentVersionFields) version() domain.AgentVersionInput {
	return domain.AgentVersionInput{
		SystemPrompt: f.SystemPrompt, Model: f.Model,
		Config: marshalOrNil(f.Config), OutputSchema: marshalOrNil(f.OutputSchema), Notes: f.Notes,
	}
}

type createAgentIn struct {
	Name        string `json:"name" jsonschema_description:"Unique name: lowercase letters, digits and hyphens."`
	Description string `json:"description" jsonschema_description:"What the agent does."`
	agentVersionFields
}

type addAgentVersionIn struct {
	Name     string `json:"name"`
	Activate bool   `json:"activate" jsonschema_description:"Make this version current."`
	agentVersionFields
}
