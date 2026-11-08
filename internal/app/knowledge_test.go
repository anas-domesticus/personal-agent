package app_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"sync"
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

	claimData := json.RawMessage(`{"title":"The graph stores support","description":"A test claim"}`)
	claim, err := a.Knowledge.UpsertNode(ctx, knowledge.ClaimKind, uuid.Nil, claimData, nil, nil)
	require.NoError(t, err)
	otherClaim, err := a.Knowledge.UpsertNode(ctx, knowledge.ClaimKind, uuid.Nil,
		json.RawMessage(`{"title":"Another claim","description":"Another test claim"}`), nil, nil)
	require.NoError(t, err)
	sources := make([]domain.KnowledgeNode, 2)
	for i := range sources {
		sources[i], err = a.Knowledge.UpsertNode(ctx, knowledge.SourceKind, uuid.Nil,
			json.RawMessage(`{"title":"Source","description":"Supporting material"}`), nil, nil)
		require.NoError(t, err)
	}

	ref := func(node domain.KnowledgeNode) domain.KnowledgeRef {
		return domain.KnowledgeRef{ID: node.ID, Kind: node.Kind}
	}
	first, err := a.Knowledge.UpsertEdge(ctx, knowledge.SupportedByKind, ref(claim), ref(sources[0]), json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, knowledge.SupportedByKind, first.Kind)
	assert.Equal(t, claim.ID, first.SourceID)
	assert.Equal(t, knowledge.ClaimKind, first.SourceKind)
	assert.Equal(t, sources[0].ID, first.TargetID)
	assert.Equal(t, knowledge.SourceKind, first.TargetKind)
	assert.JSONEq(t, `{}`, string(first.Data))
	assert.Equal(t, int64(1), first.CurrentVersion)
	assert.Equal(t, int64(1), first.Revision)

	got, err := a.Knowledge.GetNode(ctx, claim.Kind, claim.ID)
	require.NoError(t, err)
	assert.Equal(t, claim, got)
	gotEdge, err := a.Knowledge.GetEdge(ctx, first.Kind, first.ID)
	require.NoError(t, err)
	assert.Equal(t, first, gotEdge)

	// Repeated and invalid selectors share the relationship scenario's state.
	duplicate, err := a.Knowledge.UpsertEdge(ctx, knowledge.SupportedByKind, ref(claim), ref(sources[0]), json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, first, duplicate)
	second, err := a.Knowledge.UpsertEdge(ctx, knowledge.SupportedByKind, ref(claim), ref(sources[1]), json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)
	third, err := a.Knowledge.UpsertEdge(ctx, knowledge.SupportedByKind, ref(otherClaim), ref(sources[1]), json.RawMessage(`{}`), nil, nil)
	require.NoError(t, err)

	fromClaim, err := a.Knowledge.ListEdges(ctx, knowledge.SupportedByKind, claim.ID, uuid.Nil, 0)
	require.NoError(t, err)
	assert.Equal(t, []domain.KnowledgeEdge{first, second}, fromClaim)
	toSource, err := a.Knowledge.ListEdges(ctx, knowledge.SupportedByKind, uuid.Nil, sources[1].ID, 0)
	require.NoError(t, err)
	assert.Equal(t, []domain.KnowledgeEdge{second, third}, toSource)
	allSupport, err := a.Knowledge.ListEdges(ctx, knowledge.SupportedByKind, uuid.Nil, uuid.Nil, 0)
	require.NoError(t, err)
	assert.Equal(t, []domain.KnowledgeEdge{first, second, third}, allSupport)

	claims, err := a.Knowledge.ListNodes(ctx, knowledge.ClaimKind, 0)
	require.NoError(t, err)
	assert.Equal(t, []domain.KnowledgeNode{claim, otherClaim}, claims)
	history, err := a.Knowledge.NodeHistory(ctx, claim.Kind, claim.ID, 0, 0)
	require.NoError(t, err)
	require.Len(t, history.Versions, 1)
	assert.JSONEq(t, string(claimData), string(history.Versions[0].Data))
	edgeHistory, err := a.Knowledge.EdgeHistory(ctx, first.Kind, first.ID, 0, 0)
	require.NoError(t, err)
	require.Len(t, edgeHistory.Versions, 1)
	assert.Equal(t, int64(1), edgeHistory.Versions[0].Version)

	_, err = a.Knowledge.UpsertEdge(ctx, knowledge.SupportedByKind, ref(sources[0]), ref(claim), json.RawMessage(`{}`), nil, nil)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.ErrorIs(t, a.Knowledge.DeleteNode(ctx, claim.Kind, claim.ID, nil), domain.ErrConflict)

	require.NoError(t, a.Knowledge.DeleteEdge(ctx, first.Kind, first.ID, nil))
	fromClaim, err = a.Knowledge.ListEdges(ctx, knowledge.SupportedByKind, claim.ID, uuid.Nil, 0)
	require.NoError(t, err)
	assert.Equal(t, []domain.KnowledgeEdge{second}, fromClaim)
	assert.ErrorIs(t, a.Knowledge.DeleteEdge(ctx, first.Kind, first.ID, nil), domain.ErrNotFound)
}

