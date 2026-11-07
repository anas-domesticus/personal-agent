package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/embed"
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

	graph    *graph.Graph
	embedder embedding.Embedder
	model    string
}

func registerKnowledge(ctx context.Context, g *graph.Graph, embedder embedding.Embedder, model string) (Knowledge, error) {
	var out Knowledge
	out.graph = g
	out.embedder = embedder
	out.model = model
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

// Upsert writes a predefined node through its registered typed repository.
func (k *Knowledge) Upsert(ctx context.Context, kind string, id uuid.UUID, data json.RawMessage, expectedRevision *int64) (domain.KnowledgeNode, error) {
	return k.graph.UpsertNode(ctx, kind, id, data, graph.NodeWriteOptions{
		WriteOptions: graph.WriteOptions{ExpectedRevision: expectedRevision},
	})
}

// Search finds predefined nodes by semantic similarity to title or description.
func (k *Knowledge) Search(ctx context.Context, kind, query string, limit int32) ([]domain.KnowledgeNodeMatch, error) {
	if k.embedder == nil {
		return nil, fmt.Errorf("knowledge node search: %w", domain.ErrUnavailable)
	}
	vector, err := embed.Text(ctx, k.embedder, query)
	if err != nil {
		return nil, fmt.Errorf("embed knowledge node query: %w", err)
	}
	return k.graph.SearchNodes(ctx, kind, vector, k.model, limit)
}
