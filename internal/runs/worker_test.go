package runs_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/runs"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

// These tests need neither Postgres nor Eino models.

// fakeRecorder is a fake rather than a MockRecorder because it is stateful:
// the tests assert on the steps and outcome it recorded, not on the calls.
type fakeRecorder struct {
	graph workflow.Graph

	mu       sync.Mutex
	finished bool
	output   json.RawMessage
	runErr   error
	steps    []runs.StepResult
}

var _ runs.Recorder = (*fakeRecorder)(nil)

func (f *fakeRecorder) Begin(context.Context, uuid.UUID) (domain.Run, workflow.Graph, bool, error) {
	return domain.Run{Input: json.RawMessage(`{}`)}, f.graph, true, nil
}

func (f *fakeRecorder) Finish(_ context.Context, _ uuid.UUID, output json.RawMessage, runErr error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished, f.output, f.runErr = true, output, runErr
	return nil
}

func (f *fakeRecorder) StartStep(context.Context, uuid.UUID, string, *runs.StepAgent, json.RawMessage) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.steps) + 1), nil
}

func (f *fakeRecorder) FinishStep(_ context.Context, _ int64, r runs.StepResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, r)
	return nil
}

func (f *fakeRecorder) Reap(context.Context) (int, error) { return 0, nil }

// fakeRunnable plays back scripted replies, one per Run call. It is a fake
// because agent.MockRunnable lives in package agent's test files and can't be
// imported here, and scripted playback reads better than per-call
// expectations anyway.
type fakeRunnable struct {
	info    agent.Info
	replies []func() (agent.Turn, error)

	mu    sync.Mutex
	calls int
}

var _ agent.Runnable = (*fakeRunnable)(nil)

func (r *fakeRunnable) Info() agent.Info { return r.info }

func (r *fakeRunnable) Run(context.Context, []*schema.Message, func(*schema.Message)) (agent.Turn, error) {
	r.mu.Lock()
	reply := r.replies[min(r.calls, len(r.replies)-1)]
	r.calls++
	r.mu.Unlock()
	return reply()
}

// reply is a scripted turn whose final answer is text.
func reply(text string) func() (agent.Turn, error) {
	return func() (agent.Turn, error) {
		return agent.Turn{Final: text, Messages: []*schema.Message{schema.AssistantMessage(text, nil)}}, nil
	}
}

const countSchema = `{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}`

