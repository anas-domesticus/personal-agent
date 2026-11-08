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

func TestAdminServer(t *testing.T) {
	t.Parallel()
	agents := NewMockAgents(t)
	workflows := NewMockWorkflows(t)
	runs := NewMockRuns(t)
	tools := NewMockTools(t)
	srv, _ := NewAdmin(AdminDeps{
		Agents: agents, Workflows: workflows, Runs: runs, Tools: tools,
		Schema: NewMockSchema(t), SchemaAdmin: NewMockSchemaAdmin(t),
	})
	session := connect(t, srv)

	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, listed.Tools, 25)
	names := make(map[string]bool, len(listed.Tools))
	for _, tool := range listed.Tools {
		names[tool.Name] = true
		assert.NotNil(t, tool.InputSchema, tool.Name)
		assert.NotNil(t, tool.OutputSchema, tool.Name)
		assert.False(t, *tool.Annotations.OpenWorldHint, tool.Name)
	}
	assert.Contains(t, names, "create_node_type")
	assert.Contains(t, names, "create_edge_type")
	assert.Contains(t, names, "get_node_type")
	assert.NotContains(t, names, "get_node")
	assert.NotContains(t, names, "upsert_edge")
	assert.NotContains(t, names, "add_supported_by")

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

func TestSearchWorkflowsBoundsLimit(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		arguments map[string]any
		want      int32
	}{
		{name: "omitted", arguments: map[string]any{"query": "daily"}, want: 10},
		{name: "excessive", arguments: map[string]any{"query": "daily", "limit": 1000}, want: 100},
		{name: "negative", arguments: map[string]any{"query": "daily", "limit": -1}, want: 10},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			workflows := NewMockWorkflows(t)
			srv, _ := NewAdmin(AdminDeps{
				Agents: NewMockAgents(t), Workflows: workflows, Runs: NewMockRuns(t), Tools: NewMockTools(t),
				Schema: NewMockSchema(t), SchemaAdmin: NewMockSchemaAdmin(t),
			})
			session := connect(t, srv)
			workflows.EXPECT().Search(mock.Anything, "daily", tt.want).Return([]domain.WorkflowMatch{}, nil).Once()
			result := callTool(t, session, "search_workflows", tt.arguments)
			require.False(t, result.IsError)
		})
	}
}

func TestListRunsBoundsLimit(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		arguments map[string]any
		want      int32
	}{
		{name: "omitted", arguments: map[string]any{}, want: 10},
		{name: "excessive", arguments: map[string]any{"limit": 1000}, want: 100},
		{name: "negative", arguments: map[string]any{"limit": -1}, want: 10},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runs := NewMockRuns(t)
			srv, _ := NewAdmin(AdminDeps{
				Agents: NewMockAgents(t), Workflows: NewMockWorkflows(t), Runs: runs, Tools: NewMockTools(t),
				Schema: NewMockSchema(t), SchemaAdmin: NewMockSchemaAdmin(t),
			})
			session := connect(t, srv)
			runs.EXPECT().List(mock.Anything, "", tt.want).Return([]domain.Run{}, nil).Once()
			result := callTool(t, session, "list_runs", tt.arguments)
			require.False(t, result.IsError)
		})
	}
}

func TestConsumerServerSurface(t *testing.T) {
	t.Parallel()
	srv, _ := NewConsumer(ConsumerDeps{Nodes: NewMockNodes(t), Edges: NewMockEdges(t), Schema: NewMockSchema(t)})
	session := connect(t, srv)
	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, listed.Tools, 15)
	expected := []string{
		"get_node", "list_nodes", "upsert_node", "delete_node", "search_nodes", "get_node_history",
		"get_edge", "list_edges", "upsert_edge", "delete_edge", "get_edge_history",
		"get_node_type", "list_node_types", "get_edge_type", "list_edge_types",
	}
	names := make(map[string]bool, len(listed.Tools))
	for _, tool := range listed.Tools {
		names[tool.Name] = true
		assert.NotNil(t, tool.InputSchema, tool.Name)
		assert.NotNil(t, tool.OutputSchema, tool.Name)
		assert.False(t, *tool.Annotations.OpenWorldHint, tool.Name)
	}
	for _, name := range expected {
		assert.Contains(t, names, name)
	}
	assert.NotContains(t, names, "create_node_type")
	assert.NotContains(t, names, "create_edge_type")
	assert.NotContains(t, names, "list_agents")
	assert.NotContains(t, names, "search_edges")
	assert.NotContains(t, names, "add_supported_by")
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
