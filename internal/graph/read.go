package graph

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

const (
	defaultNodeLimit = 10
	maxNodeLimit     = 100
)

func nodeLimit(limit int32) int32 {
	if limit <= 0 {
		return defaultNodeLimit
	}
	return min(limit, maxNodeLimit)
}

// GetNode returns the current raw data for any persisted node kind.
func (g *Graph) GetNode(ctx context.Context, kind string, id uuid.UUID) (domain.KnowledgeNode, error) {
	row, err := g.store.GetGraphNodeCurrent(ctx, db.GetGraphNodeCurrentParams{ID: id, Kind: kind})
	if err != nil {
		return domain.KnowledgeNode{}, graphError(err, "graph node")
	}
	return domain.KnowledgeNode{
		ID: row.ID, Kind: row.Kind, CurrentVersion: row.CurrentVersion, Revision: row.Revision,
		Title: row.Title, Description: row.Description, Data: row.Data,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

// ListNodes returns current raw node data in creation order, breaking ties by
// id. An empty kind lists every persisted kind.
func (g *Graph) ListNodes(ctx context.Context, kind string, limit int32) ([]domain.KnowledgeNode, error) {
	if kind != "" {
		if _, err := g.store.GetGraphNodeType(ctx, kind); err != nil {
			return nil, graphError(err, "graph node kind")
		}
	}
	rows, err := g.store.ListGraphNodes(ctx, db.ListGraphNodesParams{
		AllKinds: kind == "", Kind: kind, Lim: nodeLimit(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list graph nodes: %w", err)
	}
	out := make([]domain.KnowledgeNode, len(rows))
	for i, row := range rows {
		out[i] = domain.KnowledgeNode{
			ID: row.ID, Kind: row.Kind, CurrentVersion: row.CurrentVersion, Revision: row.Revision,
			Title: row.Title, Description: row.Description, Data: row.Data,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
	}
	return out, nil
}
