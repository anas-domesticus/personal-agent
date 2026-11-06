package agent_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/testenv"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

// ok fails the test if a (value, error) call returned an error.
func ok(t *testing.T) func(any, error) {
	t.Helper()
	return func(_ any, err error) {
		t.Helper()
		require.NoError(t, err)
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
		info, err := bt.Info(ctx)
		require.NoError(t, err)
		if info.Name != name {
			continue
		}
		out, err := bt.(tool.InvokableTool).InvokableRun(ctx, args)
		require.NoError(t, err, name)
		var res map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &res), "%s: bad output %q", name, out)
		return res
	}
	require.Failf(t, "tool not available", "tool %q", name)
	return nil
}

// toolError returns a failed tool result's error message.
func toolError(t *testing.T, res map[string]any) string {
	t.Helper()
	require.Equal(t, false, res["ok"], "expected the tool to fail: %v", res)
	msg, isString := res["error"].(string)
	require.True(t, isString, "error is not a string: %v", res)
	return msg
}

// toolsOf returns the tool instances an agent's current version gets.
func toolsOf(t *testing.T, e *testenv.Env, name string) []tool.BaseTool {
	t.Helper()
	d, err := e.Agents.Get(context.Background(), name)
	require.NoError(t, err)
	return e.Tools.For(d.Current.Config.Tools, d.Admin)
}

// TestAdminAgent is an ordered scenario over one stack: each subtest builds
// on the agents and workflows the earlier ones created, so they run in
// sequence.
func TestAdminAgent(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	admin, err := e.Builder.Build(ctx, agent.AdminName)
	require.NoError(t, err)
	adminTools := toolsOf(t, e, agent.AdminName)
	assert.Subset(t, admin.Info().Tools, []string{"create_agent", "create_workflow", "list_tools", "remember", "current_time",
		"web_search", "web_fetch", "subagent", "read", "write", "edit", "glob", "grep", "shell"})

	t.Run("EnsureAdmin is idempotent", func(t *testing.T) {
		require.NoError(t, agent.EnsureAdmin(ctx, e.Agents, "openai/gpt-5"))
		d, err := e.Agents.Get(ctx, agent.AdminName)
		require.NoError(t, err)
		require.NotNil(t, d.CurrentVersion)
		assert.Equal(t, int32(1), *d.CurrentVersion, "admin was overwritten")
		assert.Equal(t, "deepseek/deepseek-v4.1-flash", d.Current.Model, "admin was overwritten")
	})

	t.Run("create agent with structured output", func(t *testing.T) {
		res := call(t, adminTools, "create_agent", `{
			"name": "triager", "description": "Ranks email by urgency",
			"system_prompt": "Rank the emails.", "model": "openai/gpt-5-mini",
			"config": {"tools": {"search_memories": true}},
			"output_schema": {"type": "object", "properties": {"ranked": {"type": "array"}}}
		}`)
		require.Equal(t, true, res["ok"], "create_agent failed: %v", res)
		d, err := e.Agents.Get(ctx, "triager")
		require.NoError(t, err)
		assert.False(t, d.Admin, "new agents must not be admins")
		assert.Contains(t, string(d.Current.OutputSchema), "ranked", "output schema not stored")
	})

	t.Run("admin tools cannot be assigned by name", func(t *testing.T) {
		res := call(t, adminTools, "create_agent", `{
			"name": "sneaky", "description": "x", "system_prompt": "x",
			"model": "openai/gpt-5-mini", "config": {"tools": {"create_agent": true}}
		}`)
		assert.Contains(t, toolError(t, res), "admin flag")
	})

	t.Run("workflow graph is validated", func(t *testing.T) {
		tests := []struct {
			name    string
			args    string
			wantErr string
		}{
			{
				name: "cycle",
				args: `{"name": "loop", "description": "x",
					"graph": {"nodes": [{"id": "a", "agent": "triager"}, {"id": "b", "agent": "triager"}],
					          "edges": [{"from": "a", "to": "b"}, {"from": "b", "to": "a"}]}}`,
				wantErr: "cycle",
			},
			{
				name: "unknown agent",
				args: `{"name": "ghost", "description": "x",
					"graph": {"nodes": [{"id": "a", "agent": "nobody"}], "edges": []}}`,
				wantErr: "unknown agents: nobody",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				res := call(t, adminTools, "create_workflow", tt.args)
				assert.Contains(t, toolError(t, res), tt.wantErr)
			})
		}
	})

	t.Run("create workflow queues an embed job in the same transaction", func(t *testing.T) {
		res := call(t, adminTools, "create_workflow", `{
			"name": "inbox-triage", "description": "Sort new email by urgency",
			"graph": {"nodes": [{"id": "rank", "agent": "triager", "instruction": "Rank today's email."}], "edges": []},
			"notes": "first cut"
		}`)
		require.Equal(t, true, res["ok"], "create_workflow failed: %v", res)
		var n int
		require.NoError(t, e.Store.Pool.QueryRow(ctx,
			`select count(*) from river_job where kind = 'embed' and args->>'target' = 'workflow'`).Scan(&n))
		assert.Equal(t, 1, n, "embed jobs")

		got := call(t, adminTools, "get_workflow", `{"name": "inbox-triage"}`)
		data, isMap := got["data"].(map[string]any)
		require.True(t, isMap, "get_workflow = %v", got)
		current, isMap := data["current"].(map[string]any)
		require.True(t, isMap, "get_workflow = %v", got)
		graph, isMap := current["graph"].(map[string]any)
		require.True(t, isMap, "get_workflow = %v", got)
		assert.Len(t, graph["nodes"], 1)
	})

	t.Run("versions and rollback", func(t *testing.T) {
		res := call(t, adminTools, "add_agent_version", `{
			"name": "triager", "system_prompt": "Rank the emails, most urgent first.",
			"model": "openai/gpt-5-mini", "activate": true, "notes": "clearer ordering"
		}`)
		require.Equal(t, true, res["ok"], "add_agent_version failed: %v", res)
		res = call(t, adminTools, "set_agent_version", `{"name": "triager", "version": 1}`)
		require.Equal(t, true, res["ok"], "rollback failed: %v", res)
		res = call(t, adminTools, "set_agent_version", `{"name": "triager", "version": 9}`)
		assert.Equal(t, false, res["ok"], "expected missing version error, got %v", res)
		d, err := e.Agents.Get(ctx, "triager")
		require.NoError(t, err)
		require.NotNil(t, d.CurrentVersion)
		assert.Equal(t, int32(1), *d.CurrentVersion)
	})

	t.Run("memory round trip without embeddings", func(t *testing.T) {
		res := call(t, adminTools, "remember", `{"content": "The boiler struggles below minus two", "tags": ["home"]}`)
		require.Equal(t, true, res["ok"], "remember failed: %v", res)
		res = call(t, adminTools, "search_memories", `{"query": "boiler"}`)
		assert.Len(t, res["data"], 1)
	})

	t.Run("non-admin agent gets only its listed tools", func(t *testing.T) {
		b, err := e.Builder.Build(ctx, "triager")
		require.NoError(t, err)
		assert.Equal(t, []string{"search_memories"}, b.Info().Tools)
	})
}

