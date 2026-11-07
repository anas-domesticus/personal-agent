package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/workflow"
)

func TestServer(t *testing.T) {
	t.Parallel()
	agents := NewMockAgents(t)
	workflows := NewMockWorkflows(t)
	runs := NewMockRuns(t)
	tools := NewMockTools(t)
	srv, _ := New(Deps{Agents: agents, Workflows: workflows, Runs: runs, Tools: tools})
	session := connect(t, srv)

	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, listed.Tools, 19)
	for _, tool := range listed.Tools {
		assert.NotNil(t, tool.InputSchema, tool.Name)
		assert.NotNil(t, tool.OutputSchema, tool.Name)
		assert.False(t, *tool.Annotations.OpenWorldHint, tool.Name)
	}

	graph := workflow.Graph{Nodes: []workflow.Node{{ID: "only", Agent: "worker"}}, Edges: []workflow.Edge{}}
	workflows.EXPECT().Create(mock.Anything, "daily", "Daily work", graph, "first").
		Return(domain.WorkflowVersion{Version: 1, Graph: graph}, nil).Once()
	created, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_workflow",
		Arguments: map[string]any{
			"name": "daily", "description": "Daily work", "notes": "first",
			"graph": map[string]any{"nodes": []any{map[string]any{"id": "only", "agent": "worker"}}, "edges": []any{}},
		},
	})
	require.NoError(t, err)
	require.False(t, created.IsError)
	assert.InEpsilon(t, 1, structured(t, created)["version"], 0)

	workflows.EXPECT().Search(mock.Anything, "daily", int32(100)).Return([]domain.WorkflowMatch{}, nil).Once()
	searched, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_workflows", Arguments: map[string]any{"query": "daily", "limit": 1000},
	})
	require.NoError(t, err)
	require.False(t, searched.IsError)
}

func connect(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(context.Background(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	return clientSession
}

func structured(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}
