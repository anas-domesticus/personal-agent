// Package runner executes workflow runs as River jobs.
//
// Nodes run one at a time in dependency order. Each node runs its agent's
// version that is current when the node starts, and the step records which
// version that was. Data between nodes follows the contract in package
// workflow.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/runs"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

// RunTimeout bounds a whole run.
const RunTimeout = 30 * time.Minute

type Worker struct {
	river.WorkerDefaults[runs.Args]
	Queries *db.Queries
	Builder *agent.Builder
}

// Register adds the run worker and queue to a River client config.
func Register(cfg *river.Config, w *Worker) {
	if cfg.Workers == nil {
		cfg.Workers = river.NewWorkers()
	}
	river.AddWorker(cfg.Workers, w)
	if cfg.Queues == nil {
		cfg.Queues = map[string]river.QueueConfig{}
	}
	cfg.Queues[runs.Queue] = river.QueueConfig{MaxWorkers: 2}
}

func (w *Worker) Timeout(*river.Job[runs.Args]) time.Duration { return RunTimeout }

func (w *Worker) Work(ctx context.Context, job *river.Job[runs.Args]) error {
	id := job.Args.RunID
	started, err := w.Queries.StartRun(ctx, id)
	if err != nil {
		return err
	}
	if started == 0 {
		return nil // already started or finished
	}

	output, runErr := w.execute(ctx, id)

	// Record the outcome even if the job's context was cancelled.
	ctx = context.WithoutCancel(ctx)
	if runErr != nil {
		msg := runErr.Error()
		slog.Warn("run failed", "run", id, "err", msg)
		return w.Queries.FinishRun(ctx, db.FinishRunParams{ID: id, Status: runs.StatusFailed, Error: &msg})
	}
	return w.Queries.FinishRun(ctx, db.FinishRunParams{ID: id, Status: runs.StatusSucceeded, Output: output})
}

func (w *Worker) execute(ctx context.Context, id uuid.UUID) (json.RawMessage, error) {
	run, err := w.Queries.GetRun(ctx, id)
	if err != nil {
		return nil, err
	}
	wv, err := w.Queries.GetWorkflowVersion(ctx, db.GetWorkflowVersionParams{WorkflowID: run.WorkflowID, Version: run.WorkflowVersion})
	if err != nil {
		return nil, err
	}
	g, err := workflow.Parse(wv.Graph)
	if err != nil {
		return nil, fmt.Errorf("stored graph is invalid: %w", err)
	}
	order, err := g.Order()
	if err != nil {
		return nil, err
	}

	nodes := map[string]workflow.Node{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	upstream := map[string][]string{}
	hasDownstream := map[string]bool{}
	for _, e := range g.Edges {
		upstream[e.To] = append(upstream[e.To], e.From)
		hasDownstream[e.From] = true
	}

	outputs := map[string]json.RawMessage{}
	for _, nid := range order {
		input := map[string]json.RawMessage{workflow.InputKey: run.Input}
		for _, up := range upstream[nid] {
			input[up] = outputs[up]
		}
		rawInput, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		out, err := w.runNode(ctx, id, nodes[nid], rawInput)
		if err != nil {
			return nil, fmt.Errorf("node %s: %w", nid, err)
		}
		outputs[nid] = out
	}

	final := map[string]json.RawMessage{}
	for _, nid := range order {
		if !hasDownstream[nid] {
			final[nid] = outputs[nid]
		}
	}
	return json.Marshal(final)
}

// runNode runs one node and records it as a step.
func (w *Worker) runNode(ctx context.Context, runID uuid.UUID, node workflow.Node, input json.RawMessage) (json.RawMessage, error) {
	built, buildErr := w.Builder.Build(ctx, node.Agent)

	params := db.CreateRunStepParams{RunID: runID, NodeID: node.ID, Input: input}
	if buildErr == nil {
		params.AgentID, params.AgentVersion = &built.AgentID, &built.Version
	}
	stepID, err := w.Queries.CreateRunStep(ctx, params)
	if err != nil {
		return nil, err
	}

	finish := func(output json.RawMessage, transcript []*schema.Message, prompt, completion int, stepErr error) error {
		p := db.FinishRunStepParams{
			ID: stepID, Status: runs.StatusSucceeded, Output: output,
			Messages: marshalTranscript(transcript), PromptTokens: int32(prompt), CompletionTokens: int32(completion),
		}
		if stepErr != nil {
			msg := stepErr.Error()
			p.Status, p.Error = runs.StatusFailed, &msg
		}
		if err := w.Queries.FinishRunStep(context.WithoutCancel(ctx), p); err != nil {
			return err
		}
		return stepErr
	}

	if buildErr != nil {
		return nil, finish(nil, nil, 0, 0, buildErr)
	}

	transcript := []*schema.Message{schema.UserMessage(nodePrompt(node, input, built.OutputSchema))}
	var prompt, completion int

	// One attempt, plus one correction if the reply doesn't fit the schema.
	for attempt := 0; ; attempt++ {
		turn, err := built.Run(ctx, transcript, nil)
		transcript = append(transcript, turn.Messages...)
		prompt += turn.PromptTokens
		completion += turn.CompletionTokens
		if err != nil {
			return nil, finish(nil, transcript, prompt, completion, err)
		}

		out, parseErr := workflow.ParseOutput(turn.Final, built.OutputSchema)
		if parseErr == nil {
			return out, finish(out, transcript, prompt, completion, nil)
		}
		if attempt == 1 {
			return nil, finish(nil, transcript, prompt, completion, parseErr)
		}
		transcript = append(transcript, schema.UserMessage(fmt.Sprintf(
			"Your reply couldn't be used: %s\nReply again with only a JSON object that matches the schema.", parseErr)))
	}
}

func nodePrompt(node workflow.Node, input json.RawMessage, outputSchema json.RawMessage) string {
	var b strings.Builder
	if node.Instruction != "" {
		b.WriteString(node.Instruction)
		b.WriteString("\n\n")
	}
	b.WriteString("## Input\n\n`input` is the workflow's input; other keys are the outputs of earlier steps.\n\n```json\n")
	b.Write(indent(input))
	b.WriteString("\n```\n")
	if len(outputSchema) > 0 {
		b.WriteString("\n## Output\n\nWhen you are done, reply with only a JSON object that matches this JSON Schema:\n\n```json\n")
		b.Write(indent(outputSchema))
		b.WriteString("\n```\n")
	}
	return b.String()
}

func indent(raw json.RawMessage) []byte {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return raw
	}
	return out
}

func marshalTranscript(msgs []*schema.Message) json.RawMessage {
	if len(msgs) == 0 {
		return json.RawMessage(`[]`)
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return b
}