// Checks the schema the model sees includes fields from embedded structs.
func TestToolSchemas(t *testing.T) {
	e := setup(t)
	var found bool
	for _, bt := range toolsOf(t, e, agent.AdminName) {
		info, err := bt.Info(context.Background())
		require.NoError(t, err)
		if info.Name != "create_agent" {
			continue
		}
		found = true
		js, err := info.ToJSONSchema()
		require.NoError(t, err)
		b, err := json.Marshal(js)
		require.NoError(t, err)
		for _, field := range []string{"system_prompt", "model", "output_schema", "name"} {
			assert.Contains(t, string(b), `"`+field+`"`, "create_agent schema missing %q", field)
		}
	}
	assert.True(t, found, "admin agent has no create_agent tool")
}

// TestAgentVersionRules runs its subtests in order against one database:
// later ones use the "tuned" agent created earlier and count its versions.
func TestAgentVersionRules(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	v := func(cfg string) domain.AgentVersionInput {
		return domain.AgentVersionInput{SystemPrompt: "x", Model: "openai/gpt-5-mini", Config: json.RawMessage(cfg)}
	}

	t.Run("config is validated", func(t *testing.T) {
		tests := []struct {
			name    string
			agent   string
			cfg     string
			wantErr string
		}{
			{name: "unknown key", agent: "typo", cfg: `{"max_iteration": 5}`, wantErr: "max_iteration"},
			{name: "temperature out of range", agent: "hot", cfg: `{"temperature": 7}`, wantErr: "temperature"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := e.Agents.Create(ctx, tt.agent, "x", v(tt.cfg))
				require.ErrorContains(t, err, tt.wantErr)
			})
		}

		got, err := e.Agents.Create(ctx, "tuned", "x", v(`{"temperature": 0.2, "max_iterations": 5}`))
		require.NoError(t, err)
		assert.Equal(t, 5, got.Config.MaxIterations)
		require.NotNil(t, got.Config.Temperature)
		assert.InDelta(t, 0.2, *got.Config.Temperature, 1e-6)
	})

	t.Run("names are validated", func(t *testing.T) {
		_, err := e.Agents.Create(ctx, "Bad Name", "x", v(`{}`))
		require.Error(t, err, "invalid name should be rejected")
		_, err = e.Agents.Create(ctx, "tuned", "x", v(`{}`))
		require.ErrorIs(t, err, domain.ErrExists)
	})

	t.Run("versions are immutable", func(t *testing.T) {
		_, err := e.Store.Pool.Exec(ctx, `update agent_versions set system_prompt = 'changed'`)
		require.ErrorContains(t, err, "immutable")
		ok(t)(e.Workflows.Create(ctx, "frozen", "x", workflow.Graph{Nodes: []workflow.Node{{ID: "a", Agent: "tuned"}}}, ""))
		_, err = e.Store.Pool.Exec(ctx, `update workflow_versions set notes = 'changed'`)
		require.ErrorContains(t, err, "immutable")
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
			require.NoError(t, err)
		}
		vs, err := e.Agents.Versions(ctx, "tuned")
		require.NoError(t, err)
		require.Len(t, vs, 9)
		assert.Equal(t, int32(9), vs[0].Version)
	})

	t.Run("a version listing a removed tool still builds", func(t *testing.T) {
		// Simulate a tool that existed when the version was written but has
		// since been removed from the code.
		d, err := e.Agents.Get(ctx, "tuned")
		require.NoError(t, err)
		_, err = e.Store.Pool.Exec(ctx, `
			insert into agent_versions (agent_id, version, system_prompt, model, config)
			values ($1, 10, 'x', 'openai/gpt-5-mini', '{"tools": {"current_time": true, "retired_tool": true}}')`, d.ID)
		require.NoError(t, err)
		require.NoError(t, e.Agents.SetVersion(ctx, "tuned", 10))
		b, err := e.Builder.Build(ctx, "tuned")
		require.NoError(t, err, "build should skip the missing tool")
		assert.Equal(t, []string{"current_time"}, b.Info().Tools)
	})
}
