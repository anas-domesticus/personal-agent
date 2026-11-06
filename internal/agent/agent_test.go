package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/testenv"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

// ok fails the test if a (value, error) call returned an error.
func ok(t *testing.T) func(any, error) {
	return func(_ any, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
}

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

func toolNames(names []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		out[n] = true
	}
	return out
}

// toolsOf returns the tool instances an agent's current version gets.
func toolsOf(t *testing.T, e *testenv.Env, name string) []tool.BaseTool {
	t.Helper()
	d, err := e.Agents.Get(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return e.Tools.For(d.Current.Config.Tools, d.Admin)
}

func TestAdminAgent(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	admin, err := e.Builder.Build(ctx, agent.AdminName)
	if err != nil {
		t.Fatal(err)
	}
	names := toolNames(admin.Info().Tools)
	adminTools := toolsOf(t, e, agent.AdminName)
	for _, want := range []string{"create_agent", "create_workflow", "list_tools", "remember", "current_time",
		"web_search", "web_fetch", "subagent", "read", "write", "edit", "glob", "grep", "shell"} {
		if !names[want] {
			t.Errorf("admin agent is missing tool %q", want)
		}
	}

	t.Run("EnsureAdmin is idempotent", func(t *testing.T) {
		if err := agent.EnsureAdmin(ctx, e.Agents, "openai/gpt-5"); err != nil {
			t.Fatal(err)
		}
		d, _ := e.Agents.Get(ctx, agent.AdminName)
		if *d.CurrentVersion != 1 || d.Current.Model != "deepseek/deepseek-v4.1-flash" {
			t.Fatalf("admin was overwritten: v%d %s", *d.CurrentVersion, d.Current.Model)
		}
	})

	t.Run("create agent with structured output", func(t *testing.T) {
		res := call(t, adminTools, "create_agent", `{
			"name": "triager", "description": "Ranks email by urgency",
			"system_prompt": "Rank the emails.", "model": "openai/gpt-5-mini",
			"config": {"tools": {"search_memories": true}},
			"output_schema": {"type": "object", "properties": {"ranked": {"type": "array"}}}
		}`)
		if res["ok"] != true {
			t.Fatalf("create_agent failed: %v", res)
		}
		d, err := e.Agents.Get(ctx, "triager")
		if err != nil {
			t.Fatal(err)
		}
		if d.Admin {
			t.Fatal("new agents must not be admins")
		}
		if !strings.Contains(string(d.Current.OutputSchema), "ranked") {
			t.Fatalf("output schema not stored: %s", d.Current.OutputSchema)
		}
	})

	t.Run("admin tools cannot be assigned by name", func(t *testing.T) {
		res := call(t, adminTools, "create_agent", `{
			"name": "sneaky", "description": "x", "system_prompt": "x",
			"model": "openai/gpt-5-mini", "config": {"tools": {"create_agent": true}}
		}`)
		if res["ok"] != false || !strings.Contains(res["error"].(string), "admin flag") {
			t.Fatalf("expected rejection, got %v", res)
		}
	})

	t.Run("workflow graph is validated", func(t *testing.T) {
		res := call(t, adminTools, "create_workflow", `{
			"name": "loop", "description": "x",
			"graph": {"nodes": [{"id": "a", "agent": "triager"}, {"id": "b", "agent": "triager"}],
			          "edges": [{"from": "a", "to": "b"}, {"from": "b", "to": "a"}]}
		}`)
		if res["ok"] != false || !strings.Contains(res["error"].(string), "cycle") {
			t.Fatalf("expected cycle error, got %v", res)
		}

		res = call(t, adminTools, "create_workflow", `{
			"name": "ghost", "description": "x",
			"graph": {"nodes": [{"id": "a", "agent": "nobody"}], "edges": []}
		}`)
		if res["ok"] != false || !strings.Contains(res["error"].(string), "unknown agents: nobody") {
			t.Fatalf("expected unknown agent error, got %v", res)
		}
	})

	t.Run("create workflow queues an embed job in the same transaction", func(t *testing.T) {
		res := call(t, adminTools, "create_workflow", `{
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

		got := call(t, adminTools, "get_workflow", `{"name": "inbox-triage"}`)
		data := got["data"].(map[string]any)
		graph := data["current"].(map[string]any)["graph"].(map[string]any)
		if len(graph["nodes"].([]any)) != 1 {
			t.Fatalf("graph = %v", graph)
		}
	})

	t.Run("versions and rollback", func(t *testing.T) {
		res := call(t, adminTools, "add_agent_version", `{
			"name": "triager", "system_prompt": "Rank the emails, most urgent first.",
			"model": "openai/gpt-5-mini", "activate": true, "notes": "clearer ordering"
		}`)
		if res["ok"] != true {
			t.Fatalf("add_agent_version failed: %v", res)
		}
		if res := call(t, adminTools, "set_agent_version", `{"name": "triager", "version": 1}`); res["ok"] != true {
			t.Fatalf("rollback failed: %v", res)
		}
		if res := call(t, adminTools, "set_agent_version", `{"name": "triager", "version": 9}`); res["ok"] != false {
			t.Fatalf("expected missing version error, got %v", res)
		}
		d, _ := e.Agents.Get(ctx, "triager")
		if *d.CurrentVersion != 1 {
			t.Fatalf("current = %d, want 1", *d.CurrentVersion)
		}
	})

	t.Run("memory round trip without embeddings", func(t *testing.T) {
		if res := call(t, adminTools, "remember", `{"content": "The boiler struggles below minus two", "tags": ["home"]}`); res["ok"] != true {
			t.Fatalf("remember failed: %v", res)
		}
		res := call(t, adminTools, "search_memories", `{"query": "boiler"}`)
		if hits := res["data"].([]any); len(hits) != 1 {
			t.Fatalf("hits = %v", hits)
		}
	})

	t.Run("non-admin agent gets only its listed tools", func(t *testing.T) {
		b, err := e.Builder.Build(ctx, "triager")
		if err != nil {
			t.Fatal(err)
		}
		names := toolNames(b.Info().Tools)
		if len(names) != 1 || !names["search_memories"] {
			t.Fatalf("tools = %v", names)
		}
	})
}

// Checks the schema the model sees includes fields from embedded structs.
func TestToolSchemas(t *testing.T) {
	e := setup(t)
	for _, bt := range toolsOf(t, e, agent.AdminName) {
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

func TestAgentVersionRules(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	v := func(cfg string) domain.AgentVersionInput {
		return domain.AgentVersionInput{SystemPrompt: "x", Model: "openai/gpt-5-mini", Config: json.RawMessage(cfg)}
	}

	t.Run("config is validated", func(t *testing.T) {
		if _, err := e.Agents.Create(ctx, "typo", "x", v(`{"max_iteration": 5}`)); err == nil || !strings.Contains(err.Error(), "max_iteration") {
			t.Fatalf("unknown config key should be rejected, got %v", err)
		}
		if _, err := e.Agents.Create(ctx, "hot", "x", v(`{"temperature": 7}`)); err == nil {
			t.Fatal("out-of-range temperature should be rejected")
		}
		got, err := e.Agents.Create(ctx, "tuned", "x", v(`{"temperature": 0.2, "max_iterations": 5}`))
		if err != nil {
			t.Fatal(err)
		}
		if got.Config.MaxIterations != 5 || *got.Config.Temperature != 0.2 {
			t.Fatalf("config = %+v", got.Config)
		}
	})

	t.Run("names are validated", func(t *testing.T) {
		if _, err := e.Agents.Create(ctx, "Bad Name", "x", v(`{}`)); err == nil {
			t.Fatal("invalid name should be rejected")
		}
		if _, err := e.Agents.Create(ctx, "tuned", "x", v(`{}`)); !errors.Is(err, domain.ErrExists) {
			t.Fatalf("duplicate name: got %v, want ErrExists", err)
		}
	})

	t.Run("versions are immutable", func(t *testing.T) {
		_, err := e.Store.Pool.Exec(ctx, `update agent_versions set system_prompt = 'changed'`)
		if err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("update should be refused, got %v", err)
		}
		ok(t)(e.Workflows.Create(ctx, "frozen", "x", workflow.Graph{Nodes: []workflow.Node{{ID: "a", Agent: "tuned"}}}, ""))
		_, err = e.Store.Pool.Exec(ctx, `update workflow_versions set notes = 'changed'`)
		if err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Fatalf("update should be refused, got %v", err)
		}
	})

	t.Run("concurrent versions get distinct numbers", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Go(func() {
				_, err := e.Agents.AddVersion(ctx, "tuned", v(`{}`), false)
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		vs, err := e.Agents.Versions(ctx, "tuned")
		if err != nil {
			t.Fatal(err)
		}
		if len(vs) != 9 || vs[0].Version != 9 {
			t.Fatalf("versions = %d, newest v%d", len(vs), vs[0].Version)
		}
	})

	t.Run("a version listing a removed tool still builds", func(t *testing.T) {
		// Simulate a tool that existed when the version was written but has
		// since been removed from the code.
		d, err := e.Agents.Get(ctx, "tuned")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Store.Pool.Exec(ctx, `
			insert into agent_versions (agent_id, version, system_prompt, model, config)
			values ($1, 10, 'x', 'openai/gpt-5-mini', '{"tools": {"current_time": true, "retired_tool": true}}')`, d.ID); err != nil {
			t.Fatal(err)
		}
		if err := e.Agents.SetVersion(ctx, "tuned", 10); err != nil {
			t.Fatal(err)
		}
		b, err := e.Builder.Build(ctx, "tuned")
		if err != nil {
			t.Fatalf("build should skip the missing tool: %v", err)
		}
		if names := b.Info().Tools; len(names) != 1 || names[0] != "current_time" {
			t.Fatalf("tools = %v", names)
		}
	})
}
