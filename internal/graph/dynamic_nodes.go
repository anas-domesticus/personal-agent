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

func (g *Graph) upsertDynamicNode(ctx context.Context, kind string, id uuid.UUID, raw json.RawMessage, opts NodeWriteOptions) (domain.KnowledgeNode, error) {
	definition, err := g.store.GetGraphNodeType(ctx, kind)
	if err != nil {
		return domain.KnowledgeNode{}, graphError(err, "graph node kind")
	}
	compiled, err := compileSchema(kind, definition.DataSchema)
	if err != nil {
		return domain.KnowledgeNode{}, err
	}
	if err := validateJSON(compiled, raw); err != nil {
		return domain.KnowledgeNode{}, fmt.Errorf("node %q data: %w", kind, err)
	}
	if err := validateRawNodeText(raw); err != nil {
		return domain.KnowledgeNode{}, fmt.Errorf("node %q data: %w", kind, err)
	}
	meta, err := metadata(opts.Metadata)
	if err != nil {
		return domain.KnowledgeNode{}, err
	}
	if id == uuid.Nil {
		id = uuid.New()
	}

	err = g.store.InTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		row, err := q.GetGraphNodeForUpdate(ctx, db.GetGraphNodeForUpdateParams{ID: id, Kind: kind})
		if errors.Is(err, pgx.ErrNoRows) {
			if opts.ExpectedRevision != nil {
				return fmt.Errorf("new node has an expected revision: %w", domain.ErrConflict)
			}
			version := int64(1)
			if _, err := q.InsertGraphNode(ctx, db.InsertGraphNodeParams{
				ID: id, Kind: kind, Versioned: definition.Versioned, CurrentVersion: &version,
			}); err != nil {
				return graphError(err, "insert dynamic graph node")
			}
			// Dynamic definitions are always versioned. The guard prevents a
			// malformed static definition from creating an inconsistent row.
			if !definition.Versioned {
				return fmt.Errorf("dynamic node kind %q is not versioned: %w", kind, domain.ErrConflict)
			}
			if _, err := q.InsertGraphNodeVersion(ctx, db.InsertGraphNodeVersionParams{
				NodeID: id, Version: 1, Data: raw, Metadata: meta,
			}); err != nil {
				return fmt.Errorf("insert dynamic graph node version: %w", err)
			}
			return g.queueNode(ctx, tx, EnrichmentArgs{Entity: EntityNode, Type: kind, ID: id, Revision: 1})
		}
		if err != nil {
			return fmt.Errorf("lock dynamic graph node: %w", err)
		}
		if err := checkRevision(row.Revision, opts.WriteOptions); err != nil {
			return err
		}
		if row.CurrentVersion == nil {
			return fmt.Errorf("dynamic node kind %q is not versioned: %w", kind, domain.ErrConflict)
		}
		current, err := q.GetGraphNodeVersion(ctx, db.GetGraphNodeVersionParams{NodeID: id, Version: *row.CurrentVersion})
		if err != nil {
			return graphError(err, "current dynamic graph node version")
		}
		if equalJSON(current.Data, raw) {
			return nil
		}
		maxVersion, err := q.GetGraphNodeMaxVersion(ctx, id)
		if err != nil {
			return fmt.Errorf("read latest dynamic graph node version: %w", err)
		}
		version := maxVersion + 1
		if _, err := q.InsertGraphNodeVersion(ctx, db.InsertGraphNodeVersionParams{
			NodeID: id, Version: version, Data: raw, Metadata: meta,
		}); err != nil {
			return fmt.Errorf("append dynamic graph node version: %w", err)
		}
		updated, err := q.SetGraphNodeVersion(ctx, db.SetGraphNodeVersionParams{ID: id, Kind: kind, CurrentVersion: &version})
		if err != nil {
			return fmt.Errorf("promote dynamic graph node version: %w", err)
		}
		return g.queueNode(ctx, tx, EnrichmentArgs{Entity: EntityNode, Type: kind, ID: id, Revision: updated.Revision})
	})
	if err != nil {
		return domain.KnowledgeNode{}, err
	}
	return g.GetNode(ctx, kind, id)
}

func validateRawNodeText(raw json.RawMessage) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return errors.New("node data must be a JSON object")
	}
	for _, field := range []string{"title", "description"} {
		value, ok := object[field]
		if !ok {
			return fmt.Errorf("%s is required", field)
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return fmt.Errorf("%s must be a string", field)
		}
	}
	return nil
}

// DeleteNode removes a node after checking its optimistic revision. Postgres
// rejects deletion while any edge still references it.
func (g *Graph) DeleteNode(ctx context.Context, kind string, id uuid.UUID, opts WriteOptions) error {
	expected := int64(0)
	if opts.ExpectedRevision != nil {
		expected = *opts.ExpectedRevision
	}
	count, err := g.store.DeleteGraphNode(ctx, db.DeleteGraphNodeParams{
		ID: id, Kind: kind, CheckRevision: opts.ExpectedRevision != nil, ExpectedRevision: expected,
	})
	if err != nil {
		return graphError(err, "delete graph node")
	}
	if count != 0 {
		return nil
	}
	row, err := g.store.GetGraphNode(ctx, db.GetGraphNodeParams{ID: id, Kind: kind})
	if err != nil {
		return graphError(err, "graph node")
	}
	return checkRevision(row.Revision, opts)
}

// NodeHistory returns immutable node snapshots newest first.
func (g *Graph) NodeHistory(ctx context.Context, kind string, id uuid.UUID, opts HistoryOptions) (domain.KnowledgeNodeHistory, error) {
	if _, err := g.store.GetGraphNode(ctx, db.GetGraphNodeParams{ID: id, Kind: kind}); err != nil {
		return domain.KnowledgeNodeHistory{}, graphError(err, "graph node")
	}
	limit := historyLimit(opts.Limit)
	rows, err := g.store.ListGraphNodeVersions(ctx, db.ListGraphNodeVersionsParams{
		NodeID: id, Column2: opts.Before, Limit: int32(limit + 1),
	})
	if err != nil {
		return domain.KnowledgeNodeHistory{}, fmt.Errorf("list graph node versions: %w", err)
	}
	out := domain.KnowledgeNodeHistory{Versions: make([]domain.KnowledgeNodeVersion, 0, min(limit, len(rows)))}
	for i, row := range rows {
		if i == limit {
			out.NextBefore = rows[i-1].Version
			break
		}
		out.Versions = append(out.Versions, domain.KnowledgeNodeVersion{
			NodeID: row.NodeID, Version: row.Version, Data: row.Data,
			Metadata: row.Metadata, CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}

func historyLimit(limit int) int {
	if limit <= 0 {
		return defaultHistoryLimit
	}
	return min(limit, maxHistoryLimit)
}
