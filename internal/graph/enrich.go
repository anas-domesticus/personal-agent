package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

// Queue is the River queue for graph enrichment.
const Queue = "graph_enrichment"

// Entity identifies which graph aggregate an enrichment job handles.
type Entity string

const (
	// EntityNode enriches a current node revision.
	EntityNode Entity = "node"
	// EntityEdge enriches a current edge revision.
	EntityEdge Entity = "edge"
)

// EnrichmentArgs identifies one committed entity revision.
type EnrichmentArgs struct {
	Entity   Entity    `json:"entity"`
	Type     string    `json:"type"`
	ID       uuid.UUID `json:"id"`
	Revision int64     `json:"revision"`
}

// Kind implements river.JobArgs.
func (EnrichmentArgs) Kind() string { return "graph_enrich" }

// InsertOpts coalesces outstanding work for one entity revision. Completed
// jobs do not block a later write that produces the same revision elsewhere.
func (EnrichmentArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue: Queue,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRetryable,
				rivertype.JobStateRunning,
				rivertype.JobStateScheduled,
			},
		},
	}
}

func (g *Graph) queue(ctx context.Context, tx pgx.Tx, args EnrichmentArgs) error {
	if g.jobs == nil {
		return errors.New("graph enrichment job inserter is nil")
	}
	if _, err := g.jobs.InsertTx(ctx, tx, args, nil); err != nil {
		return fmt.Errorf("queue graph enrichment: %w", err)
	}
	return nil
}

func (g *Graph) queueNode(ctx context.Context, tx pgx.Tx, args EnrichmentArgs) error {
	if err := g.queue(ctx, tx, args); err != nil {
		return err
	}
	embedding := embed.Args{Target: embed.TargetGraphNode, ID: args.ID, NodeKind: args.Type, Revision: args.Revision}
	if _, err := g.jobs.InsertTx(ctx, tx, embedding, nil); err != nil {
		return fmt.Errorf("queue graph node embedding: %w", err)
	}
	return nil
}

// Worker dispatches enrichment to handlers registered with the entity type.
// A job whose revision is no longer current is already superseded and is
// acknowledged without running stale handlers.
type Worker struct {
	river.WorkerDefaults[EnrichmentArgs]
	Graph *Graph
}

// Work implements river.Worker.
func (w *Worker) Work(ctx context.Context, job *river.Job[EnrichmentArgs]) error {
	switch job.Args.Entity {
	case EntityNode:
		return w.node(ctx, job.Args)
	case EntityEdge:
		return w.edge(ctx, job.Args)
	default:
		return river.JobCancel(fmt.Errorf("unknown graph entity %q", job.Args.Entity))
	}
}

func (w *Worker) node(ctx context.Context, args EnrichmentArgs) error {
	w.Graph.mu.RLock()
	registered, ok := w.Graph.nodes[args.Type]
	w.Graph.mu.RUnlock()
	if !ok {
		return river.JobCancel(fmt.Errorf("unknown graph node kind %q", args.Type))
	}
	if registered.enrich == nil {
		return nil
	}
	row, err := w.Graph.store.GetGraphNode(ctx, db.GetGraphNodeParams{ID: args.ID, Kind: args.Type})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load graph node for enrichment: %w", err)
	}
	if row.Revision != args.Revision {
		return nil
	}
	raw := row.Data
	if row.Versioned {
		version, err := w.Graph.store.GetGraphNodeVersion(ctx, db.GetGraphNodeVersionParams{NodeID: row.ID, Version: *row.CurrentVersion})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load graph node version for enrichment: %w", err)
		}
		raw = version.Data
	}
	node := Node[json.RawMessage]{
		Ref: Ref{ID: row.ID, Kind: row.Kind}, Data: raw, Revision: row.Revision,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if err := registered.enrich(ctx, node); err != nil {
		return fmt.Errorf("enrich graph node %s: %w", row.ID, err)
	}
	return nil
}

func (w *Worker) edge(ctx context.Context, args EnrichmentArgs) error {
	w.Graph.mu.RLock()
	registered, ok := w.Graph.edges[args.Type]
	w.Graph.mu.RUnlock()
	if !ok {
		return river.JobCancel(fmt.Errorf("unknown graph edge kind %q", args.Type))
	}
	if registered.enrich == nil {
		return nil
	}
	row, err := w.Graph.store.GetGraphEdge(ctx, db.GetGraphEdgeParams{ID: args.ID, Kind: args.Type})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load graph edge for enrichment: %w", err)
	}
	if row.Revision != args.Revision {
		return nil
	}
	edge := Edge[json.RawMessage]{
		ID: row.ID, Kind: row.Kind,
		Source: Ref{ID: row.SourceID, Kind: row.SourceKind},
		Target: Ref{ID: row.TargetID, Kind: row.TargetKind},
		Data:   row.Data, Revision: row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if err := registered.enrich(ctx, edge); err != nil {
		return fmt.Errorf("enrich graph edge %s: %w", row.ID, err)
	}
	return nil
}

// RegisterRiver freezes type registration and installs the shared enrichment
// worker and queue. External handlers are supplied in NodeSpec and EdgeSpec;
// they do not depend on River themselves.
func RegisterRiver(config *river.Config, graph *Graph) {
	graph.mu.Lock()
	graph.frozen = true
	graph.mu.Unlock()
	if config.Workers == nil {
		config.Workers = river.NewWorkers()
	}
	river.AddWorker(config.Workers, &Worker{Graph: graph})
	if config.Queues == nil {
		config.Queues = map[string]river.QueueConfig{}
	}
	config.Queues[Queue] = river.QueueConfig{MaxWorkers: 4}
}
