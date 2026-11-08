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

// Knowledge contains the base repositories and the dynamic graph service.
// Typed repositories support application code; tools use the generic methods.
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

// GetNode returns a registered node's current raw data.
func (k *Knowledge) GetNode(ctx context.Context, kind string, id uuid.UUID) (domain.KnowledgeNode, error) {
	return k.graph.GetNode(ctx, kind, id)
}

// ListNodes returns current raw node data, optionally restricted to one kind.
func (k *Knowledge) ListNodes(ctx context.Context, kind string, limit int32) ([]domain.KnowledgeNode, error) {
	return k.graph.ListNodes(ctx, kind, limit)
}

// UpsertNode writes a node through its registered typed or dynamic schema.
func (k *Knowledge) UpsertNode(ctx context.Context, kind string, id uuid.UUID, data, metadata json.RawMessage, expectedRevision *int64) (domain.KnowledgeNode, error) {
	return k.graph.UpsertNode(ctx, kind, id, data, graph.NodeWriteOptions{
		WriteOptions: graph.WriteOptions{ExpectedRevision: expectedRevision}, Metadata: metadata,
	})
}

// DeleteNode removes a node when no edges reference it.
func (k *Knowledge) DeleteNode(ctx context.Context, kind string, id uuid.UUID, expectedRevision *int64) error {
	return k.graph.DeleteNode(ctx, kind, id, graph.WriteOptions{ExpectedRevision: expectedRevision})
}

// SearchNodes finds nodes by semantic similarity to title or description.
func (k *Knowledge) SearchNodes(ctx context.Context, kind, query string, limit int32) ([]domain.KnowledgeNodeMatch, error) {
	if k.embedder == nil {
		return nil, fmt.Errorf("knowledge node search: %w", domain.ErrUnavailable)
	}
	vector, err := embed.Text(ctx, k.embedder, query)
	if err != nil {
		return nil, fmt.Errorf("embed knowledge node query: %w", err)
	}
	return k.graph.SearchNodes(ctx, kind, vector, k.model, limit)
}

// NodeHistory returns immutable node snapshots newest first.
func (k *Knowledge) NodeHistory(ctx context.Context, kind string, id uuid.UUID, before int64, limit int) (domain.KnowledgeNodeHistory, error) {
	return k.graph.NodeHistory(ctx, kind, id, graph.HistoryOptions{Before: before, Limit: limit})
}

// GetEdge returns a registered edge's current raw data.
func (k *Knowledge) GetEdge(ctx context.Context, kind string, id uuid.UUID) (domain.KnowledgeEdge, error) {
	return k.graph.GetEdge(ctx, kind, id)
}

// ListEdges returns current edges filtered by kind or either endpoint.
func (k *Knowledge) ListEdges(ctx context.Context, kind string, sourceID, targetID uuid.UUID, limit int32) ([]domain.KnowledgeEdge, error) {
	return k.graph.ListEdges(ctx, kind, sourceID, targetID, limit)
}

// UpsertEdge writes an edge through its registered typed or dynamic schema.
func (k *Knowledge) UpsertEdge(ctx context.Context, kind string, source, target domain.KnowledgeRef, data, metadata json.RawMessage, expectedRevision *int64) (domain.KnowledgeEdge, error) {
	return k.graph.UpsertEdge(ctx, kind, graph.Ref{ID: source.ID, Kind: source.Kind}, graph.Ref{ID: target.ID, Kind: target.Kind}, data, graph.EdgeWriteOptions{
		WriteOptions: graph.WriteOptions{ExpectedRevision: expectedRevision}, Metadata: metadata,
	})
}

// DeleteEdge removes an edge and its immutable history.
func (k *Knowledge) DeleteEdge(ctx context.Context, kind string, id uuid.UUID, expectedRevision *int64) error {
	return k.graph.DeleteEdge(ctx, kind, id, graph.WriteOptions{ExpectedRevision: expectedRevision})
}

// EdgeHistory returns immutable edge snapshots newest first.
func (k *Knowledge) EdgeHistory(ctx context.Context, kind string, id uuid.UUID, before int64, limit int) (domain.KnowledgeEdgeHistory, error) {
	return k.graph.EdgeHistory(ctx, kind, id, graph.HistoryOptions{Before: before, Limit: limit})
}

// GetNodeType returns a persisted node schema.
func (k *Knowledge) GetNodeType(ctx context.Context, kind string) (domain.KnowledgeNodeType, error) {
	return k.graph.GetNodeType(ctx, kind)
}

// ListNodeTypes returns every persisted node schema.
func (k *Knowledge) ListNodeTypes(ctx context.Context) ([]domain.KnowledgeNodeType, error) {
	return k.graph.ListNodeTypes(ctx)
}

// GetEdgeType returns a persisted edge schema and its permitted endpoints.
func (k *Knowledge) GetEdgeType(ctx context.Context, kind string) (domain.KnowledgeEdgeType, error) {
	return k.graph.GetEdgeType(ctx, kind)
}

// ListEdgeTypes returns every persisted edge schema.
func (k *Knowledge) ListEdgeTypes(ctx context.Context) ([]domain.KnowledgeEdgeType, error) {
	return k.graph.ListEdgeTypes(ctx)
}

// CreateNodeType adds a versioned dynamic node schema.
func (k *Knowledge) CreateNodeType(ctx context.Context, kind string, dataSchema json.RawMessage) (domain.KnowledgeNodeType, error) {
	return k.graph.CreateNodeType(ctx, kind, dataSchema)
}

// CreateEdgeType adds a dynamic edge schema and its permitted endpoints.
func (k *Knowledge) CreateEdgeType(ctx context.Context, kind string, dataSchema json.RawMessage, endpoints []domain.KnowledgeEndpoint) (domain.KnowledgeEdgeType, error) {
	converted := make([]graph.Endpoint, len(endpoints))
	for i, endpoint := range endpoints {
		converted[i] = graph.Endpoint{Source: endpoint.Source, Target: endpoint.Target}
	}
	return k.graph.CreateEdgeType(ctx, kind, dataSchema, converted)
}
