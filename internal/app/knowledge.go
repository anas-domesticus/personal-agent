package app

import (
	"context"
	"fmt"

	"github.com/anas-domesticus/personal-agent/internal/graph"
	"github.com/anas-domesticus/personal-agent/internal/knowledge"
)

// Knowledge contains the versioned graph repositories for the base knowledge
// schema. These are internal services and are not automatically MCP tools.
type Knowledge struct {
	Objectives *graph.VersionedNodeRepository[knowledge.Objective]
	Workflows  *graph.VersionedNodeRepository[knowledge.Workflow]
	Claims     *graph.VersionedNodeRepository[knowledge.Claim]
	Sources    *graph.VersionedNodeRepository[knowledge.Source]
	Agents     *graph.VersionedNodeRepository[knowledge.Agent]
}

func registerKnowledge(ctx context.Context, g *graph.Graph) (Knowledge, error) {
	var out Knowledge
	var err error
	if out.Objectives, err = graph.RegisterVersionedNode(ctx, g, graph.NodeSpec[knowledge.Objective]{
		Kind: knowledge.ObjectiveKind, Validate: knowledge.Objective.Validate,
	}); err != nil {
		return Knowledge{}, fmt.Errorf("register objective: %w", err)
	}
	if out.Workflows, err = graph.RegisterVersionedNode(ctx, g, graph.NodeSpec[knowledge.Workflow]{
		Kind: knowledge.WorkflowKind, Validate: knowledge.Workflow.Validate,
	}); err != nil {
		return Knowledge{}, fmt.Errorf("register workflow: %w", err)
	}
	if out.Claims, err = graph.RegisterVersionedNode(ctx, g, graph.NodeSpec[knowledge.Claim]{
		Kind: knowledge.ClaimKind, Validate: knowledge.Claim.Validate,
	}); err != nil {
		return Knowledge{}, fmt.Errorf("register claim: %w", err)
	}
	if out.Sources, err = graph.RegisterVersionedNode(ctx, g, graph.NodeSpec[knowledge.Source]{
		Kind: knowledge.SourceKind, Validate: knowledge.Source.Validate,
	}); err != nil {
		return Knowledge{}, fmt.Errorf("register source: %w", err)
	}
	if out.Agents, err = graph.RegisterVersionedNode(ctx, g, graph.NodeSpec[knowledge.Agent]{
		Kind: knowledge.AgentKind, Validate: knowledge.Agent.Validate,
	}); err != nil {
		return Knowledge{}, fmt.Errorf("register agent: %w", err)
	}
	return out, nil
}
