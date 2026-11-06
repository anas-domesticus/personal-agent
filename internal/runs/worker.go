package runs

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
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

// Recorder is what the worker reads and writes. *Service implements it.
type Recorder interface {
	Begin(ctx context.Context, id uuid.UUID) (domain.Run, workflow.Graph, bool, error)
	Finish(ctx context.Context, id uuid.UUID, output json.RawMessage, runErr error) error
	StartStep(ctx context.Context, runID uuid.UUID, node string, agent *StepAgent, input json.RawMessage) (int64, error)
	FinishStep(ctx context.Context, stepID int64, r StepResult) error
	Reap(ctx context.Context) (int, error)
}

// Agents builds an agent's current version. *agent.Builder implements it.
type Agents interface {
	Build(ctx context.Context, name string) (agent.Runnable, error)
}

// Worker executes runs.
type Worker struct {
	river.WorkerDefaults[Args]
	Store  Recorder
	Agents Agents
}

func (w *Worker) Timeout(*river.Job[Args]) time.Duration { return Timeout }

func (w *Worker) Work(ctx context.Context, job *river.Job[Args]) (err error) {
	id := job.Args.RunID
	run, graph, started, err := w.Store.Begin(ctx, id)
	if !started {
		return err // already started or finished, or the database is unreachable
	}
	if err != nil {
		return w.finish(ctx, id, nil, err)
	}

	defer func() {
		if p := recover(); p != nil {
			err = w.finish(ctx, id, nil, fmt.Errorf("panic: %v", p))
		}
	}()
	output, runErr := w.execute(ctx, run, graph)
	return w.finish(ctx, id, output, runErr)
}

// finish records the outcome even if the job's context was cancelled, and
// retries briefly so a blip doesn't leave the run unfinished. The reaper
// catches anything that still slips through.
func (w *Worker) finish(ctx context.Context, id uuid.UUID, output json.RawMessage, runErr error) error {
	ctx = context.WithoutCancel(ctx)
	if runErr != nil {
		slog.Warn("run failed", "run", id, "err", runErr)
	}
	var err error
	for attempt := range 3 {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		if err = w.Store.Finish(ctx, id, output, runErr); err == nil {
			return nil
		}
	}
	return fmt.Errorf("record run outcome: %w", err)
}

func (w *Worker) execute(ctx context.Context, run domain.Run, g workflow.Graph) (json.RawMessage, error) {
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
		out, err := w.runNode(ctx, run.ID, nodes[nid], rawInput)
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
	a, buildErr := w.Agents.Build(ctx, node.Agent)

	var ref *StepAgent
	if buildErr == nil {
		info := a.Info()
		ref = &StepAgent{ID: info.AgentID, Version: info.Version}
	}
	stepID, err := w.Store.StartStep(ctx, runID, node.ID, ref, input)
	if err != nil {
		return nil, err
	}

	var res StepResult
	finish := func() error {
		if err := w.Store.FinishStep(context.WithoutCancel(ctx), stepID, res); err != nil {
			return err
		}
		return res.Err
	}
	if buildErr != nil {
		res.Err = buildErr
		return nil, finish()
	}

	schemaRaw := a.Info().OutputSchema
	transcript := []*schema.Message{schema.UserMessage(nodePrompt(node, input, schemaRaw))}

	// One attempt, plus one correction if the reply doesn't fit the schema.
	for attempt := 0; ; attempt++ {
		turn, err := a.Run(ctx, transcript, nil)
		transcript = append(transcript, turn.Messages...)
		res.Transcript = marshalTranscript(transcript)
		res.PromptTokens += turn.PromptTokens
		res.CompletionTokens += turn.CompletionTokens
		if err != nil {
			res.Err = err
			return nil, finish()
		}

		out, parseErr := workflow.ParseOutput(turn.Final, schemaRaw)
		if parseErr == nil {
			res.Output = out
			return out, finish()
		}
		if attempt == 1 {
			res.Err = parseErr
			return nil, finish()
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
	b, err := json.Marshal(msgs)
	if err != nil || len(msgs) == 0 {
		return json.RawMessage(`[]`)
	}
	return b
}

// reapWorker runs Reap periodically.
type reapWorker struct {
	river.WorkerDefaults[reapArgs]
	store Recorder
}

func (r *reapWorker) Work(ctx context.Context, _ *river.Job[reapArgs]) error {
	_, err := r.store.Reap(ctx)
	return err
}
