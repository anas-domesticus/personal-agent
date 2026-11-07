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
	_, handler := mcpserver.New(mcpserver.Deps{
		Agents: env.Agents, Workflows: env.Workflows, Runs: env.Runs, Knowledge: &env.Knowledge, Tools: env.Tools,
	})
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp", HTTPClient: httpServer.Client(), DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	created, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_workflow",
		Arguments: map[string]any{
			"name": "http-workflow", "description": "Exercise MCP over HTTP",
			"graph": map[string]any{"nodes": []any{map[string]any{"id": "only", "agent": "worker"}}, "edges": []any{}},
		},
	})
	require.NoError(t, err)
	require.False(t, created.IsError)

	started, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "run_workflow", Arguments: map[string]any{"name": "http-workflow", "wait_seconds": 10},
	})
	require.NoError(t, err)
	require.False(t, started.IsError)
	content, ok := started.StructuredContent.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "succeeded", content["status"])
}

func TestKnowledgeNodesEmbeddingsAndSearchOverHTTP(t *testing.T) {
	t.Parallel()
	env := testenv.NewWith(t, "", testenv.Options{})
	_, handler := mcpserver.New(mcpserver.Deps{
		Agents: env.Agents, Workflows: env.Workflows, Runs: env.Runs, Knowledge: &env.Knowledge, Tools: env.Tools,
	})
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "knowledge-integration-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp", HTTPClient: httpServer.Client(), DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

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
		created, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name: "upsert_knowledge_node", Arguments: map[string]any{"kind": tt.kind, "data": tt.data},
		})
		require.NoError(t, err)
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

	searched, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_knowledge_nodes", Arguments: map[string]any{"kind": "claim", "query": "semantic vector storage"},
	})
	require.NoError(t, err)
	require.False(t, searched.IsError)
	matches := searched.StructuredContent.(map[string]any)["nodes"].([]any)
	require.Len(t, matches, 1)
	assert.Equal(t, ids["claim"], matches[0].(map[string]any)["id"])

	updated, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "upsert_knowledge_node", Arguments: map[string]any{
			"kind": "claim", "id": ids["claim"], "expected_revision": 1,
			"data": map[string]any{"title": "Postgres and pgvector", "description": "Vectors support similarity search"},
		},
	})
	require.NoError(t, err)
	require.False(t, updated.IsError)
	require.Eventually(t, func() bool {
		var revision int64
		err := env.Store.Pool.QueryRow(context.Background(),
			`select embedding_revision from graph_nodes where id = $1`, ids["claim"]).Scan(&revision)
		return err == nil && revision == 2
	}, 30*time.Second, 100*time.Millisecond, "updated node was not embedded")
}
