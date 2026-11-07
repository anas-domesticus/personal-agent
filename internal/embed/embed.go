// Package embed maintains workflow and current graph-node vectors with River.
//
// Rows are saved without embeddings. An "embed" job computes one workflow
// vector or a graph node's separate title and description vectors. A periodic
// "embed_sweep" queues rows that are missing vectors, used another model, or
// changed revision, so writes through any path are picked up.
//
// Conditional writes prevent stale vectors replacing newer content. A job that
// loses that race snoozes, while the sweep covers edits that land around job
// completion within SweepInterval.
package embed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

// Queue is the River queue embedding jobs run on. Only processes with an
// embedder configured should work it.
const Queue = "embeddings"

// Dimensions is the vector size the schema stores.
const Dimensions = 1536

// SweepInterval is how often the sweep looks for rows to embed.
const SweepInterval = 5 * time.Minute

// maxChars keeps input well under embedding models' token limits (about
// 8k tokens). Longer text is embedded from its start.
const maxChars = 24000

const sweepBatch = 500

type Target string

const (
	TargetWorkflow  Target = "workflow"
	TargetGraphNode Target = "graph_node"
)

// Args embeds one row.
type Args struct {
	Target   Target    `json:"target"`
	ID       uuid.UUID `json:"id"`
	NodeKind string    `json:"node_kind,omitempty"`
	Revision int64     `json:"revision,omitempty"`
}

func (Args) Kind() string { return "embed" }

// A new job is a duplicate only of one that hasn't run yet or is running.
// Completed jobs don't count, so an edit after an embed queues a fresh job.
// A running job that embedded stale text snoozes and runs again.
func (Args) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue: Queue,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRunning,
				rivertype.JobStateScheduled,
			},
		},
	}
}

// WorkflowText is the text a workflow's embedding is computed from.
func WorkflowText(name, description string) string {
	return name + "\n\n" + description
}

var (
	// ErrDimensions means the embedding model returns vectors of the wrong size.
	ErrDimensions        = errors.New("embedding dimensions don't match")
	errStaleNodeRevision = errors.New("graph node revision is stale")
)

// Store is what the workers read and write.
type Store interface {
	GetWorkflowText(ctx context.Context, id uuid.UUID) (db.GetWorkflowTextRow, error)
	SetWorkflowEmbedding(ctx context.Context, arg db.SetWorkflowEmbeddingParams) (int64, error)
	ListWorkflowsMissingEmbedding(ctx context.Context, arg db.ListWorkflowsMissingEmbeddingParams) ([]uuid.UUID, error)
	GetGraphNodeEmbeddingText(ctx context.Context, arg db.GetGraphNodeEmbeddingTextParams) (db.GetGraphNodeEmbeddingTextRow, error)
	SetGraphNodeEmbeddings(ctx context.Context, arg db.SetGraphNodeEmbeddingsParams) (int64, error)
	ListGraphNodesMissingEmbeddings(ctx context.Context, arg db.ListGraphNodesMissingEmbeddingsParams) ([]db.ListGraphNodesMissingEmbeddingsRow, error)
}

// Text embeds one text as a vector of the stored size.
// Text embeds one text as a vector of the stored size.
func Text(ctx context.Context, e embedding.Embedder, text string) (pgvector.Vector, error) {
	vectors, err := Texts(ctx, e, []string{text})
	if err != nil {
		return pgvector.Vector{}, err
	}
	return vectors[0], nil
}

