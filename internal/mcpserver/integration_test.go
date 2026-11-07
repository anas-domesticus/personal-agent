package mcpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
		Agents: env.Agents, Workflows: env.Workflows, Runs: env.Runs, Tools: env.Tools,
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
