package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/app"
	"github.com/anas-domesticus/personal-agent/internal/config"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/graph"
	"github.com/anas-domesticus/personal-agent/internal/knowledge"
	"github.com/anas-domesticus/personal-agent/internal/testdb"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

func TestKnowledgeSchemaRegistration(t *testing.T) {
	t.Parallel()
	st, _ := testdb.Open(t)
	a, err := app.New(context.Background(), config.Config{MCP: config.MCP{}, WorkspaceDir: t.TempDir()}, app.Options{Store: st})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		a.Close(ctx)
	})

	require.NotNil(t, a.Graph)
	assert.NotNil(t, a.Knowledge.Objectives)
	assert.NotNil(t, a.Knowledge.Workflows)
	assert.NotNil(t, a.Knowledge.Claims)
	assert.NotNil(t, a.Knowledge.Sources)
	assert.NotNil(t, a.Knowledge.Agents)

	definitions := a.Graph.NodeTypes()
	require.Len(t, definitions, 5)
	kinds := make([]string, len(definitions))
	for i, definition := range definitions {
		kinds[i] = definition.Kind
		assert.True(t, definition.Versioned)
		assert.NotEmpty(t, definition.DataSchema)
	}
	assert.ElementsMatch(t, []string{
		knowledge.ObjectiveKind, knowledge.WorkflowKind, knowledge.ClaimKind,
		knowledge.SourceKind, knowledge.AgentKind,
	}, kinds)

	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "objective", run: func(t *testing.T) {
			t.Helper()
			roundTrip(t, a.Knowledge.Objectives, knowledge.Objective{
				Title: "Improve retention", Description: "Keep useful institutional knowledge", SuccessCriteria: []string{"Answers cite sources"},
			}, func(value *knowledge.Objective) { value.Description = "Updated objective" })
		}},
		{name: "workflow", run: func(t *testing.T) {
			t.Helper()
			roundTrip(t, a.Knowledge.Workflows, knowledge.Workflow{
				Name: "answer", Description: "Answer from knowledge", Graph: workflow.Graph{Nodes: []workflow.Node{{ID: "answer", Agent: "question"}}, Edges: []workflow.Edge{}},
			}, func(value *knowledge.Workflow) { value.Description = "Updated workflow" })
		}},
		{name: "claim", run: func(t *testing.T) {
			t.Helper()
			roundTrip(t, a.Knowledge.Claims, knowledge.Claim{Text: "Postgres stores the knowledge graph"}, func(value *knowledge.Claim) {
				value.Text = "Postgres and River store knowledge and jobs"
			})
		}},
		{name: "source", run: func(t *testing.T) {
			t.Helper()
			roundTrip(t, a.Knowledge.Sources, knowledge.Source{
				Title: "Architecture", URI: "https://example.com/architecture", ContentHash: "sha256:abc", Metadata: []byte(`{"publisher":"example"}`),
			}, func(value *knowledge.Source) { value.ContentHash = "sha256:def" })
		}},
		{name: "agent", run: func(t *testing.T) {
			t.Helper()
			roundTrip(t, a.Knowledge.Agents, knowledge.Agent{
				Name: "question", SystemPrompt: "Answer from the graph", Model: "openai/gpt-5",
				Config: domain.AgentConfig{MaxIterations: 5}, OutputSchema: []byte(`{"type":"object","properties":{"answer":{"type":"string"}}}`),
			}, func(value *knowledge.Agent) { value.Notes = "Updated agent" })
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
	require.NoError(t, a.Start())
}

func roundTrip[T any](t *testing.T, repository *graph.VersionedNodeRepository[T], initial T, change func(*T)) {
	t.Helper()
	created, err := repository.Upsert(context.Background(), uuid.Nil, initial, graph.NodeWriteOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), created.Revision)
	current, err := repository.Current(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), current.CurrentVersion)
	assertDataEqual(t, initial, current.Data)

	changed := initial
	change(&changed)
	updated, err := repository.Upsert(context.Background(), created.ID, changed, graph.NodeWriteOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Revision)
	current, err = repository.Current(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), current.CurrentVersion)
	assertDataEqual(t, changed, current.Data)

	history, err := repository.Versions(context.Background(), created.ID, graph.HistoryOptions{})
	require.NoError(t, err)
	require.Len(t, history.Versions, 2)
	assertDataEqual(t, changed, history.Versions[0].Data)
	assertDataEqual(t, initial, history.Versions[1].Data)
	_, err = repository.Versions(context.Background(), uuid.New(), graph.HistoryOptions{})
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func assertDataEqual(t *testing.T, expected, actual any) {
	t.Helper()
	want, err := json.Marshal(expected)
	require.NoError(t, err)
	got, err := json.Marshal(actual)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
}
