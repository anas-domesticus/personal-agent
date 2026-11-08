package graph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

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
	edge, err := r.graph.GetEdge(ctx, r.kind, id)
	if err != nil {
		return Edge[T]{}, err
	}
	return typedEdge[T](edge)
}

// Current returns current data together with the immutable version number.
func (r *EdgeRepository[T]) Current(ctx context.Context, id uuid.UUID) (VersionedEdge[T], error) {
	edge, err := r.graph.GetEdge(ctx, r.kind, id)
	if err != nil {
		return VersionedEdge[T]{}, err
	}
	typed, err := typedEdge[T](edge)
	if err != nil {
		return VersionedEdge[T]{}, err
	}
	return VersionedEdge[T]{Edge: typed, CurrentVersion: edge.CurrentVersion}, nil
}

// Version returns one immutable historical edge snapshot.
func (r *EdgeRepository[T]) Version(ctx context.Context, id uuid.UUID, version int64) (EdgeVersion[T], error) {
	if _, err := r.graph.GetEdge(ctx, r.kind, id); err != nil {
		return EdgeVersion[T]{}, err
	}
	row, err := r.graph.store.GetGraphEdgeVersion(ctx, db.GetGraphEdgeVersionParams{EdgeID: id, Version: version})
	if err != nil {
		return EdgeVersion[T]{}, graphError(err, "graph edge version")
	}
	data, err := decode[T](row.Data)
	if err != nil {
		return EdgeVersion[T]{}, err
	}
	return EdgeVersion[T]{EdgeID: row.EdgeID, Version: row.Version, Data: data, Metadata: row.Metadata, CreatedAt: row.CreatedAt}, nil
}

// Versions returns immutable edge snapshots newest first.
func (r *EdgeRepository[T]) Versions(ctx context.Context, id uuid.UUID, opts HistoryOptions) (EdgeVersionHistory[T], error) {
	history, err := r.graph.EdgeHistory(ctx, r.kind, id, opts)
	if err != nil {
		return EdgeVersionHistory[T]{}, err
	}
	out := EdgeVersionHistory[T]{Versions: make([]EdgeVersion[T], 0, len(history.Versions)), NextBefore: history.NextBefore}
	for _, version := range history.Versions {
		data, err := decode[T](version.Data)
		if err != nil {
			return EdgeVersionHistory[T]{}, err
		}
		out.Versions = append(out.Versions, EdgeVersion[T]{
			EdgeID: version.EdgeID, Version: version.Version, Data: data,
			Metadata: version.Metadata, CreatedAt: version.CreatedAt,
		})
	}
	return out, nil
}

// Between returns the unique edge of this kind between source and target.
func (r *EdgeRepository[T]) Between(ctx context.Context, source, target uuid.UUID) (Edge[T], error) {
	row, err := r.graph.store.GetGraphEdgeBetween(ctx, db.GetGraphEdgeBetweenParams{
		Kind: r.kind, SourceID: source, TargetID: target,
	})
	if err != nil {
		return Edge[T]{}, graphError(err, "graph edge")
	}
	return typedEdge[T](domain.KnowledgeEdge{
		ID: row.ID, Kind: row.Kind, CurrentVersion: row.CurrentVersion,
		SourceID: row.SourceID, SourceKind: row.SourceKind,
		TargetID: row.TargetID, TargetKind: row.TargetKind,
		Data: row.Data, Revision: row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	})
}

// Upsert creates or updates the unique edge of this kind between two nodes.
// Endpoint references determine which permitted type combination applies.
func (r *EdgeRepository[T]) Upsert(ctx context.Context, source, target Ref, data T, opts WriteOptions) (Edge[T], error) {
	endpoint := Endpoint{Source: source.Kind, Target: target.Kind}
	if !r.permitted[endpoint] {
		return Edge[T]{}, fmt.Errorf("edge %q does not permit %q to %q: %w", r.kind, source.Kind, target.Kind, domain.ErrConflict)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return Edge[T]{}, fmt.Errorf("encode graph edge: %w", err)
	}
	if err := r.validate(data, raw); err != nil {
		return Edge[T]{}, err
	}
	edge, err := r.graph.upsertEdge(ctx, r.kind, source, target, raw, EdgeWriteOptions{WriteOptions: opts})
	if err != nil {
		return Edge[T]{}, err
	}
	return typedEdge[T](edge)
}

