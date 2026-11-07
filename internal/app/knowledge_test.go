package app_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
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
	assert.NotNil(t, a.Knowledge.SupportedBy)

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
	edgeDefinitions := a.Graph.EdgeTypes()
	require.Len(t, edgeDefinitions, 1)
	assert.Equal(t, knowledge.SupportedByKind, edgeDefinitions[0].Kind)
	assert.Equal(t, []graph.Endpoint{{Source: knowledge.ClaimKind, Target: knowledge.SourceKind}}, edgeDefinitions[0].Endpoints)
	assert.NotEmpty(t, edgeDefinitions[0].DataSchema)

	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "objective", run: func(t *testing.T) {
			t.Helper()
			roundTrip(t, a.Knowledge.Objectives, knowledge.Objective{
				Details: knowledge.Details{Title: "Improve retention", Description: "Keep useful institutional knowledge"}, SuccessCriteria: []string{"Answers cite sources"},
			}, func(value *knowledge.Objective) { value.Description = "Updated objective" })
		}},
		{name: "workflow", run: func(t *testing.T) {
			t.Helper()
			roundTrip(t, a.Knowledge.Workflows, knowledge.Workflow{
				Details: knowledge.Details{Title: "answer", Description: "Answer from knowledge"}, Graph: workflow.Graph{Nodes: []workflow.Node{{ID: "answer", Agent: "question"}}, Edges: []workflow.Edge{}},
			}, func(value *knowledge.Workflow) { value.Description = "Updated workflow" })
		}},
		{name: "claim", run: func(t *testing.T) {
			t.Helper()
			roundTrip(t, a.Knowledge.Claims, knowledge.Claim{Details: knowledge.Details{Title: "Postgres stores the knowledge graph", Description: "Storage architecture"}}, func(value *knowledge.Claim) {
				value.Title = "Postgres and River store knowledge and jobs"
			})
		}},
		{name: "source", run: func(t *testing.T) {
			t.Helper()
			roundTrip(t, a.Knowledge.Sources, knowledge.Source{
				Details: knowledge.Details{Title: "Architecture", Description: "System architecture documentation"}, URI: "https://example.com/architecture", ContentHash: "sha256:abc", Metadata: []byte(`{"publisher":"example"}`),
			}, func(value *knowledge.Source) { value.ContentHash = "sha256:def" })
		}},
		{name: "agent", run: func(t *testing.T) {
			t.Helper()
			roundTrip(t, a.Knowledge.Agents, knowledge.Agent{
				Details: knowledge.Details{Title: "question", Description: "Answer questions"}, SystemPrompt: "Answer from the graph", Model: "openai/gpt-5",
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

func TestSupportedByRelationships(t *testing.T) {
	t.Parallel()
	st, _ := testdb.Open(t)
	a, err := app.New(context.Background(), config.Config{MCP: config.MCP{}, WorkspaceDir: t.TempDir()}, app.Options{Store: st})
	require.NoError(t, err)
	t.Cleanup(func() { a.Close(context.Background()) })
	ctx := context.Background()

	claim, err := a.Knowledge.Claims.Upsert(ctx, uuid.Nil, knowledge.Claim{
		Details: knowledge.Details{Title: "The graph stores support", Description: "A test claim"},
	}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	otherClaim, err := a.Knowledge.Claims.Upsert(ctx, uuid.Nil, knowledge.Claim{
		Details: knowledge.Details{Title: "Another claim", Description: "Another test claim"},
	}, graph.NodeWriteOptions{})
	require.NoError(t, err)
	sources := make([]graph.Node[knowledge.Source], 2)
	for i := range sources {
		sources[i], err = a.Knowledge.Sources.Upsert(ctx, uuid.Nil, knowledge.Source{
			Details: knowledge.Details{Title: "Source", Description: "Supporting material"},
		}, graph.NodeWriteOptions{})
		require.NoError(t, err)
	}

	first, err := a.Knowledge.AddSupportedBy(ctx, claim.ID, sources[0].ID)
	require.NoError(t, err)
	assert.Equal(t, knowledge.SupportedByKind, first.Kind)
	assert.Equal(t, claim.ID, first.SourceID)
	assert.Equal(t, knowledge.ClaimKind, first.SourceKind)
	assert.Equal(t, sources[0].ID, first.TargetID)
	assert.Equal(t, knowledge.SourceKind, first.TargetKind)
	assert.JSONEq(t, `{}`, string(first.Data))
	assert.Equal(t, int64(1), first.Revision)

	// Repeated and invalid selectors share the relationship scenario's state.
	duplicate, err := a.Knowledge.AddSupportedBy(ctx, claim.ID, sources[0].ID)
	require.NoError(t, err)
	assert.Equal(t, first, duplicate)
	second, err := a.Knowledge.AddSupportedBy(ctx, claim.ID, sources[1].ID)
	require.NoError(t, err)
	third, err := a.Knowledge.AddSupportedBy(ctx, otherClaim.ID, sources[1].ID)
	require.NoError(t, err)

	fromClaim, err := a.Knowledge.ListSupportedBy(ctx, claim.ID, uuid.Nil)
	require.NoError(t, err)
	assert.Equal(t, []domain.KnowledgeEdge{first, second}, fromClaim)
	toSource, err := a.Knowledge.ListSupportedBy(ctx, uuid.Nil, sources[1].ID)
	require.NoError(t, err)
	assert.Equal(t, []domain.KnowledgeEdge{second, third}, toSource)

	_, err = a.Knowledge.ListSupportedBy(ctx, uuid.Nil, uuid.Nil)
	require.ErrorContains(t, err, "exactly one")
	_, err = a.Knowledge.ListSupportedBy(ctx, claim.ID, sources[0].ID)
	require.ErrorContains(t, err, "exactly one")
	_, err = a.Knowledge.AddSupportedBy(ctx, sources[0].ID, claim.ID)
	require.ErrorIs(t, err, domain.ErrConflict)

	require.NoError(t, a.Knowledge.DeleteSupportedBy(ctx, first.ID))
	fromClaim, err = a.Knowledge.ListSupportedBy(ctx, claim.ID, uuid.Nil)
	require.NoError(t, err)
	assert.Equal(t, []domain.KnowledgeEdge{second}, fromClaim)
	assert.ErrorIs(t, a.Knowledge.DeleteSupportedBy(ctx, first.ID), domain.ErrNotFound)
}

func TestKnowledgeMigrationPreservesExistingNodes(t *testing.T) {
	t.Parallel()
	st, url := testdb.Open(t)
	initial, err := app.New(context.Background(), config.Config{MCP: config.MCP{}, WorkspaceDir: t.TempDir()}, app.Options{Store: st})
	require.NoError(t, err)
	initial.Close(context.Background())

	database, err := sql.Open("pgx", url)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	provider, err := goose.NewProvider(goose.DialectPostgres, database, os.DirFS("../store/migrations"))
	require.NoError(t, err)
	_, err = provider.Down(context.Background())
	require.NoError(t, err)

	tests := []struct {
		kind       string
		oldData    string
		wantTitle  string
		wantDesc   string
		removedKey string
	}{
		{kind: "objective", oldData: `{"title":"Old objective","description":"Objective description","success_criteria":[]}`, wantTitle: "Old objective", wantDesc: "Objective description"},
		{kind: "workflow", oldData: `{"name":"old-workflow","description":"Workflow description","graph":{"nodes":[{"id":"n","agent":"a"}],"edges":[]}}`, wantTitle: "old-workflow", wantDesc: "Workflow description", removedKey: "name"},
		{kind: "claim", oldData: `{"text":"Old claim"}`, wantTitle: "Old claim", removedKey: "text"},
		{kind: "source", oldData: `{"title":"Old source","uri":"https://example.com","content_hash":"sha256:old"}`, wantTitle: "Old source"},
		{kind: "agent", oldData: `{"name":"old-agent","description":"Agent description","system_prompt":"Answer","model":"test/model","config":{},"notes":""}`, wantTitle: "old-agent", wantDesc: "Agent description", removedKey: "name"},
	}
	tx, err := database.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	for _, tt := range tests {
		id := uuid.New()
		_, err = tx.ExecContext(context.Background(), `insert into graph_nodes (id, kind, versioned, current_version) values ($1, $2, true, 1)`, id, tt.kind)
		require.NoError(t, err)
		_, err = tx.ExecContext(context.Background(), `insert into graph_node_versions (node_id, version, data) values ($1, 1, $2)`, id, tt.oldData)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	_, err = provider.Up(context.Background())
	require.NoError(t, err)

	upgraded, err := app.New(context.Background(), config.Config{MCP: config.MCP{}, WorkspaceDir: t.TempDir()}, app.Options{Store: st})
	require.NoError(t, err)
	upgraded.Close(context.Background())
	for _, tt := range tests {
		var raw []byte
		require.NoError(t, database.QueryRowContext(context.Background(), `select v.data from graph_node_versions v join graph_nodes n on n.id = v.node_id where n.kind = $1`, tt.kind).Scan(&raw))
		var data map[string]any
		require.NoError(t, json.Unmarshal(raw, &data))
		assert.Equal(t, tt.wantTitle, data["title"], tt.kind)
		assert.Equal(t, tt.wantDesc, data["description"], tt.kind)
		if tt.removedKey != "" {
			assert.NotContains(t, data, tt.removedKey, tt.kind)
		}
	}
}
