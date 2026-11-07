package graph

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Ref identifies a node without loading its data.
type Ref struct {
	ID   uuid.UUID `json:"id"`
	Kind string    `json:"kind"`
}

// Node is the current state of a node.
type Node[T any] struct {
	Ref
	Data      T         `json:"data"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// VersionedNode composes the current node state with its version pointer.
type VersionedNode[T any] struct {
	Node[T]
	CurrentVersion int64 `json:"current_version"`
}

// NodeVersion is one immutable snapshot of a versioned node.
type NodeVersion[T any] struct {
	NodeID    uuid.UUID       `json:"node_id"`
	Version   int64           `json:"version"`
	Data      T               `json:"data"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
}

// Edge connects two nodes. Source and target kinds are kept on the row so
// Postgres can check both endpoint types and the permitted relationship.
type Edge[T any] struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	Source    Ref       `json:"source"`
	Target    Ref       `json:"target"`
	Data      T         `json:"data"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Endpoint permits an edge kind to connect two registered node kinds.
type Endpoint struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// NodeType is the persisted structural definition of a node kind.
type NodeType struct {
	Kind       string          `json:"kind"`
	Versioned  bool            `json:"versioned"`
	DataSchema json.RawMessage `json:"data_schema"`
}

// EdgeType is the persisted structural definition of an edge kind.
type EdgeType struct {
	Kind       string          `json:"kind"`
	DataSchema json.RawMessage `json:"data_schema"`
	Endpoints  []Endpoint      `json:"endpoints"`
}

// WriteOptions controls optimistic concurrency. A nil ExpectedRevision skips
// the check; writes to a newly created entity must also leave it nil.
type WriteOptions struct {
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

// NodeWriteOptions controls a node write. Metadata is stored on a newly
// appended version and is ignored for ordinary nodes.
type NodeWriteOptions struct {
	WriteOptions
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// HistoryOptions selects an exclusive upper version and page size. Zero values
// request the newest page with the default limit.
type HistoryOptions struct {
	Before int64 `json:"before,omitempty"`
	Limit  int   `json:"limit,omitempty"`
}

// VersionHistory is a newest-first page. NextBefore is zero when no older
// versions remain.
type VersionHistory[T any] struct {
	Versions   []NodeVersion[T] `json:"versions"`
	NextBefore int64            `json:"next_before,omitempty"`
}

// NodeEnricher processes a committed node revision. Implementations must be
// idempotent because River may execute a job more than once.
type NodeEnricher[T any] func(context.Context, Node[T]) error

// EdgeEnricher processes a committed edge revision. Implementations must be
// idempotent because River may execute a job more than once.
type EdgeEnricher[T any] func(context.Context, Edge[T]) error

// NodeSpec registers a Go type as a graph node kind.
type NodeSpec[T any] struct {
	Kind      string
	Validate  func(T) error
	Enrichers []NodeEnricher[T]
}

// EdgeSpec registers a Go type as a graph edge kind.
type EdgeSpec[T any] struct {
	Kind      string
	Endpoints []Endpoint
	Validate  func(T) error
	Enrichers []EdgeEnricher[T]
}
