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

const (
	defaultHistoryLimit = 50
	maxHistoryLimit     = 100
)

// NodeRepository provides typed operations for one registered node kind.
type NodeRepository[T any] struct {
	graph    *Graph
	kind     string
	validate validateData[T]
}

// VersionedNodeRepository adds immutable history and rollback to the common
// node repository.
type VersionedNodeRepository[T any] struct {
	*NodeRepository[T]
}

// Ref returns a reference to a node of this repository's kind.
func (r *NodeRepository[T]) Ref(id uuid.UUID) Ref {
	return Ref{ID: id, Kind: r.kind}
}

// Definition returns the registered node definition.
func (r *NodeRepository[T]) Definition() NodeType {
	r.graph.mu.RLock()
	defer r.graph.mu.RUnlock()
	return r.graph.nodes[r.kind].definition
}

// Get returns the node's current data, following its version pointer when
// necessary.
func (r *NodeRepository[T]) Get(ctx context.Context, id uuid.UUID) (Node[T], error) {
	row, err := r.graph.store.GetGraphNode(ctx, db.GetGraphNodeParams{ID: id, Kind: r.kind})
	if err != nil {
		return Node[T]{}, graphError(err, "graph node")
	}
	return r.fromRow(ctx, r.graph.store.Queries, row)
}

// Upsert creates a node or changes its current data. Versioned repositories
// append a version only when the JSON value changes. Unchanged writes neither
// increment the revision nor queue enrichment.
func (r *NodeRepository[T]) Upsert(ctx context.Context, id uuid.UUID, data T, opts NodeWriteOptions) (Node[T], error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Node[T]{}, fmt.Errorf("encode graph node: %w", err)
	}
	if err := r.validate(data, raw); err != nil {
		return Node[T]{}, err
	}
	meta, err := metadata(opts.Metadata)
	if err != nil {
		return Node[T]{}, err
	}
	if id == uuid.Nil {
		id = uuid.New()
	}

	var out Node[T]
	err = r.graph.store.InTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		row, err := q.GetGraphNodeForUpdate(ctx, db.GetGraphNodeForUpdateParams{ID: id, Kind: r.kind})
		if errors.Is(err, pgx.ErrNoRows) {
			if opts.ExpectedRevision != nil {
				return fmt.Errorf("new node has an expected revision: %w", domain.ErrConflict)
			}
			out, err = r.insert(ctx, q, id, raw, meta)
			if err != nil {
				return err
			}
			return r.graph.queueNode(ctx, tx, EnrichmentArgs{Entity: EntityNode, Type: r.kind, ID: id, Revision: out.Revision})
		}
		if err != nil {
			return fmt.Errorf("lock graph node: %w", err)
		}
		if err := checkRevision(row.Revision, opts.WriteOptions); err != nil {
			return err
		}
		current, err := r.fromRow(ctx, q, row)
		if err != nil {
			return err
		}
		if equalJSON(currentRaw(current), raw) {
			out = current
			return nil
		}
		if row.Versioned {
			return r.appendVersion(ctx, q, tx, row, raw, meta, &out)
		}
		updated, err := q.UpdateGraphNodeData(ctx, db.UpdateGraphNodeDataParams{ID: id, Kind: r.kind, Data: raw})
		if err != nil {
			return fmt.Errorf("update graph node: %w", err)
		}
		out, err = r.fromRow(ctx, q, updated)
		if err != nil {
			return err
		}
		return r.graph.queueNode(ctx, tx, EnrichmentArgs{Entity: EntityNode, Type: r.kind, ID: id, Revision: out.Revision})
	})
	return out, err
}

func (r *NodeRepository[T]) insert(ctx context.Context, q *db.Queries, id uuid.UUID, raw, meta json.RawMessage) (Node[T], error) {
	versioned := r.Definition().Versioned
	params := db.InsertGraphNodeParams{ID: id, Kind: r.kind, Versioned: versioned}
	if versioned {
		version := int64(1)
		params.CurrentVersion = &version
	} else {
		params.Data = raw
	}
	row, err := q.InsertGraphNode(ctx, params)
	if err != nil {
		return Node[T]{}, graphError(err, "insert graph node")
	}
	if versioned {
		if _, err := q.InsertGraphNodeVersion(ctx, db.InsertGraphNodeVersionParams{
			NodeID: id, Version: 1, Data: raw, Metadata: meta,
		}); err != nil {
			return Node[T]{}, fmt.Errorf("insert graph node version: %w", err)
		}
	}
	return r.fromRow(ctx, q, row)
}