// UpsertVersion is equivalent to Upsert and additionally stores version
// metadata on a newly appended immutable snapshot.
func (r *EdgeRepository[T]) UpsertVersion(ctx context.Context, source, target Ref, data T, opts EdgeWriteOptions) (VersionedEdge[T], error) {
	endpoint := Endpoint{Source: source.Kind, Target: target.Kind}
	if !r.permitted[endpoint] {
		return VersionedEdge[T]{}, fmt.Errorf("edge %q does not permit %q to %q: %w", r.kind, source.Kind, target.Kind, domain.ErrConflict)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return VersionedEdge[T]{}, fmt.Errorf("encode graph edge: %w", err)
	}
	if err := r.validate(data, raw); err != nil {
		return VersionedEdge[T]{}, err
	}
	edge, err := r.graph.upsertEdge(ctx, r.kind, source, target, raw, opts)
	if err != nil {
		return VersionedEdge[T]{}, err
	}
	typed, err := typedEdge[T](edge)
	if err != nil {
		return VersionedEdge[T]{}, err
	}
	return VersionedEdge[T]{Edge: typed, CurrentVersion: edge.CurrentVersion}, nil
}

// Delete removes an edge by ID.
func (r *EdgeRepository[T]) Delete(ctx context.Context, id uuid.UUID) error {
	return r.graph.DeleteEdge(ctx, r.kind, id, WriteOptions{})
}

// DeleteRevision removes an edge after checking its optimistic revision.
func (r *EdgeRepository[T]) DeleteRevision(ctx context.Context, id uuid.UUID, opts WriteOptions) error {
	return r.graph.DeleteEdge(ctx, r.kind, id, opts)
}

// From lists all outgoing edges of this kind in creation order.
func (r *EdgeRepository[T]) From(ctx context.Context, source uuid.UUID) ([]Edge[T], error) {
	rows, err := r.graph.store.ListGraphEdgesFrom(ctx, db.ListGraphEdgesFromParams{Kind: r.kind, SourceID: source})
	if err != nil {
		return nil, fmt.Errorf("list outgoing graph edges: %w", err)
	}
	out := make([]Edge[T], len(rows))
	for i, row := range rows {
		out[i], err = typedEdge[T](domain.KnowledgeEdge{
			ID: row.ID, Kind: row.Kind, CurrentVersion: row.CurrentVersion,
			SourceID: row.SourceID, SourceKind: row.SourceKind,
			TargetID: row.TargetID, TargetKind: row.TargetKind,
			Data: row.Data, Revision: row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// To lists all incoming edges of this kind in creation order.
func (r *EdgeRepository[T]) To(ctx context.Context, target uuid.UUID) ([]Edge[T], error) {
	rows, err := r.graph.store.ListGraphEdgesTo(ctx, db.ListGraphEdgesToParams{Kind: r.kind, TargetID: target})
	if err != nil {
		return nil, fmt.Errorf("list incoming graph edges: %w", err)
	}
	out := make([]Edge[T], len(rows))
	for i, row := range rows {
		out[i], err = typedEdge[T](domain.KnowledgeEdge{
			ID: row.ID, Kind: row.Kind, CurrentVersion: row.CurrentVersion,
			SourceID: row.SourceID, SourceKind: row.SourceKind,
			TargetID: row.TargetID, TargetKind: row.TargetKind,
			Data: row.Data, Revision: row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func typedEdge[T any](edge domain.KnowledgeEdge) (Edge[T], error) {
	data, err := decode[T](edge.Data)
	if err != nil {
		return Edge[T]{}, err
	}
	return Edge[T]{
		ID: edge.ID, Kind: edge.Kind,
		Source: Ref{ID: edge.SourceID, Kind: edge.SourceKind},
		Target: Ref{ID: edge.TargetID, Kind: edge.TargetKind},
		Data:   data, Revision: edge.Revision, CreatedAt: edge.CreatedAt, UpdatedAt: edge.UpdatedAt,
	}, nil
}
