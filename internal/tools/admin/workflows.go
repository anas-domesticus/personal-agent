package admin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/store/db"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

func workflowTools(d Deps) ([]tool.InvokableTool, error) {
	c := d.Catalog
	return kit.Build(
		func() (tool.InvokableTool, error) {
			return kit.New("list_workflows", "List all workflows with their current version.",
				func(ctx context.Context, _ struct{}) ([]workflowSummary, error) {
					rows, err := c.ListWorkflows(ctx)
					if err != nil {
						return nil, err
					}
					out := make([]workflowSummary, len(rows))
					for i, w := range rows {
						out[i] = workflowSummary{Name: w.Name, Description: w.Description, CurrentVersion: w.CurrentVersion}
					}
					return out, nil
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("search_workflows", "Find workflows by what they do, using meaning and keywords.",
				func(ctx context.Context, in searchIn) ([]workflowSummary, error) {
					rows, err := c.SearchWorkflows(ctx, in.Query, kit.Limit(in.Limit, 10))
					if err != nil {
						return nil, err
					}
					out := make([]workflowSummary, len(rows))
					for i, w := range rows {
						out[i] = workflowSummary{Name: w.Name, Description: w.Description, CurrentVersion: w.CurrentVersion}
					}
					return out, nil
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("get_workflow", "Get a workflow and its current version's graph.",
				func(ctx context.Context, in nameIn) (workflowDetail, error) {
					d, err := c.GetWorkflow(ctx, in.Name)
					if err != nil {
						return workflowDetail{}, err
					}
					out := workflowDetail{workflowSummary: workflowSummary{Name: d.Workflow.Name, Description: d.Workflow.Description, CurrentVersion: d.Workflow.CurrentVersion}}
					if d.Current != nil {
						v := toWorkflowVersion(*d.Current)
						out.Current = &v
					}
					return out, nil
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("list_workflow_versions", "List every version of a workflow, newest first.",
				func(ctx context.Context, in nameIn) ([]workflowVersion, error) {
					rows, err := c.ListWorkflowVersions(ctx, in.Name)
					if err != nil {
						return nil, err
					}
					out := make([]workflowVersion, len(rows))
					for i, v := range rows {
						out[i] = toWorkflowVersion(v)
					}
					return out, nil
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("create_workflow",
				"Create a new workflow from a DAG of agent nodes. Its first version becomes current. Every agent named in the graph must already exist.",
				func(ctx context.Context, in createWorkflowIn) (workflowVersion, error) {
					v, err := c.CreateWorkflow(ctx, in.Name, in.Description, in.Graph, in.Notes)
					return toWorkflowVersion(v), err
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("add_workflow_version",
				"Add a new version of a workflow's graph. Versions are immutable. Set activate to make it current.",
				func(ctx context.Context, in addWorkflowVersionIn) (workflowVersion, error) {
					v, err := c.AddWorkflowVersion(ctx, in.Name, in.Graph, in.Notes, in.Activate)
					return toWorkflowVersion(v), err
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("set_workflow_version", "Make an existing workflow version current, e.g. to roll back.",
				func(ctx context.Context, in setVersionIn) (struct{}, error) {
					return struct{}{}, c.SetWorkflowVersion(ctx, in.Name, in.Version)
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("update_workflow_description", "Change what a workflow says it is for. This is what workflow search matches against.",
				func(ctx context.Context, in describeIn) (struct{}, error) {
					return struct{}{}, c.UpdateWorkflowDescription(ctx, in.Name, in.Description)
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
	Name        string         `json:"name" jsonschema_description:"Unique name, lowercase with hyphens."`
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

type workflowSummary struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	CurrentVersion *int32 `json:"current_version"`
}

type workflowVersion struct {
	Version   int32           `json:"version"`
	Graph     json.RawMessage `json:"graph"`
	Notes     string          `json:"notes"`
	CreatedAt time.Time       `json:"created_at"`
}

type workflowDetail struct {
	workflowSummary
	Current *workflowVersion `json:"current,omitempty"`
}

func toWorkflowVersion(v db.WorkflowVersion) workflowVersion {
	return workflowVersion{Version: v.Version, Graph: v.Graph, Notes: v.Notes, CreatedAt: v.CreatedAt}
}
