package mcpserver

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type runWorkflowInput struct {
	Name        string         `json:"name" jsonschema:"Workflow whose current version will run."`
	Input       map[string]any `json:"input,omitempty" jsonschema:"JSON object supplied to every node under input."`
	WaitSeconds int            `json:"wait_seconds,omitempty" jsonschema:"Wait up to this many seconds, capped at 300. Zero returns immediately."`
}

type getRunInput struct {
	ID                 string `json:"id" jsonschema:"Run id returned by run_workflow."`
	IncludeTranscripts bool   `json:"include_transcripts,omitempty" jsonschema:"Include full model transcripts for debugging."`
}

type listRunsInput struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"Only return runs of this workflow."`
	Limit    int32  `json:"limit,omitempty" jsonschema:"Maximum results, from 1 to 100. Defaults to 10."`
}

type runsOutput struct {
	Runs []map[string]any `json:"runs"`
}

func registerRunTools(s *mcp.Server, runs Runs) {
	add(s, "run_workflow", "Start a durable background run of a workflow's current version.", writing, func(ctx context.Context, in runWorkflowInput) (map[string]any, error) {
		id, err := runs.Start(ctx, in.Name, rawObject(in.Input), "mcp")
		if err != nil {
			return nil, err
		}
		if in.WaitSeconds > 0 {
			seconds := min(in.WaitSeconds, 300)
			waitForRun(ctx, runs, id, time.Duration(seconds)*time.Second)
		}
		return runDetail(ctx, runs, id, false)
	})
	add(s, "get_run", "Get a run's status, input, output and each node's durable result.", readOnly, func(ctx context.Context, in getRunInput) (map[string]any, error) {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, fmt.Errorf("parse run id: %w", err)
		}
		return runDetail(ctx, runs, id, in.IncludeTranscripts)
	})
	add(s, "list_runs", "List recent workflow runs, newest first.", readOnly, func(ctx context.Context, in listRunsInput) (runsOutput, error) {
		out, err := runs.List(ctx, in.Workflow, boundedLimit(in.Limit))
		if err != nil {
			return runsOutput{}, err
		}
		items, err := objects(out)
		return runsOutput{Runs: items}, err
	})
}

func runDetail(ctx context.Context, runs Runs, id uuid.UUID, transcripts bool) (map[string]any, error) {
	out, err := runs.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !transcripts {
		out.Steps = slices.Clone(out.Steps)
		for i := range out.Steps {
			out.Steps[i].Transcript = nil
		}
	}
	return object(out)
}

func waitForRun(ctx context.Context, runs Runs, id uuid.UUID, wait time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		out, err := runs.Get(ctx, id)
		if err != nil || out.Status.Terminal() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
