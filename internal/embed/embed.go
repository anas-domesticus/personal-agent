// Package embed fills in embeddings for workflows and memories using River jobs.
//
// Rows are saved without an embedding. An "embed" job computes one row's
// vector. A periodic "embed_sweep" job finds rows with no embedding, or one
// made by a different model, and queues "embed" jobs for them, so rows written
// by any path are picked up.
package embed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
	"github.com/riverqueue/river"

	"github.com/anas-domesticus/personal-agent/internal/llm"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

// Queue is the River queue embedding jobs run on. Only processes with an
// embedder configured should work it.
const Queue = "embeddings"

// SweepInterval is how often the sweep looks for rows to embed.
const SweepInterval = 5 * time.Minute

const sweepBatch = 500

type Target string

const (
	TargetWorkflow Target = "workflow"
	TargetMemory   Target = "memory"
)

// Args embeds one row.
type Args struct {
	Target Target    `json:"target"`
	ID     uuid.UUID `json:"id"`
}

func (Args) Kind() string { return "embed" }

func (Args) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:      Queue,
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	}
}

// WorkflowText is the text a workflow's embedding is computed from.
func WorkflowText(name, description string) string {
	return name + "\n\n" + description
}

// Worker computes and stores one row's embedding.
type Worker struct {
	river.WorkerDefaults[Args]
	Queries  *db.Queries
	Embedder embedding.Embedder
	Model    string
}

func (w *Worker) Work(ctx context.Context, job *river.Job[Args]) error {
	switch job.Args.Target {
	case TargetWorkflow:
		return w.workflow(ctx, job.Args.ID)
	case TargetMemory:
		return w.memory(ctx, job.Args.ID)
	default:
		return river.JobCancel(fmt.Errorf("unknown target %q", job.Args.Target))
	}
}

func (w *Worker) workflow(ctx context.Context, id uuid.UUID) error {
	row, err := w.Queries.GetWorkflowText(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // deleted since the job was queued
	}
	if err != nil {
		return err
	}
	vec, err := w.embed(ctx, WorkflowText(row.Name, row.Description))
	if err != nil {
		return err
	}
	n, err := w.Queries.SetWorkflowEmbedding(ctx, db.SetWorkflowEmbeddingParams{
		ID: id, Name: row.Name, Description: row.Description,
		Embedding: &vec, EmbeddingModel: &w.Model,
	})
	if err == nil && n == 0 {
		slog.Debug("workflow changed while embedding; leaving it for the next job", "id", id)
	}
	return err
}

func (w *Worker) memory(ctx context.Context, id uuid.UUID) error {
	row, err := w.Queries.GetMemory(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	vec, err := w.embed(ctx, row.Content)
	if err != nil {
		return err
	}
	n, err := w.Queries.SetMemoryEmbedding(ctx, db.SetMemoryEmbeddingParams{
		ID: id, Content: row.Content,
		Embedding: &vec, EmbeddingModel: &w.Model,
	})
	if err == nil && n == 0 {
		slog.Debug("memory changed while embedding; leaving it for the next job", "id", id)
	}
	return err
}

func (w *Worker) embed(ctx context.Context, text string) (pgvector.Vector, error) {
	out, err := w.Embedder.EmbedStrings(ctx, []string{text})
	if err != nil {
		return pgvector.Vector{}, fmt.Errorf("embed: %w", err)
	}
	if len(out) != 1 {
		return pgvector.Vector{}, fmt.Errorf("embed: got %d vectors, want 1", len(out))
	}
	return pgvector.NewVector(llm.Float32s(out[0])), nil
}

// SweepArgs queues embed jobs for every row that needs one.
type SweepArgs struct{}

func (SweepArgs) Kind() string { return "embed_sweep" }

func (SweepArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: Queue}
}

type SweepWorker struct {
	river.WorkerDefaults[SweepArgs]
	Queries *db.Queries
	Model   string
}

func (w *SweepWorker) Work(ctx context.Context, _ *river.Job[SweepArgs]) error {
	workflows, err := w.Queries.ListWorkflowsMissingEmbedding(ctx, db.ListWorkflowsMissingEmbeddingParams{Model: w.Model, Lim: sweepBatch})
	if err != nil {
		return err
	}
	memories, err := w.Queries.ListMemoriesMissingEmbedding(ctx, db.ListMemoriesMissingEmbeddingParams{Model: w.Model, Lim: sweepBatch})
	if err != nil {
		return err
	}

	params := make([]river.InsertManyParams, 0, len(workflows)+len(memories))
	for _, r := range workflows {
		params = append(params, river.InsertManyParams{Args: Args{Target: TargetWorkflow, ID: r.ID}})
	}
	for _, r := range memories {
		params = append(params, river.InsertManyParams{Args: Args{Target: TargetMemory, ID: r.ID}})
	}
	if len(params) == 0 {
		return nil
	}

	client := river.ClientFromContext[pgx.Tx](ctx)
	if _, err := client.InsertMany(ctx, params); err != nil {
		return fmt.Errorf("queue embed jobs: %w", err)
	}
	slog.Info("queued embedding jobs", "workflows", len(workflows), "memories", len(memories))
	return nil
}

// Register adds the embedding workers, queue and periodic sweep to a River
// client config.
func Register(cfg *river.Config, queries *db.Queries, embedder embedding.Embedder, model string) {
	if cfg.Workers == nil {
		cfg.Workers = river.NewWorkers()
	}
	river.AddWorker(cfg.Workers, &Worker{Queries: queries, Embedder: embedder, Model: model})
	river.AddWorker(cfg.Workers, &SweepWorker{Queries: queries, Model: model})

	if cfg.Queues == nil {
		cfg.Queues = map[string]river.QueueConfig{}
	}
	cfg.Queues[Queue] = river.QueueConfig{MaxWorkers: 4}

	cfg.PeriodicJobs = append(cfg.PeriodicJobs, river.NewPeriodicJob(
		river.PeriodicInterval(SweepInterval),
		func() (river.JobArgs, *river.InsertOpts) { return SweepArgs{}, nil },
		&river.PeriodicJobOpts{RunOnStart: true},
	))
}
