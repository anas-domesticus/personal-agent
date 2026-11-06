package runs_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/runs"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

// These tests use fakes for the store and agents, so they need neither
// Postgres nor Eino models.

type fakeRecorder struct {
	graph    workflow.Graph
	finished error
	steps    []runs.StepResult
}

func (f *fakeRecorder) Begin(context.Context, uuid.UUID) (domain.Run, workflow.Graph, bool, error) {
	return domain.Run{Input: json.RawMessage(`{}`)}, f.graph, true, nil
}

func (f *fakeRecorder) Finish(_ context.Context, _ uuid.UUID, _ json.RawMessage, err error) error {
	f.finished = err
	return nil
}

func (f *fakeRecorder) StartStep(context.Context, uuid.UUID, string, *runs.StepAgent, json.RawMessage) (int64, error) {
	return int64(len(f.steps) + 1), nil
}

func (f *fakeRecorder) FinishStep(_ context.Context, _ int64, r runs.StepResult) error {
	f.steps = append(f.steps, r)
	return nil
}

func (f *fakeRecorder) Reap(context.Context) (int, error) { return 0, nil }

type fakeAgents map[string]func() (agent.Turn, error)

func (f fakeAgents) Build(_ context.Context, name string) (agent.Runnable, error) {
	return fakeRunnable{name: name, run: f[name]}, nil
}

type fakeRunnable struct {
	name string
	run  func() (agent.Turn, error)
}

func (r fakeRunnable) Info() agent.Info { return agent.Info{Name: r.name, Version: 1} }

func (r fakeRunnable) Run(context.Context, []*schema.Message, func(*schema.Message)) (agent.Turn, error) {
	return r.run()
}

func TestWorkerRecoversFromPanics(t *testing.T) {
	rec := &fakeRecorder{graph: workflow.Graph{Nodes: []workflow.Node{{ID: "a", Agent: "boom"}}}}
	w := &runs.Worker{Store: rec, Agents: fakeAgents{
		"boom": func() (agent.Turn, error) { panic("model client exploded") },
	}}
	err := w.Work(context.Background(), &river.Job[runs.Args]{Args: runs.Args{RunID: uuid.New()}})
	if err != nil {
		t.Fatalf("recording the failure should succeed: %v", err)
	}
	if rec.finished == nil || !strings.Contains(rec.finished.Error(), "model client exploded") {
		t.Fatalf("run not failed with the panic: %v", rec.finished)
	}
}

func TestWorkerPassesStructuredOutput(t *testing.T) {
	rec := &fakeRecorder{graph: workflow.Graph{
		Nodes: []workflow.Node{{ID: "a", Agent: "first"}, {ID: "b", Agent: "second"}},
		Edges: []workflow.Edge{{From: "a", To: "b"}},
	}}
	w := &runs.Worker{Store: rec, Agents: fakeAgents{
		"first":  func() (agent.Turn, error) { return agent.Turn{Final: "hello", Messages: []*schema.Message{{}}}, nil },
		"second": func() (agent.Turn, error) { return agent.Turn{Final: "bye", Messages: []*schema.Message{{}}}, nil },
	}}
	if err := w.Work(context.Background(), &river.Job[runs.Args]{Args: runs.Args{RunID: uuid.New()}}); err != nil {
		t.Fatal(err)
	}
	if rec.finished != nil || len(rec.steps) != 2 || string(rec.steps[0].Output) != `{"text":"hello"}` {
		t.Fatalf("finished=%v steps=%+v", rec.finished, rec.steps)
	}
}