func (r *NodeRepository[T]) appendVersion(ctx context.Context, q *db.Queries, tx pgx.Tx, row db.GraphNode, raw, meta json.RawMessage, out *Node[T]) error {
	maxVersion, err := q.GetGraphNodeMaxVersion(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("read latest graph node version: %w", err)
	}
	version := maxVersion + 1
	if _, err := q.InsertGraphNodeVersion(ctx, db.InsertGraphNodeVersionParams{
		NodeID: row.ID, Version: version, Data: raw, Metadata: meta,
	}); err != nil {
		return fmt.Errorf("append graph node version: %w", err)
	}
	updated, err := q.SetGraphNodeVersion(ctx, db.SetGraphNodeVersionParams{ID: row.ID, Kind: row.Kind, CurrentVersion: &version})
	if err != nil {
		return fmt.Errorf("promote graph node version: %w", err)
	}
	*out, err = r.fromRow(ctx, q, updated)
	if err != nil {
		return err
	}
	return r.graph.queueNode(ctx, tx, EnrichmentArgs{Entity: EntityNode, Type: r.kind, ID: row.ID, Revision: out.Revision})
}

// Delete removes a node. Postgres rejects deletion while edges reference it.
func (r *NodeRepository[T]) Delete(ctx context.Context, id uuid.UUID) error {
	count, err := r.graph.store.DeleteGraphNode(ctx, db.DeleteGraphNodeParams{ID: id, Kind: r.kind})
	if err != nil {
		return graphError(err, "delete graph node")
	}
	if count == 0 {
		return fmt.Errorf("graph node %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

func (r *NodeRepository[T]) fromRow(ctx context.Context, q *db.Queries, row db.GraphNode) (Node[T], error) {
	raw := row.Data
	if row.Versioned {
		version, err := q.GetGraphNodeVersion(ctx, db.GetGraphNodeVersionParams{NodeID: row.ID, Version: *row.CurrentVersion})
		if err != nil {
			return Node[T]{}, graphError(err, "current graph node version")
		}
		raw = version.Data
	}
	data, err := decode[T](raw)
	if err != nil {
		return Node[T]{}, err
	}
	return Node[T]{
		Ref: Ref{ID: row.ID, Kind: row.Kind}, Data: data, Revision: row.Revision,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

// Current returns current data together with the current version number.
func (r *VersionedNodeRepository[T]) Current(ctx context.Context, id uuid.UUID) (VersionedNode[T], error) {
	row, err := r.graph.store.GetGraphNode(ctx, db.GetGraphNodeParams{ID: id, Kind: r.kind})
	if err != nil {
		return VersionedNode[T]{}, graphError(err, "graph node")
	}
	return r.currentFromRow(ctx, r.graph.store.Queries, row)
}

func (r *VersionedNodeRepository[T]) currentFromRow(ctx context.Context, q *db.Queries, row db.GraphNode) (VersionedNode[T], error) {
	node, err := r.fromRow(ctx, q, row)
	if err != nil {
		return VersionedNode[T]{}, err
	}
	return VersionedNode[T]{Node: node, CurrentVersion: *row.CurrentVersion}, nil
}

// Version returns one immutable historical version.
func (r *VersionedNodeRepository[T]) Version(ctx context.Context, id uuid.UUID, version int64) (NodeVersion[T], error) {
	if _, err := r.graph.store.GetGraphNode(ctx, db.GetGraphNodeParams{ID: id, Kind: r.kind}); err != nil {
		return NodeVersion[T]{}, graphError(err, "graph node")
	}
	row, err := r.graph.store.GetGraphNodeVersion(ctx, db.GetGraphNodeVersionParams{NodeID: id, Version: version})
	if err != nil {
		return NodeVersion[T]{}, graphError(err, "graph node version")
	}
	return r.versionFromRow(row)
}

// Versions returns immutable versions newest first. Before is exclusive, so
// passing NextBefore retrieves the following page.
func (r *VersionedNodeRepository[T]) Versions(ctx context.Context, id uuid.UUID, opts HistoryOptions) (VersionHistory[T], error) {
	if _, err := r.graph.store.GetGraphNode(ctx, db.GetGraphNodeParams{ID: id, Kind: r.kind}); err != nil {
		return VersionHistory[T]{}, graphError(err, "graph node")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultHistoryLimit
	}
	limit = min(limit, maxHistoryLimit)
	rows, err := r.graph.store.ListGraphNodeVersions(ctx, db.ListGraphNodeVersionsParams{
		NodeID: id, Column2: opts.Before, Limit: int32(limit + 1),
	})
	if err != nil {
		return VersionHistory[T]{}, fmt.Errorf("list graph node versions: %w", err)
	}
	out := VersionHistory[T]{Versions: make([]NodeVersion[T], 0, min(limit, len(rows)))}
	for i, row := range rows {
		if i == limit {
			out.NextBefore = rows[i-1].Version
			break
		}
		version, err := r.versionFromRow(row)
		if err != nil {
			return VersionHistory[T]{}, err
		}
		out.Versions = append(out.Versions, version)
	}
	return out, nil
}

func (r *VersionedNodeRepository[T]) versionFromRow(row db.GraphNodeVersion) (NodeVersion[T], error) {
	data, err := decode[T](row.Data)
	if err != nil {
		return NodeVersion[T]{}, err
	}
	return NodeVersion[T]{NodeID: row.NodeID, Version: row.Version, Data: data, Metadata: row.Metadata, CreatedAt: row.CreatedAt}, nil
}

// Rollback repoints the node to an existing immutable version. It increments
// the current revision and queues enrichment without adding a history row.
func (r *VersionedNodeRepository[T]) Rollback(ctx context.Context, id uuid.UUID, version int64, opts WriteOptions) (VersionedNode[T], error) {
	var out VersionedNode[T]
	err := r.graph.store.InTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		row, err := q.GetGraphNodeForUpdate(ctx, db.GetGraphNodeForUpdateParams{ID: id, Kind: r.kind})
		if err != nil {
			return graphError(err, "graph node")
		}
		if err := checkRevision(row.Revision, opts); err != nil {
			return err
		}
		if _, err := q.GetGraphNodeVersion(ctx, db.GetGraphNodeVersionParams{NodeID: id, Version: version}); err != nil {
			return graphError(err, "graph node version")
		}
		if *row.CurrentVersion == version {
			out, err = r.currentFromRow(ctx, q, row)
			return err
		}
		updated, err := q.SetGraphNodeVersion(ctx, db.SetGraphNodeVersionParams{ID: id, Kind: r.kind, CurrentVersion: &version})
		if err != nil {
			return fmt.Errorf("roll back graph node: %w", err)
		}
		out, err = r.currentFromRow(ctx, q, updated)
		if err != nil {
			return err
		}
		return r.graph.queueNode(ctx, tx, EnrichmentArgs{Entity: EntityNode, Type: r.kind, ID: id, Revision: updated.Revision})
	})
	return out, err
}

func currentRaw[T any](node Node[T]) json.RawMessage {
	raw, _ := json.Marshal(node.Data)
	return raw
}

func knowledgeNode[T TextData](node Node[T]) (domain.KnowledgeNode, error) {
	raw, err := json.Marshal(node.Data)
	if err != nil {
		return domain.KnowledgeNode{}, fmt.Errorf("encode graph node: %w", err)
	}
	title, description := node.Data.NodeText()
	return domain.KnowledgeNode{
		ID: node.ID, Kind: node.Kind, Revision: node.Revision,
		Title: title, Description: description, Data: raw,
		CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt,
	}, nil
}

// UpsertNode writes raw data through the typed repository registered for kind.
func (g *Graph) UpsertNode(ctx context.Context, kind string, id uuid.UUID, raw json.RawMessage, opts NodeWriteOptions) (domain.KnowledgeNode, error) {
	g.mu.RLock()
	registered, ok := g.nodes[kind]
	g.mu.RUnlock()
	if !ok {
		return domain.KnowledgeNode{}, fmt.Errorf("graph node kind %q: %w", kind, domain.ErrNotFound)
	}
	return registered.upsert(ctx, id, raw, opts)
}
