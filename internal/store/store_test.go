package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pgvector/pgvector-go"

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

func TestStore(t *testing.T) {
	st, url := testdb.Open(t)
	ctx := context.Background()

	t.Run("migrate is idempotent", func(t *testing.T) {
		if err := store.Migrate(ctx, url); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("agent versions", func(t *testing.T) {
		a, err := st.CreateAgent(ctx, db.CreateAgentParams{Name: "summariser", Description: "Summarises text"})
		if err != nil {
			t.Fatal(err)
		}
		for i, prompt := range []string{"Summarise.", "Summarise in three bullets."} {
			v, err := st.CreateAgentVersion(ctx, db.CreateAgentVersionParams{
				AgentID: a.ID, SystemPrompt: prompt, Model: "openai/gpt-4.1-mini",
				Tools: []string{}, Config: json.RawMessage(`{}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			if v.Version != int32(i+1) {
				t.Fatalf("version = %d, want %d", v.Version, i+1)
			}
			if err := st.SetAgentCurrentVersion(ctx, db.SetAgentCurrentVersionParams{ID: a.ID, CurrentVersion: &v.Version}); err != nil {
				t.Fatal(err)
			}
		}
		cur, err := st.GetCurrentAgentVersion(ctx, "summariser")
		if err != nil {
			t.Fatal(err)
		}
		if cur.Version != 2 || cur.SystemPrompt != "Summarise in three bullets." {
			t.Fatalf("current = v%d %q", cur.Version, cur.SystemPrompt)
		}

		bad := int32(99)
		if err := st.SetAgentCurrentVersion(ctx, db.SetAgentCurrentVersionParams{ID: a.ID, CurrentVersion: &bad}); err == nil {
			t.Fatal("expected foreign key error for a version that doesn't exist")
		}
	})

	t.Run("workflow versions", func(t *testing.T) {
		w, err := st.CreateWorkflow(ctx, db.CreateWorkflowParams{Name: "evening-lights", Description: "Turn on the hall lights at sunset"})
		if err != nil {
			t.Fatal(err)
		}
		graph := json.RawMessage(`{"nodes":[{"id":"on","agent":"lights"}],"edges":[]}`)
		v, err := st.CreateWorkflowVersion(ctx, db.CreateWorkflowVersionParams{WorkflowID: w.ID, Graph: graph})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetWorkflowCurrentVersion(ctx, db.SetWorkflowCurrentVersionParams{ID: w.ID, CurrentVersion: &v.Version}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateWorkflowVersion(ctx, db.CreateWorkflowVersionParams{WorkflowID: w.ID, Graph: json.RawMessage(`[]`)}); err == nil {
			t.Fatal("expected check violation for a non-object graph")
		}
	})

	t.Run("workflow search", func(t *testing.T) {
		w, err := st.CreateWorkflow(ctx, db.CreateWorkflowParams{Name: "inbox-triage", Description: "Sort new email by urgency"})
		if err != nil {
			t.Fatal(err)
		}
		e := vec(1, 0)
		n, err := st.SetWorkflowEmbedding(ctx, db.SetWorkflowEmbeddingParams{
			ID: w.ID, Name: w.Name, Description: w.Description, Embedding: &e, EmbeddingModel: textOf("test"),
		})
		if err != nil || n != 1 {
			t.Fatalf("set embedding: n=%d err=%v", n, err)
		}
		stale, err := st.SetWorkflowEmbedding(ctx, db.SetWorkflowEmbeddingParams{
			ID: w.ID, Name: w.Name, Description: "an older description", Embedding: &e, EmbeddingModel: textOf("test"),
		})
		if err != nil || stale != 0 {
			t.Fatalf("stale write should be skipped: n=%d err=%v", stale, err)
		}

		// Keyword-only hit: evening-lights has no embedding.
		got, err := st.SearchWorkflows(ctx, db.SearchWorkflowsParams{Embedding: vec(1, 0), Query: "lights", Candidates: 10, Lim: 5})
		if err != nil {
			t.Fatal(err)
		}
		if names := workflowNames(got); !contains(names, "evening-lights") || !contains(names, "inbox-triage") {
			t.Fatalf("got %v, want both workflows", names)
		}

		missing, err := st.ListWorkflowsMissingEmbedding(ctx, db.ListWorkflowsMissingEmbeddingParams{Model: "test", Lim: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(missing) != 1 || missing[0].Name != "evening-lights" {
			t.Fatalf("missing = %+v", missing)
		}
	})

	t.Run("memory search", func(t *testing.T) {
		add := func(content string, tags []string, e pgvector.Vector) {
			t.Helper()
			if _, err := st.CreateMemory(ctx, db.CreateMemoryParams{
				Content: content, Tags: tags, Metadata: json.RawMessage(`{}`),
				Embedding: &e, EmbeddingModel: textOf("test"),
			}); err != nil {
				t.Fatal(err)
			}
		}
		add("The boiler struggles below minus two degrees", []string{"home"}, vec(1, 0))
		add("Prefers the office at 19 degrees", []string{"home", "preference"}, vec(0.9, 0.1))
		add("Weekly planning happens on Monday mornings", []string{"work"}, vec(0, 1))

		// Semantic nearest to (1,0) should rank the boiler memory first.
		got, err := st.SearchMemories(ctx, db.SearchMemoriesParams{Embedding: vec(1, 0), Query: "boiler", Tags: []string{}, Candidates: 10, Lim: 3})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 || got[0].Content != "The boiler struggles below minus two degrees" {
			t.Fatalf("top result = %+v", got)
		}

		// Tag filter excludes the work memory even though it matches the vector.
		got, err = st.SearchMemories(ctx, db.SearchMemoriesParams{Embedding: vec(0, 1), Query: "planning", Tags: []string{"home"}, Candidates: 10, Lim: 5})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range got {
			if contains(m.Tags, "work") {
				t.Fatalf("tag filter leaked %q", m.Content)
			}
		}
	})
}

func workflowNames(rows []db.SearchWorkflowsRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func textOf(s string) *string { return &s }
