package runs_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
	"github.com/anas-domesticus/personal-agent/internal/testenv"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

const rankSchema = `{"type":"object","properties":{"ranked":{"type":"array","items":{"type":"string"}}},"required":["ranked"]}`

func lastUser(msgs []*schema.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == schema.User {
			return msgs[i].Content
		}
	}
	return ""
}

func waitRun(t *testing.T, e *testenv.Env, id uuid.UUID) domain.RunDetail {
	t.Helper()
	var (
		r   domain.RunDetail
		err error
	)
	// The condition runs on another goroutine, so it can't fail the test
	// itself: stop on an error and check it afterwards.
	require.Eventually(t, func() bool {
		r, err = e.Runs.Get(context.Background(), id)
		return err != nil || r.Status.Terminal()
	}, 30*time.Second, 100*time.Millisecond, "run %s did not finish", id)
	require.NoError(t, err)
	return r
}

// TestRunWorkflow is an ordered scenario over one stack: later subtests
// build on the agents, workflows and runs created earlier, so they run in
// sequence.
func TestRunWorkflow(t *testing.T) {
	e := testenv.New(t, "fake/admin")
	ctx := context.Background()

	// The ranker first replies with prose, then corrects itself.
	var rankerCalls atomic.Int32
	e.SetModel("fake/ranker", func(msgs []*schema.Message) *schema.Message {
		if rankerCalls.Add(1) == 1 {
			return schema.AssistantMessage("I think email b is most urgent.", nil)
		}
		return schema.AssistantMessage("```json\n{\"ranked\": [\"b\", \"a\"]}\n```", nil)
	})
	// The writer echoes whether it received the ranker's structured output.
	e.SetModel("fake/writer", func(msgs []*schema.Message) *schema.Message {
		if u := lastUser(msgs); strings.Contains(u, `"ranked"`) && strings.Contains(u, `"day": "mon"`) {
			return schema.AssistantMessage("got ranking", nil)
		}
		return schema.AssistantMessage("missing input", nil)
	})

	ok(t)(e.Agents.Create(ctx, "ranker", "ranks", domain.AgentVersionInput{
		SystemPrompt: "Rank.", Model: "fake/ranker", OutputSchema: json.RawMessage(rankSchema),
	}))
	ok(t)(e.Agents.Create(ctx, "writer", "writes", domain.AgentVersionInput{
		SystemPrompt: "Write.", Model: "fake/writer",
	}))
	graph := workflow.Graph{
		Nodes: []workflow.Node{{ID: "rank", Agent: "ranker", Instruction: "Rank the emails."}, {ID: "write", Agent: "writer"}},
		Edges: []workflow.Edge{{From: "rank", To: "write"}},
	}
	ok(t)(e.Workflows.Create(ctx, "triage", "Rank and summarise", graph, ""))

	id, err := e.Runs.Start(ctx, "triage", json.RawMessage(`{"day":"mon"}`), "user")
	require.NoError(t, err)
	r := waitRun(t, e, id)

	require.Equal(t, domain.RunSucceeded, r.Status, "error = %v", r.Error)
	assert.JSONEq(t, `{"write":{"text":"got ranking"}}`, string(r.Output))
	require.Len(t, r.Steps, 2)
	rank := r.Steps[0]
	assert.JSONEq(t, `{"ranked":["b","a"]}`, string(rank.Output))
	require.NotNil(t, rank.AgentVersion)
	assert.Equal(t, int32(1), *rank.AgentVersion)
	var transcript []*schema.Message
	require.NoError(t, json.Unmarshal(rank.Transcript, &transcript))
	// prompt, prose reply, correction, JSON reply
	require.Len(t, transcript, 4)
	assert.Contains(t, transcript[2].Content, "couldn't be used", "correction missing")

	t.Run("nodes use the agent's current version", func(t *testing.T) {
		ok(t)(e.Agents.AddVersion(ctx, "writer", domain.AgentVersionInput{
			SystemPrompt: "Write better.", Model: "fake/writer", Notes: "v2",
		}, true))
		id, err := e.Runs.Start(ctx, "triage", json.RawMessage(`{"day":"mon"}`), "user")
		require.NoError(t, err)
		r := waitRun(t, e, id)
		require.Len(t, r.Steps, 2)
		require.NotNil(t, r.Steps[1].AgentVersion)
		assert.Equal(t, int32(2), *r.Steps[1].AgentVersion, "writer ran the wrong version")
	})

	t.Run("a node that never matches its schema fails the run", func(t *testing.T) {
		e.SetModel("fake/stubborn", func([]*schema.Message) *schema.Message { return schema.AssistantMessage("no", nil) })
		ok(t)(e.Agents.Create(ctx, "stubborn", "x", domain.AgentVersionInput{
			SystemPrompt: "x", Model: "fake/stubborn", OutputSchema: json.RawMessage(rankSchema),
		}))
		ok(t)(e.Workflows.Create(ctx, "doomed", "x", workflow.Graph{
			Nodes: []workflow.Node{{ID: "s", Agent: "stubborn"}, {ID: "after", Agent: "writer"}},
			Edges: []workflow.Edge{{From: "s", To: "after"}},
		}, ""))
		id, err := e.Runs.Start(ctx, "doomed", nil, "user")
		require.NoError(t, err)
		r := waitRun(t, e, id)
		assert.Equal(t, domain.RunFailed, r.Status)
		assert.Contains(t, r.Error, "node s")
		require.Len(t, r.Steps, 1, "downstream node should not run")
		assert.Equal(t, domain.RunFailed, r.Steps[0].Status)
	})

}

