package graph

import (
	"context"
	"fmt"

	"github.com/pgvector/pgvector-go"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

// SearchNodes finds current nodes by their closest title or description vector.
func (g *Graph) SearchNodes(ctx context.Context, kind string, embedding pgvector.Vector, model string, limit int32) ([]domain.KnowledgeNodeMatch, error) {
	if kind != "" {
		if _, err := g.store.GetGraphNodeType(ctx, kind); err != nil {
			return nil, graphError(err, "graph node kind")
		}
	}
	limit = nodeLimit(limit)
	rows, err := g.store.SearchGraphNodes(ctx, db.SearchGraphNodesParams{
		Embedding: embedding, Model: model, AllKinds: kind == "", Kind: kind, Lim: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("search graph nodes: %w", err)
	}
	out := make([]domain.KnowledgeNodeMatch, len(rows))
	for i, row := range rows {
		out[i] = domain.KnowledgeNodeMatch{
			KnowledgeNode: domain.KnowledgeNode{
				ID: row.ID, Kind: row.Kind, CurrentVersion: row.CurrentVersion, Revision: row.Revision,
				Title: row.Title, Description: row.Description, Data: row.Data,
				CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			},
			Score: row.Score,
		}
	}
	return out, nil
}
