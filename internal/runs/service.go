package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/store"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

// JobInserter queues River jobs inside a transaction.
type JobInserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// Service is the Postgres store of runs and steps.
type Service struct {
	st   *store.Store
	jobs JobInserter
}

func NewService(st *store.Store, jobs JobInserter) *Service {
	return &Service{st: st, jobs: jobs}
}

// Start queues a run of the workflow's current version. The run row and its
// job are written in one transaction.
func (s *Service) Start(ctx context.Context, workflowName string, input json.RawMessage, triggeredBy string) (uuid.UUID, error) {
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	var obj map[string]any
	if json.Unmarshal(input, &obj) != nil || obj == nil {
		return uuid.Nil, errors.New("input must be a JSON object")
	}
	var id uuid.UUID
	err := s.st.InTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		w, err := q.GetWorkflowByName(ctx, workflowName)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("workflow %q: %w", workflowName, domain.ErrNotFound)
		}
		if err != nil {
			return err
		}
		if w.CurrentVersion == nil {
			return fmt.Errorf("workflow %q has no current version", workflowName)
		}
		r, err := q.CreateRun(ctx, db.CreateRunParams{
			WorkflowID: w.ID, WorkflowVersion: *w.CurrentVersion, Input: input, TriggeredBy: triggeredBy,
		})
		if err != nil {
			return err
		}
		id = r.ID
		if _, err := s.jobs.InsertTx(ctx, tx, Args{RunID: r.ID}, nil); err != nil {
			return fmt.Errorf("queue run: %w", err)
		}
		return nil
	})
	return id, err
}

// Get returns a run with its steps, including transcripts.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (domain.RunDetail, error) {
	r, err := s.st.GetRun(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RunDetail{}, fmt.Errorf("run %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.RunDetail{}, err
	}
	steps, err := s.st.ListRunSteps(ctx, id)
	if err != nil {
		return domain.RunDetail{}, err
	}
	out := domain.RunDetail{
		Run: domain.Run{
			ID: r.ID, WorkflowID: r.WorkflowID, Workflow: r.WorkflowName, WorkflowVersion: r.WorkflowVersion,
			Status: domain.RunStatus(r.Status), Input: r.Input, Output: r.Output, Error: deref(r.Error),
			TriggeredBy: r.TriggeredBy, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		},
		Steps: make([]domain.Step, len(steps)),
	}
	for i, st := range steps {
		out.Steps[i] = domain.Step{
			ID: st.ID, Node: st.NodeID, Agent: deref(st.AgentName), AgentVersion: st.AgentVersion,
			Status: domain.RunStatus(st.Status), Input: st.Input, Output: st.Output, Error: deref(st.Error),
			Transcript: st.Messages, PromptTokens: st.PromptTokens, CompletionTokens: st.CompletionTokens,
			StartedAt: st.StartedAt, FinishedAt: st.FinishedAt,
		}
	}
	return out, nil
}

// List lists recent runs, newest first, optionally for one workflow.
func (s *Service) List(ctx context.Context, workflowName string, limit int32) ([]domain.Run, error) {
	rows, err := s.st.ListRuns(ctx, db.ListRunsParams{WorkflowName: workflowName, Lim: limit})
	if err != nil {
		return nil, err
	}
	out := make([]domain.Run, len(rows))
	for i, r := range rows {
		out[i] = domain.Run{
			ID: r.ID, Workflow: r.WorkflowName, WorkflowVersion: r.WorkflowVersion, Status: domain.RunStatus(r.Status),
			Error: deref(r.Error), TriggeredBy: r.TriggeredBy, CreatedAt: r.CreatedAt,
			StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		}
	}
	return out, nil
}

// --- Recording, used by the worker -------------------------------------------

// Begin moves a queued run to running and returns it with its graph. It
// returns false if the run had already started, so a job can never run a
// run twice.
func (s *Service) Begin(ctx context.Context, id uuid.UUID) (domain.Run, workflow.Graph, bool, error) {
	n, err := s.st.StartRun(ctx, id)
	if err != nil || n == 0 {
		return domain.Run{}, workflow.Graph{}, false, err
	}
	r, err := s.st.GetRun(ctx, id)
	if err != nil {
		return domain.Run{}, workflow.Graph{}, true, err
	}
	wv, err := s.st.GetWorkflowVersion(ctx, db.GetWorkflowVersionParams{WorkflowID: r.WorkflowID, Version: r.WorkflowVersion})
	if err != nil {
		return domain.Run{}, workflow.Graph{}, true, err
	}
	g, err := workflow.Parse(wv.Graph)
	if err != nil {
		return domain.Run{}, workflow.Graph{}, true, fmt.Errorf("stored graph is invalid: %w", err)
	}
	return domain.Run{
		ID: r.ID, WorkflowID: r.WorkflowID, Workflow: r.WorkflowName, WorkflowVersion: r.WorkflowVersion,
		Status: domain.RunRunning, Input: r.Input, TriggeredBy: r.TriggeredBy, CreatedAt: r.CreatedAt,
	}, g, true, nil
}

