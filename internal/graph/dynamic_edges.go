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

// GetEdge returns an edge's current immutable snapshot.
func (g *Graph) GetEdge(ctx context.Context, kind string, id uuid.UUID) (domain.KnowledgeEdge, error) {
	row, err := g.store.GetGraphEdge(ctx, db.GetGraphEdgeParams{ID: id, Kind: kind})
	if err != nil {
		return domain.KnowledgeEdge{}, graphError(err, "graph edge")
	}
	return domain.KnowledgeEdge{
		ID: row.ID, Kind: row.Kind, CurrentVersion: row.CurrentVersion,
		SourceID: row.SourceID, SourceKind: row.SourceKind,
		TargetID: row.TargetID, TargetKind: row.TargetKind,
		Data: row.Data, Revision: row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

// ListEdges returns current edges in deterministic creation order. Nil UUIDs
// leave the corresponding endpoint unrestricted.
func (g *Graph) ListEdges(ctx context.Context, kind string, sourceID, targetID uuid.UUID, limit int32) ([]domain.KnowledgeEdge, error) {
	if kind != "" {
		if _, err := g.store.GetGraphEdgeType(ctx, kind); err != nil {
			return nil, graphError(err, "graph edge kind")
		}
	}
	rows, err := g.store.ListGraphEdges(ctx, db.ListGraphEdgesParams{
		AllKinds: kind == "", Kind: kind,
		AllSources: sourceID == uuid.Nil, SourceID: sourceID,
		AllTargets: targetID == uuid.Nil, TargetID: targetID,
		Lim: nodeLimit(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list graph edges: %w", err)
	}
	out := make([]domain.KnowledgeEdge, len(rows))
	for i, row := range rows {
		out[i] = domain.KnowledgeEdge{
			ID: row.ID, Kind: row.Kind, CurrentVersion: row.CurrentVersion,
			SourceID: row.SourceID, SourceKind: row.SourceKind,
			TargetID: row.TargetID, TargetKind: row.TargetKind,
			Data: row.Data, Revision: row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
	}
	return out, nil
}

// UpsertEdge dispatches static kinds through their strict Go decoding and
// custom validators. Other kinds use the persisted dynamic schema directly.
func (g *Graph) UpsertEdge(ctx context.Context, kind string, source, target Ref, raw json.RawMessage, opts EdgeWriteOptions) (domain.KnowledgeEdge, error) {
	g.mu.RLock()
	registered, ok := g.edges[kind]
	g.mu.RUnlock()
	if ok {
		return registered.upsert(ctx, source, target, raw, opts)
	}
	return g.upsertEdge(ctx, kind, source, target, raw, opts)
}

// upsertEdge is the persistence path shared by typed repositories and dynamic
// edges. Typed repositories enter directly to avoid dispatch recursion.
func (g *Graph) upsertEdge(ctx context.Context, kind string, source, target Ref, raw json.RawMessage, opts EdgeWriteOptions) (domain.KnowledgeEdge, error) {
	if source.ID == uuid.Nil || target.ID == uuid.Nil {
		return domain.KnowledgeEdge{}, errors.New("edge endpoints require node IDs")
	}
	definition, err := g.store.GetGraphEdgeType(ctx, kind)
	if err != nil {
		return domain.KnowledgeEdge{}, graphError(err, "graph edge kind")
	}
	compiled, err := compileSchema(kind, definition.DataSchema)
	if err != nil {
		return domain.KnowledgeEdge{}, err
	}
	if err := validateJSONObject(compiled, raw); err != nil {
		return domain.KnowledgeEdge{}, fmt.Errorf("edge %q data: %w", kind, err)
	}
	meta, err := metadata(opts.Metadata)
	if err != nil {
		return domain.KnowledgeEdge{}, err
	}
	id := uuid.Nil
	err = g.store.InTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		// The uniqueness constraint alone cannot serialize two first inserts:
		// the loser would receive 23505 before it could re-read the winner. A
		// transaction-scoped tuple lock makes every caller lock and re-evaluate
		// the same row in turn. Hash collisions only add harmless serialization.
		lockKey := kind + "|" + source.ID.String() + "|" + target.ID.String()
		if err := q.LockGraphEdgeTuple(ctx, lockKey); err != nil {
			return fmt.Errorf("lock graph edge tuple: %w", err)
		}
		row, err := q.GetGraphEdgeBetweenForUpdate(ctx, db.GetGraphEdgeBetweenForUpdateParams{
			Kind: kind, SourceID: source.ID, TargetID: target.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			if opts.ExpectedRevision != nil {
				return fmt.Errorf("new edge has an expected revision: %w", domain.ErrConflict)
			}
			id = uuid.New()
			if _, err := q.InsertGraphEdge(ctx, db.InsertGraphEdgeParams{
				ID: id, Kind: kind,
				SourceID: source.ID, SourceKind: source.Kind,
				TargetID: target.ID, TargetKind: target.Kind,
			}); err != nil {
				return graphError(err, "insert graph edge")
			}
			if _, err := q.InsertGraphEdgeVersion(ctx, db.InsertGraphEdgeVersionParams{
				EdgeID: id, Version: 1, Data: raw, Metadata: meta,
			}); err != nil {
				return fmt.Errorf("insert graph edge version: %w", err)
			}
			return g.queue(ctx, tx, EnrichmentArgs{Entity: EntityEdge, Type: kind, ID: id, Revision: 1})
		}
		if err != nil {
			return fmt.Errorf("lock graph edge: %w", err)
		}
		id = row.ID
		if row.SourceKind != source.Kind || row.TargetKind != target.Kind {
			return fmt.Errorf("edge endpoint kinds cannot change: %w", domain.ErrConflict)
		}
		if err := checkRevision(row.Revision, opts.WriteOptions); err != nil {
			return err
		}
		current, err := q.GetGraphEdgeVersion(ctx, db.GetGraphEdgeVersionParams{EdgeID: row.ID, Version: row.CurrentVersion})
		if err != nil {
			return graphError(err, "current graph edge version")
		}
		if equalJSON(current.Data, raw) {
			return nil
		}
		maxVersion, err := q.GetGraphEdgeMaxVersion(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("read latest graph edge version: %w", err)
		}
		version := maxVersion + 1
		if _, err := q.InsertGraphEdgeVersion(ctx, db.InsertGraphEdgeVersionParams{
			EdgeID: row.ID, Version: version, Data: raw, Metadata: meta,
		}); err != nil {
			return fmt.Errorf("append graph edge version: %w", err)
		}
		updated, err := q.SetGraphEdgeVersion(ctx, db.SetGraphEdgeVersionParams{ID: row.ID, CurrentVersion: version})
		if err != nil {
			return fmt.Errorf("promote graph edge version: %w", err)
		}
		return g.queue(ctx, tx, EnrichmentArgs{Entity: EntityEdge, Type: kind, ID: row.ID, Revision: updated.Revision})
	})
	if err != nil {
		return domain.KnowledgeEdge{}, err
	}
	return g.GetEdge(ctx, kind, id)
}

func validateJSONObject(schema interface{ Validate(any) error }, raw json.RawMessage) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decode graph data: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return errors.New("edge data must be a JSON object")
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("validate graph data: %w", err)
	}
	return nil
}

// DeleteEdge removes an edge and its history after an optimistic revision
// check. Version snapshots themselves cannot be updated independently.
func (g *Graph) DeleteEdge(ctx context.Context, kind string, id uuid.UUID, opts WriteOptions) error {
	expected := int64(0)
	if opts.ExpectedRevision != nil {
		expected = *opts.ExpectedRevision
	}
	count, err := g.store.DeleteGraphEdge(ctx, db.DeleteGraphEdgeParams{
		ID: id, Kind: kind, CheckRevision: opts.ExpectedRevision != nil, ExpectedRevision: expected,
	})
	if err != nil {
		return graphError(err, "delete graph edge")
	}
	if count != 0 {
		return nil
	}
	row, err := g.store.GetGraphEdge(ctx, db.GetGraphEdgeParams{ID: id, Kind: kind})
	if err != nil {
		return graphError(err, "graph edge")
	}
	return checkRevision(row.Revision, opts)
}

// EdgeHistory returns immutable edge snapshots newest first.
func (g *Graph) EdgeHistory(ctx context.Context, kind string, id uuid.UUID, opts HistoryOptions) (domain.KnowledgeEdgeHistory, error) {
	if _, err := g.GetEdge(ctx, kind, id); err != nil {
		return domain.KnowledgeEdgeHistory{}, err
	}
	limit := historyLimit(opts.Limit)
	rows, err := g.store.ListGraphEdgeVersions(ctx, db.ListGraphEdgeVersionsParams{
		EdgeID: id, Column2: opts.Before, Limit: int32(limit + 1),
	})
	if err != nil {
		return domain.KnowledgeEdgeHistory{}, fmt.Errorf("list graph edge versions: %w", err)
	}
	out := domain.KnowledgeEdgeHistory{Versions: make([]domain.KnowledgeEdgeVersion, 0, min(limit, len(rows)))}
	for i, row := range rows {
		if i == limit {
			out.NextBefore = rows[i-1].Version
			break
		}
		out.Versions = append(out.Versions, domain.KnowledgeEdgeVersion{
			EdgeID: row.EdgeID, Version: row.Version, Data: row.Data,
			Metadata: row.Metadata, CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}