func TestWorkerExecutesGraph(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		graph workflow.Graph
		setup func(m *runs.MockAgents)
		// wantOutputs are the recorded steps' outputs, "" for a failed step.
		wantOutputs []string
		wantOutput  string // the run's output; empty when it fails
		wantErr     string // substring of the run's error; empty means success
	}{
		{
			name:  "panic fails the run",
			graph: workflow.Graph{Nodes: []workflow.Node{{ID: "a", Agent: "boom"}}},
			setup: func(m *runs.MockAgents) {
				m.EXPECT().Build(mock.Anything, "boom").Return(&fakeRunnable{replies: []func() (agent.Turn, error){
					func() (agent.Turn, error) { panic("model client exploded") },
				}}, nil).Once()
			},
			wantErr: "model client exploded",
		},
		{
			name: "structured output passes downstream",
			graph: workflow.Graph{
				Nodes: []workflow.Node{{ID: "a", Agent: "first"}, {ID: "b", Agent: "second"}},
				Edges: []workflow.Edge{{From: "a", To: "b"}},
			},
			setup: func(m *runs.MockAgents) {
				m.EXPECT().Build(mock.Anything, "first").
					Return(&fakeRunnable{replies: []func() (agent.Turn, error){reply("hello")}}, nil).Once()
				m.EXPECT().Build(mock.Anything, "second").
					Return(&fakeRunnable{replies: []func() (agent.Turn, error){reply("bye")}}, nil).Once()
			},
			wantOutputs: []string{`{"text":"hello"}`, `{"text":"bye"}`},
			wantOutput:  `{"b":{"text":"bye"}}`,
		},
		{
			name:  "a reply that misses the schema is corrected once",
			graph: workflow.Graph{Nodes: []workflow.Node{{ID: "a", Agent: "counter"}}},
			setup: func(m *runs.MockAgents) {
				m.EXPECT().Build(mock.Anything, "counter").Return(&fakeRunnable{
					info:    agent.Info{Name: "counter", Version: 1, OutputSchema: json.RawMessage(countSchema)},
					replies: []func() (agent.Turn, error){reply("about three"), reply(`{"n": 3}`)},
				}, nil).Once()
			},
			wantOutputs: []string{`{"n":3}`},
			wantOutput:  `{"a":{"n":3}}`,
		},
		{
			name: "a reply that never fits the schema fails the run",
			graph: workflow.Graph{
				Nodes: []workflow.Node{{ID: "a", Agent: "counter"}, {ID: "b", Agent: "never"}},
				Edges: []workflow.Edge{{From: "a", To: "b"}},
			},
			setup: func(m *runs.MockAgents) {
				m.EXPECT().Build(mock.Anything, "counter").Return(&fakeRunnable{
					info:    agent.Info{Name: "counter", Version: 1, OutputSchema: json.RawMessage(countSchema)},
					replies: []func() (agent.Turn, error){reply("no")},
				}, nil).Once()
			},
			wantOutputs: []string{""},
			wantErr:     "node a",
		},
		{
			name:  "an agent that can't be built fails its step",
			graph: workflow.Graph{Nodes: []workflow.Node{{ID: "a", Agent: "ghost"}}},
			setup: func(m *runs.MockAgents) {
				m.EXPECT().Build(mock.Anything, "ghost").Return(nil, domain.ErrNotFound).Once()
			},
			wantOutputs: []string{""},
			wantErr:     "node a: not found",
		},
		{
			name:  "an agent error fails the run",
			graph: workflow.Graph{Nodes: []workflow.Node{{ID: "a", Agent: "flaky"}}},
			setup: func(m *runs.MockAgents) {
				m.EXPECT().Build(mock.Anything, "flaky").Return(&fakeRunnable{replies: []func() (agent.Turn, error){
					func() (agent.Turn, error) { return agent.Turn{}, errors.New("rate limited") },
				}}, nil).Once()
			},
			wantOutputs: []string{""},
			wantErr:     "rate limited",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := &fakeRecorder{graph: tt.graph}
			agents := runs.NewMockAgents(t)
			tt.setup(agents)
			w := &runs.Worker{Store: rec, Agents: agents}

			err := w.Work(context.Background(), &river.Job[runs.Args]{Args: runs.Args{RunID: uuid.New()}})
			require.NoError(t, err, "recording the outcome should succeed")
			require.True(t, rec.finished, "run outcome not recorded")

			var outputs []string
			for _, s := range rec.steps {
				outputs = append(outputs, string(s.Output))
			}
			assert.Equal(t, tt.wantOutputs, outputs)

			if tt.wantErr != "" {
				require.ErrorContains(t, rec.runErr, tt.wantErr)
				assert.Empty(t, rec.output)
				return
			}
			require.NoError(t, rec.runErr)
			assert.JSONEq(t, tt.wantOutput, string(rec.output))
		})
	}
}

func TestWorkerBegin(t *testing.T) {
	t.Parallel()

	dbDown := errors.New("connection refused")
	tests := []struct {
		name    string
		setup   func(m *runs.MockRecorder)
		wantErr error
	}{
		{
			name: "a run already started is left alone",
			setup: func(m *runs.MockRecorder) {
				m.EXPECT().Begin(mock.Anything, mock.Anything).Return(domain.Run{}, workflow.Graph{}, false, nil).Once()
			},
		},
		{
			name: "an unreachable database is retried by River",
			setup: func(m *runs.MockRecorder) {
				m.EXPECT().Begin(mock.Anything, mock.Anything).Return(domain.Run{}, workflow.Graph{}, false, dbDown).Once()
			},
			wantErr: dbDown,
		},
		{
			name: "a run that can't start is failed",
			setup: func(m *runs.MockRecorder) {
				m.EXPECT().Begin(mock.Anything, mock.Anything).Return(domain.Run{}, workflow.Graph{}, true, dbDown).Once()
				m.EXPECT().Finish(mock.Anything, mock.Anything, json.RawMessage(nil), dbDown).Return(nil).Once()
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := runs.NewMockRecorder(t)
			tt.setup(rec)
			w := &runs.Worker{Store: rec, Agents: runs.NewMockAgents(t)}

			err := w.Work(context.Background(), &river.Job[runs.Args]{Args: runs.Args{RunID: uuid.New()}})
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
