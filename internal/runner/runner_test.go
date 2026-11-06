package runner_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/runs"
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

func waitRun(t *testing.T, e *testenv.Env, id uuid.UUID) catalog.RunDetail {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		r, err := e.Catalog.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if runs.Terminal(r.Run.Status) {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s still %s", id, r.Run.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

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
		if strings.Contains(lastUser(msgs), `"ranked"`) && strings.Contains(lastUser(msgs), `"day": "mon"`) {
			return schema.AssistantMessage("got ranking", nil)
		}
		return schema.AssistantMessage("missing input", nil)
	})

	ok(t)(e.Catalog.CreateAgent(ctx, "ranker", "ranks", catalog.AgentVersionInput{
		SystemPrompt: "Rank.", Model: "fake/ranker", OutputSchema: json.RawMessage(rankSchema),
	}))
	ok(t)(e.Catalog.CreateAgent(ctx, "writer", "writes", catalog.AgentVersionInput{
		SystemPrompt: "Write.", Model: "fake/writer",
	}))
	graph := workflow.Graph{
		Nodes: []workflow.Node{{ID: "rank", Agent: "ranker", Instruction: "Rank the emails."}, {ID: "write", Agent: "writer"}},
		Edges: []workflow.Edge{{From: "rank", To: "write"}},
	}
	ok(t)(e.Catalog.CreateWorkflow(ctx, "triage", "Rank and summarise", graph, ""))

	id, err := e.Catalog.StartRun(ctx, "triage", json.RawMessage(`{"day":"mon"}`), "user")
	if err != nil {
		t.Fatal(err)
	}
	r := waitRun(t, e, id)

	if r.Run.Status != runs.StatusSucceeded {
		t.Fatalf("status = %s, error = %v", r.Run.Status, deref(r.Run.Error))
	}
	if !jsonEqual(r.Run.Output, `{"write":{"text":"got ranking"}}`) {
		t.Fatalf("output = %s", r.Run.Output)
	}
	if len(r.Steps) != 2 {
		t.Fatalf("steps = %d", len(r.Steps))
	}
	rank := r.Steps[0]
	if !jsonEqual(rank.Output, `{"ranked":["b","a"]}`) || *rank.AgentVersion != 1 {
		t.Fatalf("rank step = %s v%d", rank.Output, *rank.AgentVersion)
	}
	var transcript []*schema.Message
	if err := json.Unmarshal(rank.Messages, &transcript); err != nil {
		t.Fatal(err)
	}
	// prompt, prose reply, correction, JSON reply
	if len(transcript) != 4 || !strings.Contains(transcript[2].Content, "couldn't be used") {
		t.Fatalf("transcript has %d messages; correction missing", len(transcript))
	}

	t.Run("nodes use the agent's current version", func(t *testing.T) {
		ok(t)(e.Catalog.AddAgentVersion(ctx, "writer", catalog.AgentVersionInput{
			SystemPrompt: "Write better.", Model: "fake/writer", Notes: "v2",
		}, true))
		id, err := e.Catalog.StartRun(ctx, "triage", json.RawMessage(`{"day":"mon"}`), "user")
		if err != nil {
			t.Fatal(err)
		}
		r := waitRun(t, e, id)
		if v := *r.Steps[1].AgentVersion; v != 2 {
			t.Fatalf("writer ran v%d, want v2", v)
		}
	})

	t.Run("a node that never matches its schema fails the run", func(t *testing.T) {
		e.SetModel("fake/stubborn", func([]*schema.Message) *schema.Message { return schema.AssistantMessage("no", nil) })
		ok(t)(e.Catalog.CreateAgent(ctx, "stubborn", "x", catalog.AgentVersionInput{
			SystemPrompt: "x", Model: "fake/stubborn", OutputSchema: json.RawMessage(rankSchema),
		}))
		ok(t)(e.Catalog.CreateWorkflow(ctx, "doomed", "x", workflow.Graph{
			Nodes: []workflow.Node{{ID: "s", Agent: "stubborn"}, {ID: "after", Agent: "writer"}},
			Edges: []workflow.Edge{{From: "s", To: "after"}},
		}, ""))
		id, err := e.Catalog.StartRun(ctx, "doomed", nil, "user")
		if err != nil {
			t.Fatal(err)
		}
		r := waitRun(t, e, id)
		if r.Run.Status != runs.StatusFailed || !strings.Contains(deref(r.Run.Error), "node s") {
			t.Fatalf("status = %s, error = %q", r.Run.Status, deref(r.Run.Error))
		}
		if len(r.Steps) != 1 || r.Steps[0].Status != runs.StatusFailed {
			t.Fatalf("downstream node should not run: %d steps", len(r.Steps))
		}
	})

	t.Run("admin tools start and inspect runs", func(t *testing.T) {
		admin, err := e.Builder.Build(ctx, agent.AdminName)
		if err != nil {
			t.Fatal(err)
		}
		res := call(t, admin.Tools, "run_workflow", `{"name": "triage", "input": {"day": "mon"}, "wait_seconds": 30}`)
		data := res["data"].(map[string]any)
		if data["status"] != runs.StatusSucceeded || len(data["steps"].([]any)) != 2 {
			t.Fatalf("run_workflow = %v", res)
		}
		if _, ok := data["steps"].([]any)[0].(map[string]any)["transcript"]; ok {
			t.Fatal("transcripts should be omitted by default")
		}

		res = call(t, admin.Tools, "list_runs", `{"workflow": "triage"}`)
		if n := len(res["data"].([]any)); n != 3 {
			t.Fatalf("list_runs = %d runs, want 3", n)
		}

		res = call(t, admin.Tools, "run_workflow", `{"name": "nope"}`)
		if res["ok"] != false {
			t.Fatalf("expected error for unknown workflow: %v", res)
		}
	})
}

func call(t *testing.T, ts []tool.BaseTool, name, args string) map[string]any {
	t.Helper()
	for _, bt := range ts {
		info, _ := bt.Info(context.Background())
		if info.Name != name {
			continue
		}
		out, err := bt.(tool.InvokableTool).InvokableRun(context.Background(), args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var res map[string]any
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("%s: bad output %q", name, out)
		}
		return res
	}
	t.Fatalf("tool %q not available", name)
	return nil
}

// ok fails the test if a (value, error) call returned an error.
func ok(t *testing.T) func(any, error) {
	return func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func jsonEqual(got []byte, want string) bool {
	var a, b any
	if json.Unmarshal(got, &a) != nil || json.Unmarshal([]byte(want), &b) != nil {
		return false
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
