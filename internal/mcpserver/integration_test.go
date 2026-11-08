package mcpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/mcpserver"
	"github.com/anas-domesticus/personal-agent/internal/testenv"
)

func TestWorkflowOverHTTP(t *testing.T) {
	t.Parallel()
	env := testenv.NewWith(t, "", testenv.Options{})
	_, err := env.Agents.Create(context.Background(), "worker", "Test worker", domain.AgentVersionInput{
		SystemPrompt: "Reply with structured output.", Model: "test/model",
	})
	require.NoError(t, err)
	session := adminSession(t, env)

	created := callIntegrationTool(t, session, "create_workflow", map[string]any{
		"name": "http-workflow", "description": "Exercise MCP over HTTP",
		"graph": map[string]any{"nodes": []any{map[string]any{"id": "only", "agent": "worker"}}, "edges": []any{}},
	})
	require.False(t, created.IsError)

	started := callIntegrationTool(t, session, "run_workflow", map[string]any{"name": "http-workflow", "wait_seconds": 10})
	require.False(t, started.IsError)
	content, ok := started.StructuredContent.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "succeeded", content["status"])
}

func TestKnowledgeNodesEmbeddingsAndSearchOverHTTP(t *testing.T) {
	t.Parallel()
	env := testenv.NewWith(t, "", testenv.Options{})
	session := consumerSession(t, env)

	tests := []struct {
		kind string
		data map[string]any
	}{
		{kind: "objective", data: map[string]any{"title": "Improve research", "description": "Collect reliable sources", "success_criteria": []any{"Sources are cited"}}},
		{kind: "workflow", data: map[string]any{"title": "research-flow", "description": "Research a topic", "graph": map[string]any{"nodes": []any{map[string]any{"id": "research", "agent": "researcher"}}, "edges": []any{}}}},
		{kind: "claim", data: map[string]any{"title": "Postgres stores vectors", "description": "Pgvector enables semantic search"}},
		{kind: "source", data: map[string]any{"title": "Architecture guide", "description": "System design reference", "uri": "https://example.com/architecture", "content_hash": "sha256:abc", "metadata": map[string]any{"publisher": "example"}}},
		{kind: "agent", data: map[string]any{"title": "researcher", "description": "Find useful sources", "system_prompt": "Research the question", "model": "test/model", "config": map[string]any{}, "notes": ""}},
	}
	ids := make(map[string]string, len(tests))
	for _, tt := range tests {
		created := callIntegrationTool(t, session, "upsert_node", map[string]any{"kind": tt.kind, "data": tt.data})
		require.False(t, created.IsError, tt.kind)
		content := created.StructuredContent.(map[string]any)
		ids[tt.kind] = content["id"].(string)
	}

	require.Eventually(t, func() bool {
		var count int
		err := env.Store.Pool.QueryRow(context.Background(),
			`select count(*) from graph_nodes where embedding_model = $1 and embedding_revision = revision`, testenv.FakeEmbeddingModel).Scan(&count)
		return err == nil && count == len(tests)
	}, 30*time.Second, 100*time.Millisecond, "nodes were not embedded")

	searched := callIntegrationTool(t, session, "search_nodes", map[string]any{"kind": "claim", "query": "semantic vector storage"})
	require.False(t, searched.IsError)
	matches := searched.StructuredContent.(map[string]any)["nodes"].([]any)
	require.Len(t, matches, 1)
	assert.Equal(t, ids["claim"], matches[0].(map[string]any)["id"])

	updated := callIntegrationTool(t, session, "upsert_node", map[string]any{
		"kind": "claim", "id": ids["claim"], "expected_revision": 1,
		"data": map[string]any{"title": "Postgres and pgvector", "description": "Vectors support similarity search"},
	})
	require.False(t, updated.IsError)
	require.Eventually(t, func() bool {
		var revision int64
		err := env.Store.Pool.QueryRow(context.Background(),
			`select embedding_revision from graph_nodes where id = $1`, ids["claim"]).Scan(&revision)
		return err == nil && revision == 2
	}, 30*time.Second, 100*time.Millisecond, "updated node was not embedded")
}

