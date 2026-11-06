package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/anas-domesticus/personal-agent/internal/runs"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

// StartRun queues a run of the workflow's current version. The run row and its
// job are written in one transaction.
func (c *Catalog) StartRun(ctx context.Context, workflowName string, input json.RawMessage, triggeredBy string) (uuid.UUID, error) {
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	if !isJSONObject(input) {
		return uuid.Nil, errors.New("input must be a JSON object")
	}
	var id uuid.UUID
	err := c.tx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		w, err := q.GetWorkflowByName(ctx, workflowName)
		if err != nil {
			return notFound(err, fmt.Sprintf("workflow %q", workflowName))
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
		if _, err := c.jobs.InsertTx(ctx, tx, runs.Args{RunID: r.ID}, nil); err != nil {
			return fmt.Errorf("queue run: %w", err)
		}
		return nil
	})
	return id, err
}

type RunDetail struct {
	Run   db.GetRunRow
	Steps []db.ListRunStepsRow
}

func (c *Catalog) GetRun(ctx context.Context, id uuid.UUID) (RunDetail, error) {
	r, err := c.store.GetRun(ctx, id)
	if err != nil {
		return RunDetail{}, notFound(err, fmt.Sprintf("run %s", id))
	}
	steps, err := c.store.ListRunSteps(ctx, id)
	if err != nil {
		return RunDetail{}, err
	}
	return RunDetail{Run: r, Steps: steps}, nil
}

// ListRuns lists recent runs, optionally for one workflow.
func (c *Catalog) ListRuns(ctx context.Context, workflowName string, limit int32) ([]db.ListRunsRow, error) {
	return c.store.ListRuns(ctx, db.ListRunsParams{WorkflowName: workflowName, Lim: limit})
}