// Texts embeds a batch, preserving input order and validating every vector.
func Texts(ctx context.Context, e embedding.Embedder, texts []string) ([]pgvector.Vector, error) {
	input := make([]string, len(texts))
	for i, text := range texts {
		if utf8.RuneCountInString(text) > maxChars {
			text = string([]rune(text)[:maxChars])
		}
		input[i] = text
	}
	out, err := e.EmbedStrings(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	if len(out) != len(input) {
		return nil, fmt.Errorf("embed: got %d vectors, want %d", len(out), len(input))
	}
	vectors := make([]pgvector.Vector, len(out))
	for i, vector := range out {
		if len(vector) != Dimensions {
			return nil, fmt.Errorf("%w: model returned %d, the schema stores %d", ErrDimensions, len(vector), Dimensions)
		}
		values := make([]float32, len(vector))
		for j, value := range vector {
			values[j] = float32(value)
		}
		vectors[i] = pgvector.NewVector(values)
	}
	return vectors, nil
}

// Worker computes and stores one row's embedding.
type Worker struct {
	river.WorkerDefaults[Args]
	Store    Store
	Embedder embedding.Embedder
	Model    string
}

func (w *Worker) Work(ctx context.Context, job *river.Job[Args]) error {
	var written int64
	var err error
	switch job.Args.Target {
	case TargetWorkflow:
		written, err = w.workflow(ctx, job.Args.ID)
	case TargetGraphNode:
		written, err = w.graphNode(ctx, job.Args)
	default:
		return river.JobCancel(fmt.Errorf("unknown target %q", job.Args.Target))
	}
	if errors.Is(err, errStaleNodeRevision) {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // deleted since the job was queued
	}
	if err != nil {
		return err
	}
	if written == 0 {
		// The text changed while it was being embedded. A new job for the
		// edit was deduplicated against this one, so run again.
		slog.Debug("text changed while embedding; embedding again", "target", job.Args.Target, "id", job.Args.ID)
		return river.JobSnooze(time.Second)
	}
	return nil
}

func (w *Worker) workflow(ctx context.Context, id uuid.UUID) (int64, error) {
	row, err := w.Store.GetWorkflowText(ctx, id)
	if err != nil {
		return 0, err
	}
	vec, err := w.embed(ctx, WorkflowText(row.Name, row.Description))
	if err != nil {
		return 0, err
	}
	return w.Store.SetWorkflowEmbedding(ctx, db.SetWorkflowEmbeddingParams{
		ID: id, Name: row.Name, Description: row.Description,
		Embedding: &vec, EmbeddingModel: &w.Model,
	})
}

func (w *Worker) graphNode(ctx context.Context, args Args) (int64, error) {
	row, err := w.Store.GetGraphNodeEmbeddingText(ctx, db.GetGraphNodeEmbeddingTextParams{ID: args.ID, Kind: args.NodeKind})
	if err != nil {
		return 0, err
	}
	if row.Revision != args.Revision {
		return 0, errStaleNodeRevision
	}
	vectors, err := w.embedTexts(ctx, []string{row.Title, row.Description})
	if err != nil {
		return 0, err
	}
	return w.Store.SetGraphNodeEmbeddings(ctx, db.SetGraphNodeEmbeddingsParams{
		ID: args.ID, Kind: args.NodeKind, Revision: args.Revision, EmbeddingRevision: &args.Revision,
		TitleEmbedding: &vectors[0], DescriptionEmbedding: &vectors[1], EmbeddingModel: &w.Model,
	})
}

func (w *Worker) embed(ctx context.Context, text string) (pgvector.Vector, error) {
	vectors, err := w.embedTexts(ctx, []string{text})
	if err != nil {
		return pgvector.Vector{}, err
	}
	return vectors[0], nil
}

func (w *Worker) embedTexts(ctx context.Context, texts []string) ([]pgvector.Vector, error) {
	vectors, err := Texts(ctx, w.Embedder, texts)
	if errors.Is(err, ErrDimensions) {
		// Retrying won't help: the configured model is wrong.
		return nil, river.JobCancel(err)
	}
	return vectors, err
}

// SweepArgs queues embed jobs for every row that needs one.
type SweepArgs struct{}

func (SweepArgs) Kind() string { return "embed_sweep" }

func (SweepArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: Queue}
}

type SweepWorker struct {
	river.WorkerDefaults[SweepArgs]
	Store Store
	Model string
}

func (w *SweepWorker) Work(ctx context.Context, _ *river.Job[SweepArgs]) error {
	workflows, err := w.Store.ListWorkflowsMissingEmbedding(ctx, db.ListWorkflowsMissingEmbeddingParams{Model: w.Model, Lim: sweepBatch})
	if err != nil {
		return err
	}
	nodes, err := w.Store.ListGraphNodesMissingEmbeddings(ctx, db.ListGraphNodesMissingEmbeddingsParams{Model: w.Model, Lim: sweepBatch})
	if err != nil {
		return err
	}
	params := make([]river.InsertManyParams, 0, len(workflows)+len(nodes))
	for _, id := range workflows {
		params = append(params, river.InsertManyParams{Args: Args{Target: TargetWorkflow, ID: id}})
	}
	for _, node := range nodes {
		params = append(params, river.InsertManyParams{Args: Args{
			Target: TargetGraphNode, ID: node.ID, NodeKind: node.NodeKind, Revision: node.Revision,
		}})
	}
	if len(params) == 0 {
		return nil
	}

	client := river.ClientFromContext[pgx.Tx](ctx)
	if _, err := client.InsertMany(ctx, params); err != nil {
		return fmt.Errorf("queue embed jobs: %w", err)
	}
	slog.Info("queued embedding jobs", "workflows", len(workflows), "graph_nodes", len(nodes))
	return nil
}

// Register adds the embedding workers, queue and periodic sweep to a River
// client config.
func Register(cfg *river.Config, store Store, embedder embedding.Embedder, model string) {
	if cfg.Workers == nil {
		cfg.Workers = river.NewWorkers()
	}
	river.AddWorker(cfg.Workers, &Worker{Store: store, Embedder: embedder, Model: model})
	river.AddWorker(cfg.Workers, &SweepWorker{Store: store, Model: model})

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