func TestDynamicGraphOverHTTP(t *testing.T) {
	t.Parallel()
	env := testenv.NewWith(t, "", testenv.Options{})
	admin := adminSession(t, env)
	consumer := consumerSession(t, env)

	nodeType := callIntegrationTool(t, admin, "create_node_type", map[string]any{
		"kind": "integration_note",
		"data_schema": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"title": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"}},
			"required":   []any{"title", "description"},
		},
	})
	require.False(t, nodeType.IsError)
	edgeType := callIntegrationTool(t, admin, "create_edge_type", map[string]any{
		"kind": "integration_related",
		"data_schema": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"weight": map[string]any{"type": "number"}},
		},
		"endpoints": []any{map[string]any{"source": "integration_note", "target": "integration_note"}},
	})
	require.False(t, edgeType.IsError)

	first := callIntegrationTool(t, consumer, "upsert_node", map[string]any{
		"kind": "integration_note", "data": map[string]any{"title": "First", "description": "First dynamic note"},
	})
	require.False(t, first.IsError)
	firstID := first.StructuredContent.(map[string]any)["id"].(string)
	second := callIntegrationTool(t, consumer, "upsert_node", map[string]any{
		"kind": "integration_note", "data": map[string]any{"title": "Second", "description": "Second dynamic note"},
	})
	require.False(t, second.IsError)
	secondID := second.StructuredContent.(map[string]any)["id"].(string)

	got := callIntegrationTool(t, consumer, "get_node", map[string]any{"kind": "integration_note", "id": firstID})
	require.False(t, got.IsError)
	assert.Equal(t, "First", got.StructuredContent.(map[string]any)["title"])
	updatedNode := callIntegrationTool(t, consumer, "upsert_node", map[string]any{
		"kind": "integration_note", "id": firstID, "expected_revision": 1,
		"data":     map[string]any{"title": "First updated", "description": "Updated dynamic note"},
		"metadata": map[string]any{"reason": "integration"},
	})
	require.False(t, updatedNode.IsError)
	nodeHistory := callIntegrationTool(t, consumer, "get_node_history", map[string]any{"kind": "integration_note", "id": firstID, "limit": 10})
	require.False(t, nodeHistory.IsError)
	versions := nodeHistory.StructuredContent.(map[string]any)["versions"].([]any)
	require.Len(t, versions, 2)
	assert.Equal(t, map[string]any{"reason": "integration"}, versions[0].(map[string]any)["metadata"])

	createdEdge := callIntegrationTool(t, consumer, "upsert_edge", map[string]any{
		"kind":   "integration_related",
		"source": map[string]any{"id": firstID, "kind": "integration_note"},
		"target": map[string]any{"id": secondID, "kind": "integration_note"},
		"data":   map[string]any{"weight": 0.5}, "metadata": map[string]any{"reason": "initial"},
	})
	require.False(t, createdEdge.IsError)
	edgeID := createdEdge.StructuredContent.(map[string]any)["id"].(string)
	gotEdge := callIntegrationTool(t, consumer, "get_edge", map[string]any{"kind": "integration_related", "id": edgeID})
	require.False(t, gotEdge.IsError)
	assert.Equal(t, firstID, gotEdge.StructuredContent.(map[string]any)["source_id"])
	listedEdges := callIntegrationTool(t, consumer, "list_edges", map[string]any{"kind": "integration_related", "source_id": firstID})
	require.False(t, listedEdges.IsError)
	assert.Len(t, listedEdges.StructuredContent.(map[string]any)["edges"], 1)
	updatedEdge := callIntegrationTool(t, consumer, "upsert_edge", map[string]any{
		"kind":   "integration_related",
		"source": map[string]any{"id": firstID, "kind": "integration_note"},
		"target": map[string]any{"id": secondID, "kind": "integration_note"},
		"data":   map[string]any{"weight": 0.9}, "metadata": map[string]any{"reason": "updated"}, "expected_revision": 1,
	})
	require.False(t, updatedEdge.IsError)
	edgeHistory := callIntegrationTool(t, consumer, "get_edge_history", map[string]any{"kind": "integration_related", "id": edgeID, "limit": 10})
	require.False(t, edgeHistory.IsError)
	assert.Len(t, edgeHistory.StructuredContent.(map[string]any)["versions"], 2)

	deletedEdge := callIntegrationTool(t, consumer, "delete_edge", map[string]any{"kind": "integration_related", "id": edgeID, "expected_revision": 2})
	assert.False(t, deletedEdge.IsError)
	deletedNode := callIntegrationTool(t, consumer, "delete_node", map[string]any{"kind": "integration_note", "id": firstID, "expected_revision": 2})
	assert.False(t, deletedNode.IsError)
}

func adminSession(t *testing.T, env *testenv.Env) *mcp.ClientSession {
	t.Helper()
	_, handler := mcpserver.NewAdmin(mcpserver.AdminDeps{
		Agents: env.Agents, Workflows: env.Workflows, Runs: env.Runs, Tools: env.Tools,
		Schema: &env.Knowledge, SchemaAdmin: &env.Knowledge,
	})
	return httpSession(t, handler)
}

func consumerSession(t *testing.T, env *testenv.Env) *mcp.ClientSession {
	t.Helper()
	_, handler := mcpserver.NewConsumer(mcpserver.ConsumerDeps{
		Nodes: &env.Knowledge, Edges: &env.Knowledge, Schema: &env.Knowledge,
	})
	return httpSession(t, handler)
}

func httpSession(t *testing.T, handler http.Handler) *mcp.ClientSession {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: server.URL + "/mcp", HTTPClient: server.Client(), DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callIntegrationTool(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	require.NoError(t, err)
	return result
}
