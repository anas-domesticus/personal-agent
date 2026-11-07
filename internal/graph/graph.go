// Package graph stores application-defined nodes and edges in Postgres.
//
// Registration associates Go types with persisted JSON Schemas and permitted
// endpoint combinations. Generic repositories provide typed writes and reads,
// while one shared schema enforces graph identity and relationship invariants.
// Versioned nodes keep immutable snapshots and a movable current pointer.
package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/store"
)

// JobInserter queues River jobs inside the graph write transaction. An
// insert-only River client satisfies it.
type JobInserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// Graph owns the registered types and their shared Postgres persistence.
// Types must be registered before RegisterRiver freezes the registry.
type Graph struct {
	store *store.Store
	jobs  JobInserter

	mu     sync.RWMutex
	nodes  map[string]registeredNode
	edges  map[string]registeredEdge
	frozen bool
}

type registeredNode struct {
	definition NodeType
	enrich     func(context.Context, Node[json.RawMessage]) error
	upsert     func(context.Context, uuid.UUID, json.RawMessage, NodeWriteOptions) (domain.KnowledgeNode, error)
}

type registeredEdge struct {
	definition EdgeType
	enrich     func(context.Context, Edge[json.RawMessage]) error
}

// New constructs an empty graph registry. Registration persists definitions
// in the same database as the nodes, edges and River jobs.
func New(store *store.Store, jobs JobInserter) *Graph {
	return &Graph{
		store: store,
		jobs:  jobs,
		nodes: map[string]registeredNode{},
		edges: map[string]registeredEdge{},
	}
}

// NodeTypes returns registered node definitions ordered by kind.
func (g *Graph) NodeTypes() []NodeType {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]NodeType, 0, len(g.nodes))
	for _, registered := range g.nodes {
		out = append(out, registered.definition)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// EdgeTypes returns registered edge definitions ordered by kind.
func (g *Graph) EdgeTypes() []EdgeType {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]EdgeType, 0, len(g.edges))
	for _, registered := range g.edges {
		out = append(out, registered.definition)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

func decode[T any](raw json.RawMessage) (T, error) {
	return decodeUsing[T](json.NewDecoder(bytes.NewReader(raw)))
}

func decodeStrict[T any](raw json.RawMessage) (T, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decodeUsing[T](decoder)
}

func decodeUsing[T any](decoder *json.Decoder) (T, error) {
	var out T
	if err := decoder.Decode(&out); err != nil {
		return out, fmt.Errorf("decode graph data: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return out, fmt.Errorf("decode graph data: %w", err)
	}
	return out, nil
}

func equalJSON(left, right json.RawMessage) bool {
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

func graphError(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, domain.ErrNotFound)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%s: %w", what, domain.ErrExists)
		case "23503", "23514":
			return fmt.Errorf("%s: %w", what, domain.ErrConflict)
		}
	}
	return err
}

func checkRevision(revision int64, opts WriteOptions) error {
	if opts.ExpectedRevision != nil && revision != *opts.ExpectedRevision {
		return fmt.Errorf("revision %d, expected %d: %w", revision, *opts.ExpectedRevision, domain.ErrConflict)
	}
	return nil
}

func metadata(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil, errors.New("version metadata must be a JSON object")
	}
	return raw, nil
}
