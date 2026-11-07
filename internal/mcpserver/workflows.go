package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

type searchWorkflowsInput struct {
	Query string `json:"query" jsonschema:"Meaning or keywords to search for."`
	Limit int32  `json:"limit,omitempty" jsonschema:"Maximum results, from 1 to 100. Defaults to 10."`
}

type getWorkflowInput struct {
	Name    string `json:"name"`
	Version int32  `json:"version,omitempty" jsonschema:"Optional immutable version to return instead of the current version."`
}

type createWorkflowInput struct {
	Name        string         `json:"name" jsonschema:"Unique name using lowercase letters, digits and hyphens."`
	Description string         `json:"description" jsonschema:"What the workflow is for. Search matches this text."`
	Graph       workflow.Graph `json:"graph" jsonschema:"Directed acyclic graph of agent nodes. Every agent must already exist."`
	Notes       string         `json:"notes,omitempty" jsonschema:"Why this version exists."`
}

type addWorkflowVersionInput struct {
	Name     string         `json:"name"`
	Graph    workflow.Graph `json:"graph"`
	Notes    string         `json:"notes,omitempty" jsonschema:"What changed and why."`
	Activate bool           `json:"activate" jsonschema:"Make the new version current."`
}

type describeWorkflowInput struct {
	Name        string `json:"name"`
	Description string `json:"description" jsonschema:"New description used by workflow search."`
}

type workflowsOutput struct {
	Workflows []domain.Workflow `json:"workflows"`
}

type workflowMatchesOutput struct {
	Workflows []domain.WorkflowMatch `json:"workflows"`
}

type workflowVersionsOutput struct {
	Versions []domain.WorkflowVersion `json:"versions"`
}

type getWorkflowOutput struct {
	Workflow domain.Workflow         `json:"workflow"`
	Version  *domain.WorkflowVersion `json:"version,omitempty"`
}

func registerWorkflowTools(s *mcp.Server, workflows Workflows) {
	add(s, "list_workflows", "List workflows and their current versions.", readOnly, func(ctx context.Context, _ struct{}) (workflowsOutput, error) {
		out, err := workflows.List(ctx)
		return workflowsOutput{Workflows: out}, err
	})
	add(s, "search_workflows", "Find workflows by meaning and keywords.", readOnly, func(ctx context.Context, in searchWorkflowsInput) (workflowMatchesOutput, error) {
		out, err := workflows.Search(ctx, in.Query, boundedLimit(in.Limit))
		return workflowMatchesOutput{Workflows: out}, err
	})
	add(s, "get_workflow", "Get a workflow and its current graph, or a specified immutable version.", readOnly, func(ctx context.Context, in getWorkflowInput) (getWorkflowOutput, error) {
		out, err := workflows.Get(ctx, in.Name)
		if err != nil {
			return getWorkflowOutput{}, err
		}
		result := getWorkflowOutput{Workflow: out.Workflow, Version: out.Current}
		if in.Version == 0 {
			return result, nil
		}
		version, err := workflows.Version(ctx, out.ID, in.Version)
		if err != nil {
			return getWorkflowOutput{}, err
		}
		result.Version = &version
		return result, nil
	})
	add(s, "list_workflow_versions", "List a workflow's immutable graph versions, newest first.", readOnly, func(ctx context.Context, in nameInput) (workflowVersionsOutput, error) {
		out, err := workflows.Versions(ctx, in.Name)
		return workflowVersionsOutput{Versions: out}, err
	})
	add(s, "create_workflow", "Create a workflow DAG with its first immutable version.", additive, func(ctx context.Context, in createWorkflowInput) (domain.WorkflowVersion, error) {
		return workflows.Create(ctx, in.Name, in.Description, in.Graph, in.Notes)
	})
	add(s, "add_workflow_version", "Append an immutable workflow graph version and optionally make it current.", additive, func(ctx context.Context, in addWorkflowVersionInput) (domain.WorkflowVersion, error) {
		return workflows.AddVersion(ctx, in.Name, in.Graph, in.Notes, in.Activate)
	})
	add(s, "set_workflow_version", "Make an existing workflow version current, for promotion or rollback.", closedID, func(ctx context.Context, in versionInput) (struct{}, error) {
		return struct{}{}, workflows.SetVersion(ctx, in.Name, in.Version)
	})
	add(s, "update_workflow_description", "Update the searchable description without changing any graph version.", closedID, func(ctx context.Context, in describeWorkflowInput) (struct{}, error) {
		return struct{}{}, workflows.UpdateDescription(ctx, in.Name, in.Description)
	})
	add(s, "delete_workflow", "Delete a workflow and all its versions. Fails with a conflict error if the workflow has recorded runs.", writing, func(ctx context.Context, in nameInput) (struct{}, error) {
		return struct{}{}, workflows.Delete(ctx, in.Name)
	})
}
