package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/store"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
	"github.com/anas-domesticus/personal-agent/internal/testdb"
)

// vec builds a 1536-dimension vector with the given leading values.
func vec(lead ...float32) pgvector.Vector {
	v := make([]float32, 1536)
	copy(v, lead)
	return pgvector.NewVector(v)
}

// TestStore runs against one database. Its subtests are an ordered scenario:
// later steps rely on rows earlier ones created (the search step expects the
// workflow from "workflow versions", and the constraint checks reuse the
// agent and workflow), so they run sequentially rather than in parallel.
func TestStore(t *testing.T) {
	st, url := testdb.Open(t)
	ctx := context.Background()

	t.Run("migrate is idempotent", func(t *testing.T) {
		require.NoError(t, store.Migrate(ctx, url))
	})

	var agent db.Agent
	t.Run("agent versions", func(t *testing.T) {
		var err error
		agent, err = st.CreateAgent(ctx, db.CreateAgentParams{Name: "summariser", Description: "Summarises text"})
		require.NoError(t, err)
		for i, prompt := range []string{"Summarise.", "Summarise in three bullets."} {
			v, err := st.CreateAgentVersion(ctx, db.CreateAgentVersionParams{
				AgentID: agent.ID, SystemPrompt: prompt, Model: "openai/gpt-4.1-mini",
				Config: json.RawMessage(`{}`),
			})
			require.NoError(t, err)
			assert.Equal(t, int32(i+1), v.Version)
			require.NoError(t, st.SetAgentCurrentVersion(ctx, db.SetAgentCurrentVersionParams{ID: agent.ID, CurrentVersion: &v.Version}))
		}
		cur, err := st.GetCurrentAgentVersion(ctx, "summariser")
		require.NoError(t, err)
		assert.Equal(t, int32(2), cur.Version)
		assert.Equal(t, "Summarise in three bullets.", cur.SystemPrompt)
	})

	var wf db.CreateWorkflowRow
	t.Run("workflow versions", func(t *testing.T) {
		var err error
		wf, err = st.CreateWorkflow(ctx, db.CreateWorkflowParams{Name: "evening-lights", Description: "Turn on the hall lights at sunset"})
		require.NoError(t, err)
		graph := json.RawMessage(`{"nodes":[{"id":"on","agent":"lights"}],"edges":[]}`)
		v, err := st.CreateWorkflowVersion(ctx, db.CreateWorkflowVersionParams{WorkflowID: wf.ID, Graph: graph})
		require.NoError(t, err)
		require.NoError(t, st.SetWorkflowCurrentVersion(ctx, db.SetWorkflowCurrentVersionParams{ID: wf.ID, CurrentVersion: &v.Version}))
	})

	t.Run("constraints and triggers", func(t *testing.T) {
		require.NotZero(t, agent.ID, "needs the agent from the earlier step")
		require.NotZero(t, wf.ID, "needs the workflow from the earlier step")
		missing := int32(99)

		tests := []struct {
			name     string
			exec     func() error // or sql, run with args
			sql      string
			args     []any
			wantCode string // SQLSTATE
			wantMsg  string // substring of the database message; empty means unchecked
		}{
			{name: "agent names are unique", wantCode: "23505", exec: func() error {
				_, err := st.CreateAgent(ctx, db.CreateAgentParams{Name: "summariser"})
				return err
			}},
			{name: "workflow names are unique", wantCode: "23505", exec: func() error {
				_, err := st.CreateWorkflow(ctx, db.CreateWorkflowParams{Name: "evening-lights"})
				return err
			}},
			{name: "agent current version must exist", wantCode: "23503", exec: func() error {
				return st.SetAgentCurrentVersion(ctx, db.SetAgentCurrentVersionParams{ID: agent.ID, CurrentVersion: &missing})
			}},
			{name: "workflow current version must exist", wantCode: "23503", exec: func() error {
				return st.SetWorkflowCurrentVersion(ctx, db.SetWorkflowCurrentVersionParams{ID: wf.ID, CurrentVersion: &missing})
			}},
			{name: "workflow graph must be an object", wantCode: "23514", exec: func() error {
				_, err := st.CreateWorkflowVersion(ctx, db.CreateWorkflowVersionParams{WorkflowID: wf.ID, Graph: json.RawMessage(`[]`)})
				return err
			}},
			{name: "agent output schema must be an object", wantCode: "23514", exec: func() error {
				_, err := st.CreateAgentVersion(ctx, db.CreateAgentVersionParams{
					AgentID: agent.ID, SystemPrompt: "x", Model: "a/b", Config: json.RawMessage(`{}`),
					OutputSchema: json.RawMessage(`[]`),
				})
				return err
			}},
			{name: "agent versions are immutable", wantCode: "P0001", wantMsg: "agent_versions rows are immutable",
				sql: `update agent_versions set system_prompt = 'changed' where agent_id = $1`, args: []any{agent.ID}},
			{name: "workflow versions are immutable", wantCode: "P0001", wantMsg: "workflow_versions rows are immutable",
				sql: `update workflow_versions set notes = 'changed' where workflow_id = $1`, args: []any{wf.ID}},
		}
		// Sequential: the cases share the database. Each one fails, so none
		// changes what the next sees.
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var err error
				if tt.sql != "" {
					_, err = st.Pool.Exec(ctx, tt.sql, tt.args...)
				} else {
					err = tt.exec()
				}
				var pgErr *pgconn.PgError
				require.ErrorAs(t, err, &pgErr)
				assert.Equal(t, tt.wantCode, pgErr.Code)
				if tt.wantMsg != "" {
					assert.Contains(t, pgErr.Message, tt.wantMsg)
				}
			})
		}
	})

	t.Run("workflow search", func(t *testing.T) {
		w, err := st.CreateWorkflow(ctx, db.CreateWorkflowParams{Name: "inbox-triage", Description: "Sort new email by urgency"})
		require.NoError(t, err)
		e := vec(1, 0)
		n, err := st.SetWorkflowEmbedding(ctx, db.SetWorkflowEmbeddingParams{
			ID: w.ID, Name: w.Name, Description: w.Description, Embedding: &e, EmbeddingModel: textOf("test"),
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), n)
		stale, err := st.SetWorkflowEmbedding(ctx, db.SetWorkflowEmbeddingParams{
			ID: w.ID, Name: w.Name, Description: "an older description", Embedding: &e, EmbeddingModel: textOf("test"),
		})
		require.NoError(t, err)
		assert.Equal(t, int64(0), stale, "stale write should be skipped")

		// Keyword-only hit: evening-lights has no embedding.
		got, err := st.SearchWorkflows(ctx, db.SearchWorkflowsParams{UseSemantic: true, Embedding: vec(1, 0), Model: "test", Query: "lights", Candidates: 10, Lim: 5})
		require.NoError(t, err)
		names := workflowNames(got)
		assert.Contains(t, names, "evening-lights")
		assert.Contains(t, names, "inbox-triage")

		missing, err := st.ListWorkflowsMissingEmbedding(ctx, db.ListWorkflowsMissingEmbeddingParams{Model: "test", Lim: 10})
		require.NoError(t, err)
		lights, err := st.GetWorkflowByName(ctx, "evening-lights")
		require.NoError(t, err)
		assert.Equal(t, []uuid.UUID{lights.ID}, missing)

		// Vectors from another embedding model are never compared.
		other, err := st.SearchWorkflows(ctx, db.SearchWorkflowsParams{UseSemantic: true, Embedding: vec(1, 0), Model: "other", Query: "zzz", Candidates: 10, Lim: 5})
		require.NoError(t, err)
		assert.Empty(t, workflowNames(other), "matched vectors from another model")
	})

}

func workflowNames(rows []db.SearchWorkflowsRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

func textOf(s string) *string { return &s }
