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
	Objectives  *graph.VersionedNodeRepository[knowledge.Objective]
	Workflows   *graph.VersionedNodeRepository[knowledge.Workflow]
	Claims      *graph.VersionedNodeRepository[knowledge.Claim]
	Sources     *graph.VersionedNodeRepository[knowledge.Source]
	Agents      *graph.VersionedNodeRepository[knowledge.Agent]
	SupportedBy *graph.EdgeRepository[knowledge.SupportedBy]

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
	if out.SupportedBy, err = graph.RegisterEdge(ctx, g, graph.EdgeSpec[knowledge.SupportedBy]{
		Kind:      knowledge.SupportedByKind,
		Endpoints: []graph.Endpoint{{Source: knowledge.ClaimKind, Target: knowledge.SourceKind}},
	}); err != nil {
		return Knowledge{}, fmt.Errorf("register supported_by: %w", err)
	}
	return out, nil
}

// Get returns a predefined node's current raw data.
func (k *Knowledge) Get(ctx context.Context, kind string, id uuid.UUID) (domain.KnowledgeNode, error) {
	return k.graph.GetNode(ctx, kind, id)
}

// List returns current raw node data, optionally restricted to one kind.
func (k *Knowledge) List(ctx context.Context, kind string, limit int32) ([]domain.KnowledgeNode, error) {
	return k.graph.ListNodes(ctx, kind, limit)
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

// AddSupportedBy creates or returns the relationship from a claim to a source.
func (k *Knowledge) AddSupportedBy(ctx context.Context, claimID, sourceID uuid.UUID) (domain.KnowledgeEdge, error) {
	edge, err := k.SupportedBy.Upsert(ctx,
		graph.Ref{ID: claimID, Kind: knowledge.ClaimKind},
		graph.Ref{ID: sourceID, Kind: knowledge.SourceKind},
		knowledge.SupportedBy{}, graph.WriteOptions{},
	)
	if err != nil {
		return domain.KnowledgeEdge{}, err
	}
	return knowledgeEdge(edge)
}

// DeleteSupportedBy removes a support relationship by edge ID.
func (k *Knowledge) DeleteSupportedBy(ctx context.Context, edgeID uuid.UUID) error {
	return k.SupportedBy.Delete(ctx, edgeID)
}

// ListSupportedBy lists relationships for exactly one claim or source selector.
func (k *Knowledge) ListSupportedBy(ctx context.Context, claimID, sourceID uuid.UUID) ([]domain.KnowledgeEdge, error) {
	if (claimID == uuid.Nil) == (sourceID == uuid.Nil) {
		return nil, fmt.Errorf("exactly one supported_by selector is required")
	}
	var (
		edges []graph.Edge[knowledge.SupportedBy]
		err   error
	)
	if claimID != uuid.Nil {
		edges, err = k.SupportedBy.From(ctx, claimID)
	} else {
		edges, err = k.SupportedBy.To(ctx, sourceID)
	}
	if err != nil {
		return nil, err
	}
	out := make([]domain.KnowledgeEdge, len(edges))
	for i, edge := range edges {
		out[i], err = knowledgeEdge(edge)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func knowledgeEdge[T any](edge graph.Edge[T]) (domain.KnowledgeEdge, error) {
	data, err := json.Marshal(edge.Data)
	if err != nil {
		return domain.KnowledgeEdge{}, fmt.Errorf("encode graph edge data: %w", err)
	}
	return domain.KnowledgeEdge{
		ID: edge.ID, Kind: edge.Kind,
		SourceID: edge.Source.ID, SourceKind: edge.Source.Kind,
		TargetID: edge.Target.ID, TargetKind: edge.Target.Kind,
		Data: data, Revision: edge.Revision, CreatedAt: edge.CreatedAt, UpdatedAt: edge.UpdatedAt,
	}, nil
}
