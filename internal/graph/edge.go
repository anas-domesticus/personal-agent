package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

// EdgeRepository provides typed operations for one registered edge kind.
type EdgeRepository[T any] struct {
	graph     *Graph
	kind      string
	permitted map[Endpoint]bool
	validate  validateData[T]
}

// Definition returns the registered edge definition.
func (r *EdgeRepository[T]) Definition() EdgeType {
	r.graph.mu.RLock()
	defer r.graph.mu.RUnlock()
	return r.graph.edges[r.kind].definition
}

// Get returns an edge by ID.
func (r *EdgeRepository[T]) Get(ctx context.Context, id uuid.UUID) (Edge[T], error) {
	row, err := r.graph.store.GetGraphEdge(ctx, db.GetGraphEdgeParams{ID: id, Kind: r.kind})
	if err != nil {
		return Edge[T]{}, graphError(err, "graph edge")
	}
	return r.fromRow(row)
}

// Between returns the unique edge of this kind between source and target.
func (r *EdgeRepository[T]) Between(ctx context.Context, source, target uuid.UUID) (Edge[T], error) {
	row, err := r.graph.store.GetGraphEdgeBetween(ctx, db.GetGraphEdgeBetweenParams{
		Kind: r.kind, SourceID: source, TargetID: target,
	})
	if err != nil {
		return Edge[T]{}, graphError(err, "graph edge")
	}
	return r.fromRow(row)
}

// Upsert creates or updates the unique edge of this kind between two nodes.
// Endpoint references determine which permitted type combination applies.
func (r *EdgeRepository[T]) Upsert(ctx context.Context, source, target Ref, data T, opts WriteOptions) (Edge[T], error) {
	endpoint := Endpoint{Source: source.Kind, Target: target.Kind}
	if !r.permitted[endpoint] {
		return Edge[T]{}, fmt.Errorf("edge %q does not permit %q to %q: %w", r.kind, source.Kind, target.Kind, domain.ErrConflict)
	}
	if source.ID == uuid.Nil || target.ID == uuid.Nil {
		return Edge[T]{}, errors.New("edge endpoints require node IDs")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return Edge[T]{}, fmt.Errorf("encode graph edge: %w", err)
	}
	if err := r.validate(data, raw); err != nil {
		return Edge[T]{}, err
	}

	var out Edge[T]
	err = r.graph.store.InTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		row, err := q.GetGraphEdgeBetweenForUpdate(ctx, db.GetGraphEdgeBetweenForUpdateParams{
			Kind: r.kind, SourceID: source.ID, TargetID: target.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			if opts.ExpectedRevision != nil {
				return fmt.Errorf("new edge has an expected revision: %w", domain.ErrConflict)
			}
			row, err = q.InsertGraphEdge(ctx, db.InsertGraphEdgeParams{
				ID: uuid.New(), Kind: r.kind,
				SourceID: source.ID, SourceKind: source.Kind,
				TargetID: target.ID, TargetKind: target.Kind, Data: raw,
			})
			if err != nil {
				return graphError(err, "insert graph edge")
			}
			out, err = r.fromRow(row)
			if err != nil {
				return err
			}
			return r.graph.queue(ctx, tx, EnrichmentArgs{Entity: EntityEdge, Type: r.kind, ID: row.ID, Revision: row.Revision})
		}
		if err != nil {
			return fmt.Errorf("lock graph edge: %w", err)
		}
		if row.SourceKind != source.Kind || row.TargetKind != target.Kind {
			return fmt.Errorf("edge endpoint kinds cannot change: %w", domain.ErrConflict)
		}
		if err := checkRevision(row.Revision, opts); err != nil {
			return err
		}
		if equalJSON(row.Data, raw) {
			out, err = r.fromRow(row)
			return err
		}
		row, err = q.UpdateGraphEdgeData(ctx, db.UpdateGraphEdgeDataParams{
			ID: row.ID, Kind: r.kind, SourceID: source.ID, Data: raw,
		})
		if err != nil {
			return fmt.Errorf("update graph edge: %w", err)
		}
		out, err = r.fromRow(row)
		if err != nil {
			return err
		}
		return r.graph.queue(ctx, tx, EnrichmentArgs{Entity: EntityEdge, Type: r.kind, ID: row.ID, Revision: row.Revision})
	})
	return out, err
}

// Delete removes an edge by ID.
func (r *EdgeRepository[T]) Delete(ctx context.Context, id uuid.UUID) error {
	count, err := r.graph.store.DeleteGraphEdge(ctx, db.DeleteGraphEdgeParams{ID: id, Kind: r.kind})
	if err != nil {
		return fmt.Errorf("delete graph edge: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("graph edge %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

// From lists outgoing edges of this kind in creation order.
func (r *EdgeRepository[T]) From(ctx context.Context, source uuid.UUID) ([]Edge[T], error) {
	rows, err := r.graph.store.ListGraphEdgesFrom(ctx, db.ListGraphEdgesFromParams{Kind: r.kind, SourceID: source})
	if err != nil {
		return nil, fmt.Errorf("list outgoing graph edges: %w", err)
	}
	return r.fromRows(rows)
}

// To lists incoming edges of this kind in creation order.
func (r *EdgeRepository[T]) To(ctx context.Context, target uuid.UUID) ([]Edge[T], error) {
	rows, err := r.graph.store.ListGraphEdgesTo(ctx, db.ListGraphEdgesToParams{Kind: r.kind, TargetID: target})
	if err != nil {
		return nil, fmt.Errorf("list incoming graph edges: %w", err)
	}
	return r.fromRows(rows)
}

func (r *EdgeRepository[T]) fromRows(rows []db.GraphEdge) ([]Edge[T], error) {
	out := make([]Edge[T], len(rows))
	for i, row := range rows {
		edge, err := r.fromRow(row)
		if err != nil {
			return nil, err
		}
		out[i] = edge
	}
	return out, nil
}

func (r *EdgeRepository[T]) fromRow(row db.GraphEdge) (Edge[T], error) {
	data, err := decode[T](row.Data)
	if err != nil {
		return Edge[T]{}, err
	}
	return Edge[T]{
		ID: row.ID, Kind: row.Kind,
		Source: Ref{ID: row.SourceID, Kind: row.SourceKind},
		Target: Ref{ID: row.TargetID, Kind: row.TargetKind},
		Data:   data, Revision: row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}
