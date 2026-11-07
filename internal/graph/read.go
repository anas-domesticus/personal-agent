package graph

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

const (
	defaultNodeListLimit = 10
	maxNodeListLimit     = 100
)

// GetNode returns the current raw data for a registered node kind.
func (g *Graph) GetNode(ctx context.Context, kind string, id uuid.UUID) (domain.KnowledgeNode, error) {
	if !g.hasNodeKind(kind) {
		return domain.KnowledgeNode{}, fmt.Errorf("graph node kind %q: %w", kind, domain.ErrNotFound)
	}
	row, err := g.store.GetGraphNodeCurrent(ctx, db.GetGraphNodeCurrentParams{ID: id, Kind: kind})
	if err != nil {
		return domain.KnowledgeNode{}, graphError(err, "graph node")
	}
	return domain.KnowledgeNode{
		ID: row.ID, Kind: row.Kind, Revision: row.Revision,
		Title: row.Title, Description: row.Description, Data: row.Data,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

// ListNodes returns current raw node data in creation order, breaking ties by
// id. An empty kind lists every registered kind.
func (g *Graph) ListNodes(ctx context.Context, kind string, limit int32) ([]domain.KnowledgeNode, error) {
	if kind != "" && !g.hasNodeKind(kind) {
		return nil, fmt.Errorf("graph node kind %q: %w", kind, domain.ErrNotFound)
	}
	if limit <= 0 {
		limit = defaultNodeListLimit
	}
	limit = min(limit, maxNodeListLimit)
	rows, err := g.store.ListGraphNodes(ctx, db.ListGraphNodesParams{
		AllKinds: kind == "", Kind: kind, Lim: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list graph nodes: %w", err)
	}
	out := make([]domain.KnowledgeNode, len(rows))
	for i, row := range rows {
		out[i] = domain.KnowledgeNode{
			ID: row.ID, Kind: row.Kind, Revision: row.Revision,
			Title: row.Title, Description: row.Description, Data: row.Data,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
	}
	return out, nil
}

func (g *Graph) hasNodeKind(kind string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.nodes[kind]
	return ok
}