// ok fails the test if a (value, error) call returned an error.
func ok(t *testing.T) func(any, error) {
	t.Helper()
	return func(_ any, err error) {
		t.Helper()
		require.NoError(t, err)
	}
}

func TestReapInterruptedRuns(t *testing.T) {
	e := testenv.New(t, "fake/admin")
	ctx := context.Background()

	ok(t)(e.Agents.Create(ctx, "solo", "x", domain.AgentVersionInput{SystemPrompt: "x", Model: "fake/solo"}))
	ok(t)(e.Workflows.Create(ctx, "solo-flow", "x", workflow.Graph{Nodes: []workflow.Node{{ID: "a", Agent: "solo"}}}, ""))
	w, err := e.Workflows.Get(ctx, "solo-flow")
	require.NoError(t, err)

	// A run whose worker died mid-step: running, with a running step, and no
	// job left in the queue.
	orphan, err := e.Store.CreateRun(ctx, db.CreateRunParams{WorkflowID: w.ID, WorkflowVersion: 1, Input: json.RawMessage(`{}`)})
	require.NoError(t, err)
	_, err = e.Store.StartRun(ctx, orphan.ID)
	require.NoError(t, err)
	_, err = e.Store.CreateRunStep(ctx, db.CreateRunStepParams{RunID: orphan.ID, NodeID: "a", Input: json.RawMessage(`{}`)})
	require.NoError(t, err)
	_, err = e.Store.Pool.Exec(ctx, `update runs set created_at = now() - interval '2 minutes' where id = $1`, orphan.ID)
	require.NoError(t, err)

	// A healthy run must be left alone.
	healthy, err := e.Runs.Start(ctx, "solo-flow", nil, "user")
	require.NoError(t, err)

	n, err := e.Runs.Reap(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "reaped runs")

	r, err := e.Runs.Get(ctx, orphan.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunFailed, r.Status)
	assert.Contains(t, r.Error, "interrupted")
	require.NotEmpty(t, r.Steps)
	assert.Equal(t, domain.RunFailed, r.Steps[0].Status)

	got := waitRun(t, e, healthy)
	assert.Equal(t, domain.RunSucceeded, got.Status, "healthy run error = %q", got.Error)
}
