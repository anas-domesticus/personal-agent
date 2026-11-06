package admin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/runs"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

const maxWait = 5 * time.Minute

func runTools(d Deps) ([]tool.InvokableTool, error) {
	c := d.Catalog
	return kit.Build(
		func() (tool.InvokableTool, error) {
			return kit.New("run_workflow",
				"Start a run of a workflow's current version. Optionally wait for it to finish and return the result.",
				func(ctx context.Context, in runWorkflowIn) (runDetail, error) {
					triggeredBy := "agent"
					if name := kit.Agent(ctx); name != "" {
						triggeredBy = "agent:" + name
					}
					id, err := c.StartRun(ctx, in.Name, marshalOrNil(in.Input), triggeredBy)
					if err != nil {
						return runDetail{}, err
					}
					if in.WaitSeconds > 0 {
						wait := min(time.Duration(in.WaitSeconds)*time.Second, maxWait)
						if err := waitForRun(ctx, d, id, wait); err != nil {
							return runDetail{}, err
						}
					}
					return getRun(ctx, d, id, false)
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("get_run", "Get a run's status, input, output and each step's result.",
				func(ctx context.Context, in getRunIn) (runDetail, error) {
					return getRun(ctx, d, in.ID, in.IncludeTranscripts)
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("list_runs", "List recent runs, newest first.",
				func(ctx context.Context, in listRunsIn) ([]runSummary, error) {
					rows, err := c.ListRuns(ctx, in.Workflow, kit.Limit(in.Limit, 10))
					if err != nil {
						return nil, err
					}
					out := make([]runSummary, len(rows))
					for i, r := range rows {
						out[i] = runSummary{
							ID: r.ID, Workflow: r.WorkflowName, WorkflowVersion: r.WorkflowVersion, Status: r.Status,
							Error: r.Error, TriggeredBy: r.TriggeredBy, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt,
						}
					}
					return out, nil
				})
		},
	)
}

func waitForRun(ctx context.Context, d Deps, id uuid.UUID, wait time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		r, err := d.Catalog.GetRun(ctx, id)
		if err != nil {
			return err
		}
		if runs.Terminal(r.Run.Status) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil // still running; the caller sees its current status
		case <-tick.C:
		}
	}
}

func getRun(ctx context.Context, d Deps, id uuid.UUID, transcripts bool) (runDetail, error) {
	r, err := d.Catalog.GetRun(ctx, id)
	if err != nil {
		return runDetail{}, err
	}
	out := runDetail{
		runSummary: runSummary{
			ID: r.Run.ID, Workflow: r.Run.WorkflowName, WorkflowVersion: r.Run.WorkflowVersion, Status: r.Run.Status,
			Error: r.Run.Error, TriggeredBy: r.Run.TriggeredBy, CreatedAt: r.Run.CreatedAt, FinishedAt: r.Run.FinishedAt,
		},
		Input:  r.Run.Input,
		Output: r.Run.Output,
		Steps:  make([]stepOut, len(r.Steps)),
	}
	for i, s := range r.Steps {
		out.Steps[i] = stepOut{
			Node: s.NodeID, Agent: s.AgentName, AgentVersion: s.AgentVersion, Status: s.Status,
			Input: s.Input, Output: s.Output, Error: s.Error,
			PromptTokens: s.PromptTokens, CompletionTokens: s.CompletionTokens,
		}
		if transcripts {
			out.Steps[i].Transcript = s.Messages
		}
	}
	return out, nil
}

type runWorkflowIn struct {
	Name        string         `json:"name" jsonschema_description:"Workflow name."`
	Input       map[string]any `json:"input,omitempty" jsonschema_description:"Workflow input object, given to every node under \"input\"."`
	WaitSeconds int            `json:"wait_seconds,omitempty" jsonschema_description:"Wait up to this many seconds (max 300) for the run to finish. 0 returns immediately."`
}

type getRunIn struct {
	ID                 uuid.UUID `json:"id"`
	IncludeTranscripts bool      `json:"include_transcripts,omitempty" jsonschema_description:"Include each step's full message transcript, for debugging. Can be long."`
}

type listRunsIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema_description:"Only runs of this workflow."`
	Limit    int32  `json:"limit,omitempty" jsonschema_description:"Maximum results, default 10."`
}

type runSummary struct {
	ID              uuid.UUID  `json:"id"`
	Workflow        string     `json:"workflow"`
	WorkflowVersion int32      `json:"workflow_version"`
	Status          string     `json:"status"`
	Error           *string    `json:"error,omitempty"`
	TriggeredBy     string     `json:"triggered_by"`
	CreatedAt       time.Time  `json:"created_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

type runDetail struct {
	runSummary
	Input  json.RawMessage `json:"input"`
	Output json.RawMessage `json:"output,omitempty"`
	Steps  []stepOut       `json:"steps"`
}

type stepOut struct {
	Node             string          `json:"node"`
	Agent            *string         `json:"agent"`
	AgentVersion     *int32          `json:"agent_version"`
	Status           string          `json:"status"`
	Input            json.RawMessage `json:"input"`
	Output           json.RawMessage `json:"output,omitempty"`
	Error            *string         `json:"error,omitempty"`
	PromptTokens     int32           `json:"prompt_tokens"`
	CompletionTokens int32           `json:"completion_tokens"`
	Transcript       json.RawMessage `json:"transcript,omitempty"`
}
