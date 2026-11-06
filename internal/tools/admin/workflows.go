package admin

import (
	"context"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

func workflowTools(w Workflows) ([]tool.InvokableTool, error) {
	return kit.Build(
		func() (tool.InvokableTool, error) {
			return kit.New("list_workflows", "List all workflows with their current version.",
				func(ctx context.Context, _ struct{}) ([]domain.Workflow, error) { return w.List(ctx) })
		},
		func() (tool.InvokableTool, error) {
			return kit.New("search_workflows", "Find workflows by what they do, using meaning and keywords.",
				func(ctx context.Context, in searchIn) ([]domain.WorkflowMatch, error) {
					return w.Search(ctx, in.Query, kit.Limit(in.Limit, 10))
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("get_workflow", "Get a workflow and its current version's graph.",
				func(ctx context.Context, in nameIn) (domain.WorkflowDetail, error) { return w.Get(ctx, in.Name) })
		},
		func() (tool.InvokableTool, error) {
			return kit.New("list_workflow_versions", "List every version of a workflow, newest first.",
				func(ctx context.Context, in nameIn) ([]domain.WorkflowVersion, error) {
					return w.Versions(ctx, in.Name)
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("create_workflow",
				"Create a new workflow from a DAG of agent nodes. Its first version becomes current. Every agent named in the graph must already exist.",
				func(ctx context.Context, in createWorkflowIn) (domain.WorkflowVersion, error) {
					return w.Create(ctx, in.Name, in.Description, in.Graph, in.Notes)
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("add_workflow_version",
				"Add a new version of a workflow's graph. Versions are immutable. Set activate to make it current.",
				func(ctx context.Context, in addWorkflowVersionIn) (domain.WorkflowVersion, error) {
					return w.AddVersion(ctx, in.Name, in.Graph, in.Notes, in.Activate)
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("set_workflow_version", "Make an existing workflow version current, e.g. to roll back.",
				func(ctx context.Context, in setVersionIn) (struct{}, error) {
					return struct{}{}, w.SetVersion(ctx, in.Name, in.Version)
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("update_workflow_description", "Change what a workflow says it is for. This is what workflow search matches against.",
				func(ctx context.Context, in describeIn) (struct{}, error) {
					return struct{}{}, w.UpdateDescription(ctx, in.Name, in.Description)
				})
		},
	)
}

type searchIn struct {
	Query string `json:"query"`
	Limit int32  `json:"limit,omitempty" jsonschema_description:"Maximum results, default 10."`
}

type describeIn struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type createWorkflowIn struct {
	Name        string         `json:"name" jsonschema_description:"Unique name: lowercase letters, digits and hyphens."`
	Description string         `json:"description" jsonschema_description:"What the workflow is for. Used to find it later."`
	Graph       workflow.Graph `json:"graph"`
	Notes       string         `json:"notes,omitempty" jsonschema_description:"Why this version exists."`
}

type addWorkflowVersionIn struct {
	Name     string         `json:"name"`
	Graph    workflow.Graph `json:"graph"`
	Notes    string         `json:"notes,omitempty" jsonschema_description:"What changed and why."`
	Activate bool           `json:"activate" jsonschema_description:"Make this version current."`
}
