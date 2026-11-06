package agent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/testenv"
)

func setup(t *testing.T) *testenv.Env {
	t.Helper()
	return testenv.New(t, "deepseek/deepseek-v4.1-flash")
}

// call invokes a tool by name with JSON args, as the model would.
func call(t *testing.T, ts []tool.BaseTool, name, args string) map[string]any {
	t.Helper()
	ctx := context.Background()
	for _, bt := range ts {
		info, _ := bt.Info(ctx)
		if info.Name != name {
			continue
		}
		out, err := bt.(tool.InvokableTool).InvokableRun(ctx, args)
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

func toolNames(ts []tool.BaseTool) map[string]bool {
	out := map[string]bool{}
	for _, t := range ts {
		info, _ := t.Info(context.Background())
		out[info.Name] = true
	}
	return out
}

func TestAdminAgent(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	admin, err := e.Builder.Build(ctx, agent.AdminName)
	if err != nil {
		t.Fatal(err)
	}
	names := toolNames(admin.Tools)
	for _, want := range []string{"create_agent", "create_workflow", "list_tools", "remember", "current_time"} {
		if !names[want] {
			t.Errorf("admin agent is missing tool %q", want)
		}
	}

	t.Run("EnsureAdmin is idempotent", func(t *testing.T) {
		if err := agent.EnsureAdmin(ctx, e.Catalog, "openai/gpt-5"); err != nil {
			t.Fatal(err)
		}
		d, _ := e.Catalog.GetAgent(ctx, agent.AdminName)
		if *d.Agent.CurrentVersion != 1 || d.Current.Model != "deepseek/deepseek-v4.1-flash" {
			t.Fatalf("admin was overwritten: v%d %s", *d.Agent.CurrentVersion, d.Current.Model)
		}
	})

	t.Run("create agent with structured output", func(t *testing.T) {
		res := call(t, admin.Tools, "create_agent", `{
			"name": "triager", "description": "Ranks email by urgency",
			"system_prompt": "Rank the emails.", "model": "openai/gpt-5-mini",
			"tools": ["search_memories"],
			"output_schema": {"type": "object", "properties": {"ranked": {"type": "array"}}}
		}`)
		if res["ok"] != true {
			t.Fatalf("create_agent failed: %v", res)
		}
		d, err := e.Catalog.GetAgent(ctx, "triager")
		if err != nil {
			t.Fatal(err)
		}
		if d.Agent.Admin {
			t.Fatal("new agents must not be admins")
		}
		if !strings.Contains(string(d.Current.OutputSchema), "ranked") {
			t.Fatalf("output schema not stored: %s", d.Current.OutputSchema)
		}
	})

	t.Run("admin tools cannot be assigned by name", func(t *testing.T) {
		res := call(t, admin.Tools, "create_agent", `{
			"name": "sneaky", "description": "x", "system_prompt": "x",
			"model": "openai/gpt-5-mini", "tools": ["create_agent"]
		}`)
		if res["ok"] != false || !strings.Contains(res["error"].(string), "cannot be assigned") {
			t.Fatalf("expected rejection, got %v", res)
		}
	})

	t.Run("workflow graph is validated", func(t *testing.T) {
		res := call(t, admin.Tools, "create_workflow", `{
			"name": "loop", "description": "x",
			"graph": {"nodes": [{"id": "a", "agent": "triager"}, {"id": "b", "agent": "triager"}],
			          "edges": [{"from": "a", "to": "b"}, {"from": "b", "to": "a"}]}
		}`)
		if res["ok"] != false || !strings.Contains(res["error"].(string), "cycle") {
			t.Fatalf("expected cycle error, got %v", res)
		}

		res = call(t, admin.Tools, "create_workflow", `{
			"name": "ghost", "description": "x",
			"graph": {"nodes": [{"id": "a", "agent": "nobody"}], "edges": []}
		}`)
		if res["ok"] != false || !strings.Contains(res["error"].(string), "unknown agents: nobody") {
			t.Fatalf("expected unknown agent error, got %v", res)
		}
	})

	t.Run("create workflow queues an embed job in the same transaction", func(t *testing.T) {
		res := call(t, admin.Tools, "create_workflow", `{
			"name": "inbox-triage", "description": "Sort new email by urgency",
			"graph": {"nodes": [{"id": "rank", "agent": "triager", "instruction": "Rank today's email."}], "edges": []},
			"notes": "first cut"
		}`)
		if res["ok"] != true {
			t.Fatalf("create_workflow failed: %v", res)
		}
		var n int
		if err := e.Store.Pool.QueryRow(ctx, `select count(*) from river_job where kind = 'embed' and args->>'target' = 'workflow'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("embed jobs = %d, want 1", n)
		}

		got := call(t, admin.Tools, "get_workflow", `{"name": "inbox-triage"}`)
		data := got["data"].(map[string]any)
		graph := data["current"].(map[string]any)["graph"].(map[string]any)
		if len(graph["nodes"].([]any)) != 1 {
			t.Fatalf("graph = %v", graph)
		}
	})

	t.Run("versions and rollback", func(t *testing.T) {
		res := call(t, admin.Tools, "add_agent_version", `{
			"name": "triager", "system_prompt": "Rank the emails, most urgent first.",
			"model": "openai/gpt-5-mini", "activate": true, "notes": "clearer ordering"
		}`)
		if res["ok"] != true {
			t.Fatalf("add_agent_version failed: %v", res)
		}
		if res := call(t, admin.Tools, "set_agent_version", `{"name": "triager", "version": 1}`); res["ok"] != true {
			t.Fatalf("rollback failed: %v", res)
		}
		if res := call(t, admin.Tools, "set_agent_version", `{"name": "triager", "version": 9}`); res["ok"] != false {
			t.Fatalf("expected missing version error, got %v", res)
		}
		d, _ := e.Catalog.GetAgent(ctx, "triager")
		if *d.Agent.CurrentVersion != 1 {
			t.Fatalf("current = %d, want 1", *d.Agent.CurrentVersion)
		}
	})

	t.Run("memory round trip without embeddings", func(t *testing.T) {
		if res := call(t, admin.Tools, "remember", `{"content": "The boiler struggles below minus two", "tags": ["home"]}`); res["ok"] != true {
			t.Fatalf("remember failed: %v", res)
		}
		res := call(t, admin.Tools, "search_memories", `{"query": "boiler"}`)
		if hits := res["data"].([]any); len(hits) != 1 {
			t.Fatalf("hits = %v", hits)
		}
	})

	t.Run("non-admin agent gets only its listed tools", func(t *testing.T) {
		b, err := e.Builder.Build(ctx, "triager")
		if err != nil {
			t.Fatal(err)
		}
		names := toolNames(b.Tools)
		if len(names) != 1 || !names["search_memories"] {
			t.Fatalf("tools = %v", names)
		}
	})
}

// Checks the schema the model sees includes fields from embedded structs.
func TestToolSchemas(t *testing.T) {
	e := setup(t)
	admin, err := e.Builder.Build(context.Background(), agent.AdminName)
	if err != nil {
		t.Fatal(err)
	}
	for _, bt := range admin.Tools {
		info, _ := bt.Info(context.Background())
		if info.Name != "create_agent" {
			continue
		}
		js, err := info.ParamsOneOf.ToJSONSchema()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(js)
		for _, field := range []string{"system_prompt", "model", "output_schema", "name"} {
			if !strings.Contains(string(b), `"`+field+`"`) {
				t.Errorf("create_agent schema missing %q: %s", field, b)
			}
		}
	}
}
