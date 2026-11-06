// Package admin provides the tools for managing agents, workflows and runs.
// They are injected only into agents with the admin flag. None of them can
// change an agent's admin flag.
//
// Each group of tools depends on a small interface; the catalog and runs
// services implement them.
package admin

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

type Agents interface {
	List(ctx context.Context) ([]domain.Agent, error)
	Get(ctx context.Context, name string) (domain.AgentDetail, error)
	Versions(ctx context.Context, name string) ([]domain.AgentVersion, error)
	Create(ctx context.Context, name, description string, v domain.AgentVersionInput) (domain.AgentVersion, error)
	AddVersion(ctx context.Context, name string, v domain.AgentVersionInput, activate bool) (domain.AgentVersion, error)
	SetVersion(ctx context.Context, name string, version int32) error
}

type Workflows interface {
	List(ctx context.Context) ([]domain.Workflow, error)
	Search(ctx context.Context, query string, limit int32) ([]domain.WorkflowMatch, error)
	Get(ctx context.Context, name string) (domain.WorkflowDetail, error)
	Versions(ctx context.Context, name string) ([]domain.WorkflowVersion, error)
	Create(ctx context.Context, name, description string, g workflow.Graph, notes string) (domain.WorkflowVersion, error)
	AddVersion(ctx context.Context, name string, g workflow.Graph, notes string, activate bool) (domain.WorkflowVersion, error)
	SetVersion(ctx context.Context, name string, version int32) error
	UpdateDescription(ctx context.Context, name, description string) error
}

type Runs interface {
	Start(ctx context.Context, workflow string, input json.RawMessage, triggeredBy string) (uuid.UUID, error)
	Get(ctx context.Context, id uuid.UUID) (domain.RunDetail, error)
	List(ctx context.Context, workflow string, limit int32) ([]domain.Run, error)
}

// Tools lists the tools that can be given to agents.
type Tools interface {
	Assignable() []kit.Info
}

type Deps struct {
	Agents    Agents
	Workflows Workflows
	Runs      Runs
	Tools     Tools
}

// Names lists the admin tools, so versions can be validated before the tools
// are built (they need the services that do the validating).
var Names = []string{
	"list_tools", "list_agents", "get_agent", "list_agent_versions", "create_agent", "add_agent_version", "set_agent_version",
	"list_workflows", "search_workflows", "get_workflow", "list_workflow_versions", "create_workflow", "add_workflow_version",
	"set_workflow_version", "update_workflow_description",
	"run_workflow", "get_run", "list_runs",
}

// New builds the admin tools.
func New(d Deps) ([]kit.Spec, error) {
	var all []tool.InvokableTool
	for _, group := range []func() ([]tool.InvokableTool, error){
		func() ([]tool.InvokableTool, error) { return agentTools(d.Agents, d.Tools) },
		func() ([]tool.InvokableTool, error) { return workflowTools(d.Workflows) },
		func() ([]tool.InvokableTool, error) { return runTools(d.Runs) },
	} {
		ts, err := group()
		if err != nil {
			return nil, err
		}
		all = append(all, ts...)
	}
	specs, err := kit.Specs(all, nil)
	for i := range specs {
		specs[i].Admin = true
	}
	return specs, err
}

type nameIn struct {
	Name string `json:"name"`
}

type setVersionIn struct {
	Name    string `json:"name"`
	Version int32  `json:"version"`
}

func marshalOrNil(m map[string]any) json.RawMessage {
	if m == nil {
		return nil
	}
	b, _ := json.Marshal(m)
	return b
}