// Finish records a run's outcome. A nil runErr means it succeeded. Steps
// still marked running are failed with it. Finishing an already finished
// run is a no-op.
func (s *Service) Finish(ctx context.Context, id uuid.UUID, output json.RawMessage, runErr error) error {
	return s.st.InTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		p := db.FinishRunParams{ID: id, Status: string(domain.RunSucceeded), Output: output}
		if runErr != nil {
			msg := runErr.Error()
			p.Status, p.Output, p.Error = string(domain.RunFailed), nil, &msg
			if err := q.FailUnfinishedSteps(ctx, db.FailUnfinishedStepsParams{RunID: id, Error: msg}); err != nil {
				return err
			}
		}
		_, err := q.FinishRun(ctx, p)
		return err
	})
}

// StepAgent identifies the agent version a step ran.
type StepAgent struct {
	ID      uuid.UUID
	Version int32
}

// StartStep records that a node started. agent is nil if the agent couldn't
// be built.
func (s *Service) StartStep(ctx context.Context, runID uuid.UUID, node string, agent *StepAgent, input json.RawMessage) (int64, error) {
	p := db.CreateRunStepParams{RunID: runID, NodeID: node, Input: input}
	if agent != nil {
		p.AgentID, p.AgentVersion = &agent.ID, &agent.Version
	}
	return s.st.CreateRunStep(ctx, p)
}

// StepResult is how a step ended.
type StepResult struct {
	Output           json.RawMessage
	Transcript       json.RawMessage
	PromptTokens     int
	CompletionTokens int
	Err              error
}

func (s *Service) FinishStep(ctx context.Context, stepID int64, r StepResult) error {
	transcript := r.Transcript
	if len(transcript) == 0 {
		transcript = json.RawMessage(`[]`)
	}
	p := db.FinishRunStepParams{
		ID: stepID, Status: string(domain.RunSucceeded), Output: r.Output, Messages: transcript,
		PromptTokens: int32(r.PromptTokens), CompletionTokens: int32(r.CompletionTokens),
	}
	if r.Err != nil {
		msg := r.Err.Error()
		p.Status, p.Output, p.Error = string(domain.RunFailed), nil, &msg
	}
	return s.st.FinishRunStep(ctx, p)
}

// staleGrace is how long past Timeout a running run is given before it is
// presumed dead.
const staleGrace = 5 * time.Minute

// Reap fails runs that can no longer finish: those whose job is gone or
// finished without recording an outcome (a crash, or a failed write), and
// those running far longer than Timeout. It returns how many it failed.
func (s *Service) Reap(ctx context.Context) (int, error) {
	var ids []uuid.UUID
	err := s.st.InTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			update runs r
			set status = 'failed', finished_at = now(),
			    error = 'run was interrupted (the worker stopped or crashed) and can no longer finish'
			where r.status in ('queued', 'running')
			  and r.created_at < now() - interval '1 minute'
			  and (
			    (r.status = 'running' and r.started_at < now() - make_interval(secs => $1))
			    or not exists (
			      select 1 from river_job j
			      where j.kind = $2 and j.args->>'run_id' = r.id::text
			        and j.state in ('available', 'pending', 'running', 'retryable', 'scheduled')
			    )
			  )
			returning r.id`,
			(Timeout + staleGrace).Seconds(), Args{}.Kind())
		if err != nil {
			return err
		}
		if ids, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID]); err != nil {
			return err
		}
		for _, id := range ids {
			if err := q.FailUnfinishedSteps(ctx, db.FailUnfinishedStepsParams{RunID: id, Error: "run was interrupted"}); err != nil {
				return err
			}
		}
		return nil
	})
	if len(ids) > 0 {
		slog.Warn("failed interrupted runs", "runs", ids)
	}
	return len(ids), err
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
