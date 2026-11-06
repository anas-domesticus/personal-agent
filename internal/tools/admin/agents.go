package admin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

func agentTools(d Deps) ([]tool.InvokableTool, error) {
	c := d.Catalog
	return kit.Build(
		func() (tool.InvokableTool, error) {
			return kit.New("list_tools", "List the tools that can be given to agents.",
				func(_ context.Context, _ struct{}) ([]kit.Info, error) { return d.Assignable(), nil })
		},
		func() (tool.InvokableTool, error) {
			return kit.New("list_agents", "List all agents with their current version.",
				func(ctx context.Context, _ struct{}) ([]agentSummary, error) {
					rows, err := c.ListAgents(ctx)
					if err != nil {
						return nil, err
					}
					out := make([]agentSummary, len(rows))
					for i, a := range rows {
						out[i] = summarizeAgent(a)
					}
					return out, nil
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("get_agent", "Get an agent and its current version's prompt, model, tools and output schema.",
				func(ctx context.Context, in nameIn) (agentDetail, error) {
					d, err := c.GetAgent(ctx, in.Name)
					if err != nil {
						return agentDetail{}, err
					}
					out := agentDetail{agentSummary: summarizeAgent(d.Agent)}
					if d.Current != nil {
						v := toAgentVersion(*d.Current)
						out.Current = &v
					}
					return out, nil
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("list_agent_versions", "List every version of an agent, newest first.",
				func(ctx context.Context, in nameIn) ([]agentVersion, error) {
					rows, err := c.ListAgentVersions(ctx, in.Name)
					if err != nil {
						return nil, err
					}
					out := make([]agentVersion, len(rows))
					for i, v := range rows {
						out[i] = toAgentVersion(v)
					}
					return out, nil
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("create_agent", "Create a new agent. Its first version becomes current.",
				func(ctx context.Context, in createAgentIn) (agentVersion, error) {
					v, err := c.CreateAgent(ctx, in.Name, in.Description, in.version())
					return toAgentVersion(v), err
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("add_agent_version",
				"Add a new version of an existing agent. Versions are immutable, so every change is a new version. Set activate to make it current.",
				func(ctx context.Context, in addAgentVersionIn) (agentVersion, error) {
					v, err := c.AddAgentVersion(ctx, in.Name, in.version(), in.Activate)
					return toAgentVersion(v), err
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("set_agent_version", "Make an existing agent version current, e.g. to roll back.",
				func(ctx context.Context, in setVersionIn) (struct{}, error) {
					return struct{}{}, c.SetAgentVersion(ctx, in.Name, in.Version)
				})
		},
	)
}

type agentVersionFields struct {
	SystemPrompt string         `json:"system_prompt" jsonschema_description:"The agent's instructions."`
	Model        string         `json:"model" jsonschema_description:"OpenRouter model slug, e.g. deepseek/deepseek-v4.1-flash or anthropic/claude-sonnet-5.5."`
	Tools        []string       `json:"tools,omitempty" jsonschema_description:"Names of tools from list_tools."`
	OutputSchema map[string]any `json:"output_schema,omitempty" jsonschema_description:"JSON Schema (type object) for the object the agent outputs when run in a workflow. Omit for free text, which is passed on as {\"text\": ...}."`
	Config       map[string]any `json:"config,omitempty" jsonschema_description:"Settings such as max_iterations."`
	Notes        string         `json:"notes,omitempty" jsonschema_description:"Why this version exists."`
}

func (f agentVersionFields) version() catalog.AgentVersionInput {
	return catalog.AgentVersionInput{
		SystemPrompt: f.SystemPrompt, Model: f.Model, Tools: f.Tools,
		Config: marshalOrNil(f.Config), OutputSchema: marshalOrNil(f.OutputSchema), Notes: f.Notes,
	}
}

type createAgentIn struct {
	Name        string `json:"name" jsonschema_description:"Unique name, lowercase with hyphens."`
	Description string `json:"description" jsonschema_description:"What the agent does."`
	agentVersionFields
}

type addAgentVersionIn struct {
	Name     string `json:"name"`
	Activate bool   `json:"activate" jsonschema_description:"Make this version current."`
	agentVersionFields
}

type agentSummary struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	CurrentVersion *int32 `json:"current_version"`
	Admin          bool   `json:"admin"`
}

type agentVersion struct {
	Version      int32           `json:"version"`
	SystemPrompt string          `json:"system_prompt"`
	Model        string          `json:"model"`
	Tools        []string        `json:"tools"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	Config       json.RawMessage `json:"config"`
	Notes        string          `json:"notes"`
	CreatedAt    time.Time       `json:"created_at"`
}

type agentDetail struct {
	agentSummary
	Current *agentVersion `json:"current,omitempty"`
}

func summarizeAgent(a db.Agent) agentSummary {
	return agentSummary{Name: a.Name, Description: a.Description, CurrentVersion: a.CurrentVersion, Admin: a.Admin}
}

func toAgentVersion(v db.AgentVersion) agentVersion {
	return agentVersion{
		Version: v.Version, SystemPrompt: v.SystemPrompt, Model: v.Model, Tools: v.Tools,
		OutputSchema: v.OutputSchema, Config: v.Config, Notes: v.Notes, CreatedAt: v.CreatedAt,
	}
}
