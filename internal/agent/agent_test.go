package agent_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// toolError returns a failed tool result's error message.
func toolError(t *testing.T, res map[string]any) string {
	t.Helper()
	require.Equal(t, false, res["ok"], "expected the tool to fail: %v", res)
	msg, isString := res["error"].(string)
	require.True(t, isString, "error is not a string: %v", res)
	return msg
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
