package admin

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

const maxWait = 5 * time.Minute

func runTools(r Runs) ([]tool.InvokableTool, error) {
	return kit.Build(
		func() (tool.InvokableTool, error) {
			return kit.New("run_workflow",
				"Start a run of a workflow's current version. Optionally wait for it to finish and return the result.",
				func(ctx context.Context, in runWorkflowIn) (domain.RunDetail, error) {
					triggeredBy := "agent"
					if name := domain.Caller(ctx); name != "" {
						triggeredBy = "agent:" + name
					}
					id, err := r.Start(ctx, in.Name, marshalOrNil(in.Input), triggeredBy)
					if err != nil {
						return domain.RunDetail{}, err
					}
					if in.WaitSeconds > 0 {
						wait := min(time.Duration(in.WaitSeconds)*time.Second, maxWait)
						if err := waitForRun(ctx, r, id, wait); err != nil {
							return domain.RunDetail{}, err
						}
					}
					return getRun(ctx, r, id, false)
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("get_run", "Get a run's status, input, output and each step's result.",
				func(ctx context.Context, in getRunIn) (domain.RunDetail, error) {
					return getRun(ctx, r, in.ID, in.IncludeTranscripts)
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("list_runs", "List recent runs, newest first.",
				func(ctx context.Context, in listRunsIn) ([]domain.Run, error) {
					return r.List(ctx, in.Workflow, kit.Limit(in.Limit, 10))
				})
		},
	)
}

func waitForRun(ctx context.Context, r Runs, id uuid.UUID, wait time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		d, err := r.Get(ctx, id)
		if ctx.Err() != nil {
			return nil // still running; the caller sees its current status
		}
		if err != nil {
			return err
		}
		if d.Status.Terminal() {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func getRun(ctx context.Context, r Runs, id uuid.UUID, transcripts bool) (domain.RunDetail, error) {
	d, err := r.Get(ctx, id)
	if err != nil {
		return domain.RunDetail{}, err
	}
	if !transcripts {
		for i := range d.Steps {
			d.Steps[i].Transcript = nil
		}
	}
	return d, nil
}

type runWorkflowIn struct {
	Name        string         `json:"name" jsonschema_description:"Workflow name."`
	Input       map[string]any `json:"input,omitempty" jsonschema_description:"Workflow input object, given to every node under \"input\"."`
	WaitSeconds int            `json:"wait_seconds,omitempty" jsonschema_description:"Wait up to this many seconds (max 300) for the run to finish. 0 returns immediately."`
}

type getRunIn struct {
	ID                 uuid.UUID `json:"id"`
	IncludeTranscripts bool      `json:"include_transcripts,omitempty" jsonschema_description:"Include each step's full message transcript, for debugging. Long transcripts are truncated."`
}

type listRunsIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema_description:"Only runs of this workflow."`
	Limit    int32  `json:"limit,omitempty" jsonschema_description:"Maximum results, default 10."`
}