func TestConcurrentSupportedByCreation(t *testing.T) {
	t.Parallel()
	st, _ := testdb.Open(t)
	a, err := app.New(context.Background(), config.Config{MCP: config.MCP{}, WorkspaceDir: t.TempDir()}, app.Options{Store: st})
	require.NoError(t, err)
	t.Cleanup(func() { a.Close(context.Background()) })
	ctx := context.Background()

	claim, err := a.Knowledge.UpsertNode(ctx, knowledge.ClaimKind, uuid.Nil,
		json.RawMessage(`{"title":"Concurrent support","description":"A test claim"}`), nil, nil)
	require.NoError(t, err)
	source, err := a.Knowledge.UpsertNode(ctx, knowledge.SourceKind, uuid.Nil,
		json.RawMessage(`{"title":"Source","description":"Supporting material"}`), nil, nil)
	require.NoError(t, err)
	claimRef := domain.KnowledgeRef{ID: claim.ID, Kind: claim.Kind}
	sourceRef := domain.KnowledgeRef{ID: source.ID, Kind: source.Kind}

	const callers = 8
	type result struct {
		edge domain.KnowledgeEdge
		err  error
	}
	results := make([]result, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i].edge, results[i].err = a.Knowledge.UpsertEdge(ctx, knowledge.SupportedByKind, claimRef, sourceRef, json.RawMessage(`{}`), nil, nil)
		}()
	}
	close(start)
	wg.Wait()

	for _, result := range results {
		require.NoError(t, result.err)
		assert.Equal(t, results[0].edge, result.edge)
	}
}

