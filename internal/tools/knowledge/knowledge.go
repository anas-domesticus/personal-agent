// Package knowledge provides native tools for reading and writing the
// application's knowledge graph.
package knowledge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/domain"
)

// Nodes is the consumer contract used by the native knowledge-node tools.
type Nodes interface {
	GetNode(ctx context.Context, kind string, id uuid.UUID) (domain.KnowledgeNode, error)
	ListNodes(ctx context.Context, kind string, limit int32) ([]domain.KnowledgeNode, error)
	UpsertNode(ctx context.Context, kind string, id uuid.UUID, data, metadata json.RawMessage, expectedRevision *int64) (domain.KnowledgeNode, error)
	DeleteNode(ctx context.Context, kind string, id uuid.UUID, expectedRevision *int64) error
	SearchNodes(ctx context.Context, kind, query string, limit int32) ([]domain.KnowledgeNodeMatch, error)
	NodeHistory(ctx context.Context, kind string, id uuid.UUID, before int64, limit int) (domain.KnowledgeNodeHistory, error)
}

// Edges is the consumer contract used by the native knowledge-edge tools.
type Edges interface {
	GetEdge(ctx context.Context, kind string, id uuid.UUID) (domain.KnowledgeEdge, error)
	ListEdges(ctx context.Context, kind string, sourceID, targetID uuid.UUID, limit int32) ([]domain.KnowledgeEdge, error)
	UpsertEdge(ctx context.Context, kind string, source, target domain.KnowledgeRef, data, metadata json.RawMessage, expectedRevision *int64) (domain.KnowledgeEdge, error)
	DeleteEdge(ctx context.Context, kind string, id uuid.UUID, expectedRevision *int64) error
	EdgeHistory(ctx context.Context, kind string, id uuid.UUID, before int64, limit int) (domain.KnowledgeEdgeHistory, error)
}

// Schema is the read-only consumer contract used by the native schema tools.
type Schema interface {
	GetNodeType(ctx context.Context, kind string) (domain.KnowledgeNodeType, error)
	ListNodeTypes(ctx context.Context) ([]domain.KnowledgeNodeType, error)
	GetEdgeType(ctx context.Context, kind string) (domain.KnowledgeEdgeType, error)
	ListEdgeTypes(ctx context.Context) ([]domain.KnowledgeEdgeType, error)
}

type deleteOutput struct{}

func parseID(raw, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse %s: %w", name, err)
	}
	if id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%s must not be zero", name)
	}
	return id, nil
}

func parseOptionalID(raw, name string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, nil
	}
	return parseID(raw, name)
}

func rawObject(name string, value map[string]any) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", name, err)
	}
	return raw, nil
}

func validateHistory(before int64, limit int) error {
	if before < 0 {
		return fmt.Errorf("history before must not be negative")
	}
	if limit < 0 {
		return fmt.Errorf("history limit must not be negative")
	}
	return nil
}