func TestDynamicTypeAdministration(t *testing.T) {
	t.Parallel()
	st, _ := testdb.Open(t)
	a, err := app.New(context.Background(), config.Config{MCP: config.MCP{}, WorkspaceDir: t.TempDir()}, app.Options{Store: st})
	require.NoError(t, err)
	t.Cleanup(func() { a.Close(context.Background()) })
	ctx := context.Background()

	nodeSchema := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"description":{"type":"string"},"body":{"type":"string"}},"required":["title","description","body"],"additionalProperties":false}`)
	nodeType, err := a.Knowledge.CreateNodeType(ctx, "fact", nodeSchema)
	require.NoError(t, err)
	assert.Equal(t, "fact", nodeType.Kind)
	assert.True(t, nodeType.Versioned)
	assert.JSONEq(t, string(nodeSchema), string(nodeType.DataSchema))
	gotNodeType, err := a.Knowledge.GetNodeType(ctx, nodeType.Kind)
	require.NoError(t, err)
	assert.Equal(t, nodeType, gotNodeType)
	nodeTypes, err := a.Knowledge.ListNodeTypes(ctx)
	require.NoError(t, err)
	assert.Contains(t, nodeTypes, nodeType)

	edgeSchema := json.RawMessage(`{"type":"object","properties":{"note":{"type":"string"}},"required":["note"],"additionalProperties":false}`)
	endpoints := []domain.KnowledgeEndpoint{{Source: "fact", Target: "fact"}}
	edgeType, err := a.Knowledge.CreateEdgeType(ctx, "references", edgeSchema, endpoints)
	require.NoError(t, err)
	assert.Equal(t, "references", edgeType.Kind)
	assert.JSONEq(t, string(edgeSchema), string(edgeType.DataSchema))
	assert.Equal(t, endpoints, edgeType.Endpoints)
	gotEdgeType, err := a.Knowledge.GetEdgeType(ctx, edgeType.Kind)
	require.NoError(t, err)
	assert.Equal(t, edgeType, gotEdgeType)
	edgeTypes, err := a.Knowledge.ListEdgeTypes(ctx)
	require.NoError(t, err)
	assert.Contains(t, edgeTypes, edgeType)

	firstData := json.RawMessage(`{"title":"First fact","description":"Initial dynamic node","body":"one"}`)
	firstMetadata := json.RawMessage(`{"origin":"initial"}`)
	first, err := a.Knowledge.UpsertNode(ctx, nodeType.Kind, uuid.Nil, firstData, firstMetadata, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(1), first.CurrentVersion)
	assert.Equal(t, int64(1), first.Revision)
	second, err := a.Knowledge.UpsertNode(ctx, nodeType.Kind, uuid.Nil,
		json.RawMessage(`{"title":"Second fact","description":"Another dynamic node","body":"two"}`), nil, nil)
	require.NoError(t, err)

	firstRevision := first.Revision
	changedData := json.RawMessage(`{"title":"First fact","description":"Updated dynamic node","body":"changed"}`)
	changedMetadata := json.RawMessage(`{"origin":"update"}`)
	first, err = a.Knowledge.UpsertNode(ctx, nodeType.Kind, first.ID, changedData, changedMetadata, &firstRevision)
	require.NoError(t, err)
	assert.Equal(t, int64(2), first.CurrentVersion)
	assert.Equal(t, int64(2), first.Revision)
	gotNode, err := a.Knowledge.GetNode(ctx, first.Kind, first.ID)
	require.NoError(t, err)
	assert.Equal(t, first, gotNode)
	nodes, err := a.Knowledge.ListNodes(ctx, nodeType.Kind, 0)
	require.NoError(t, err)
	assert.Equal(t, []domain.KnowledgeNode{first, second}, nodes)
	nodeHistory, err := a.Knowledge.NodeHistory(ctx, first.Kind, first.ID, 0, 0)
	require.NoError(t, err)
	require.Len(t, nodeHistory.Versions, 2)
	assert.Equal(t, int64(2), nodeHistory.Versions[0].Version)
	assert.JSONEq(t, string(changedData), string(nodeHistory.Versions[0].Data))
	assert.JSONEq(t, string(changedMetadata), string(nodeHistory.Versions[0].Metadata))
	assert.Equal(t, int64(1), nodeHistory.Versions[1].Version)
	assert.JSONEq(t, string(firstData), string(nodeHistory.Versions[1].Data))
	assert.JSONEq(t, string(firstMetadata), string(nodeHistory.Versions[1].Metadata))

	firstRef := domain.KnowledgeRef{ID: first.ID, Kind: first.Kind}
	secondRef := domain.KnowledgeRef{ID: second.ID, Kind: second.Kind}
	edgeData := json.RawMessage(`{"note":"initial"}`)
	edgeMetadata := json.RawMessage(`{"origin":"initial"}`)
	edge, err := a.Knowledge.UpsertEdge(ctx, edgeType.Kind, firstRef, secondRef, edgeData, edgeMetadata, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(1), edge.CurrentVersion)
	assert.Equal(t, int64(1), edge.Revision)
	edgeRevision := edge.Revision
	changedEdgeData := json.RawMessage(`{"note":"changed"}`)
	changedEdgeMetadata := json.RawMessage(`{"origin":"update"}`)
	edge, err = a.Knowledge.UpsertEdge(ctx, edgeType.Kind, firstRef, secondRef, changedEdgeData, changedEdgeMetadata, &edgeRevision)
	require.NoError(t, err)
	assert.Equal(t, int64(2), edge.CurrentVersion)
	assert.Equal(t, int64(2), edge.Revision)
	gotEdge, err := a.Knowledge.GetEdge(ctx, edge.Kind, edge.ID)
	require.NoError(t, err)
	assert.Equal(t, edge, gotEdge)

	fromFirst, err := a.Knowledge.ListEdges(ctx, edgeType.Kind, first.ID, uuid.Nil, 0)
	require.NoError(t, err)
	assert.Equal(t, []domain.KnowledgeEdge{edge}, fromFirst)
	toSecond, err := a.Knowledge.ListEdges(ctx, edgeType.Kind, uuid.Nil, second.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, []domain.KnowledgeEdge{edge}, toSecond)
	allReferences, err := a.Knowledge.ListEdges(ctx, edgeType.Kind, uuid.Nil, uuid.Nil, 0)
	require.NoError(t, err)
	assert.Equal(t, []domain.KnowledgeEdge{edge}, allReferences)
	edgeHistory, err := a.Knowledge.EdgeHistory(ctx, edge.Kind, edge.ID, 0, 0)
	require.NoError(t, err)
	require.Len(t, edgeHistory.Versions, 2)
	assert.Equal(t, int64(2), edgeHistory.Versions[0].Version)
	assert.JSONEq(t, string(changedEdgeData), string(edgeHistory.Versions[0].Data))
	assert.JSONEq(t, string(changedEdgeMetadata), string(edgeHistory.Versions[0].Metadata))
	assert.Equal(t, int64(1), edgeHistory.Versions[1].Version)
	assert.JSONEq(t, string(edgeData), string(edgeHistory.Versions[1].Data))
	assert.JSONEq(t, string(edgeMetadata), string(edgeHistory.Versions[1].Metadata))

	require.ErrorIs(t, a.Knowledge.DeleteNode(ctx, first.Kind, first.ID, nil), domain.ErrConflict)
	require.ErrorIs(t, a.Knowledge.DeleteNode(ctx, second.Kind, second.ID, nil), domain.ErrConflict)
	edgeRevision = edge.Revision
	require.NoError(t, a.Knowledge.DeleteEdge(ctx, edge.Kind, edge.ID, &edgeRevision))
	_, err = a.Knowledge.GetEdge(ctx, edge.Kind, edge.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
	require.NoError(t, a.Knowledge.DeleteNode(ctx, first.Kind, first.ID, nil))
	require.NoError(t, a.Knowledge.DeleteNode(ctx, second.Kind, second.ID, nil))
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
	_, err = provider.DownTo(context.Background(), 7)
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
