package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
)

func consumerSession(t *testing.T, nodes Nodes, edges Edges, schema Schema) *mcp.ClientSession {
	t.Helper()
	srv, _ := NewConsumer(ConsumerDeps{Nodes: nodes, Edges: edges, Schema: schema})
	return connect(t, srv)
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	require.NoError(t, err)
	return result
}

func TestNodeTools(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	revision := int64(3)
	node := domain.KnowledgeNode{ID: id, Kind: "note", Revision: revision, Data: json.RawMessage(`{"title":"Note"}`)}
	nodes := NewMockNodes(t)
	session := consumerSession(t, nodes, NewMockEdges(t), NewMockSchema(t))

	nodes.EXPECT().GetNode(mock.Anything, "note", id).Return(node, nil).Once()
	got := callTool(t, session, "get_node", map[string]any{"kind": "note", "id": id.String()})
	require.False(t, got.IsError)
	assert.Equal(t, id.String(), structured(t, got)["id"])

	nodes.EXPECT().ListNodes(mock.Anything, "note", int32(1000)).Return([]domain.KnowledgeNode{node}, nil).Once()
	listed := callTool(t, session, "list_nodes", map[string]any{"kind": "note", "limit": 1000})
	require.False(t, listed.IsError)
	assert.Len(t, structured(t, listed)["nodes"], 1)

	nodes.EXPECT().UpsertNode(
		mock.Anything, "note", id,
		json.RawMessage(`{"title":"Note"}`), json.RawMessage(`{"actor":"test"}`), &revision,
	).Return(node, nil).Once()
	upserted := callTool(t, session, "upsert_node", map[string]any{
		"kind": "note", "id": id.String(), "data": map[string]any{"title": "Note"},
		"metadata": map[string]any{"actor": "test"}, "expected_revision": revision,
	})
	require.False(t, upserted.IsError)
	assert.Equal(t, "note", structured(t, upserted)["kind"])

	nodes.EXPECT().DeleteNode(mock.Anything, "note", id, &revision).Return(nil).Once()
	deleted := callTool(t, session, "delete_node", map[string]any{"kind": "note", "id": id.String(), "expected_revision": revision})
	assert.False(t, deleted.IsError)

	nodes.EXPECT().SearchNodes(mock.Anything, "note", "garden", int32(250)).
		Return([]domain.KnowledgeNodeMatch{{KnowledgeNode: node, Score: 0.8}}, nil).Once()
	searched := callTool(t, session, "search_nodes", map[string]any{"kind": "note", "query": "garden", "limit": 250})
	require.False(t, searched.IsError)
	assert.Len(t, structured(t, searched)["nodes"], 1)

	historyResult := domain.KnowledgeNodeHistory{
		Versions: []domain.KnowledgeNodeVersion{{NodeID: id, Version: 3}}, NextBefore: 3,
	}
	nodes.EXPECT().NodeHistory(mock.Anything, "note", id, int64(4), 250).Return(historyResult, nil).Once()
	history := callTool(t, session, "get_node_history", map[string]any{"kind": "note", "id": id.String(), "before": 4, "limit": 250})
	require.False(t, history.IsError)
	assert.Len(t, structured(t, history)["versions"], 1)
	assert.InDelta(t, 3, structured(t, history)["next_before"], 0)
}

func TestNodeToolValidation(t *testing.T) {
	t.Parallel()
	session := consumerSession(t, NewMockNodes(t), NewMockEdges(t), NewMockSchema(t))
	id := uuid.New().String()
	tests := []struct {
		name      string
		tool      string
		arguments map[string]any
	}{
		{name: "get malformed UUID", tool: "get_node", arguments: map[string]any{"kind": "note", "id": "bad"}},
		{name: "get zero UUID", tool: "get_node", arguments: map[string]any{"kind": "note", "id": uuid.Nil.String()}},
		{name: "upsert malformed UUID", tool: "upsert_node", arguments: map[string]any{"kind": "note", "id": "bad", "data": map[string]any{}}},
		{name: "upsert zero UUID", tool: "upsert_node", arguments: map[string]any{"kind": "note", "id": uuid.Nil.String(), "data": map[string]any{}}},
		{name: "new node revision", tool: "upsert_node", arguments: map[string]any{"kind": "note", "expected_revision": 1, "data": map[string]any{}}},
		{name: "delete malformed UUID", tool: "delete_node", arguments: map[string]any{"kind": "note", "id": "bad"}},
		{name: "delete zero UUID", tool: "delete_node", arguments: map[string]any{"kind": "note", "id": uuid.Nil.String()}},
		{name: "history malformed UUID", tool: "get_node_history", arguments: map[string]any{"kind": "note", "id": "bad"}},
		{name: "history zero UUID", tool: "get_node_history", arguments: map[string]any{"kind": "note", "id": uuid.Nil.String()}},
		{name: "history negative before", tool: "get_node_history", arguments: map[string]any{"kind": "note", "id": id, "before": -1}},
		{name: "history negative limit", tool: "get_node_history", arguments: map[string]any{"kind": "note", "id": id, "limit": -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := callTool(t, session, tt.tool, tt.arguments)
			assert.True(t, result.IsError)
		})
	}
}

func TestEdgeTools(t *testing.T) {
	t.Parallel()
	id, sourceID, targetID := uuid.New(), uuid.New(), uuid.New()
	revision := int64(2)
	edge := domain.KnowledgeEdge{ID: id, Kind: "related", SourceID: sourceID, SourceKind: "note", TargetID: targetID, TargetKind: "note", Revision: revision}
	edges := NewMockEdges(t)
	session := consumerSession(t, NewMockNodes(t), edges, NewMockSchema(t))

	edges.EXPECT().GetEdge(mock.Anything, "related", id).Return(edge, nil).Once()
	got := callTool(t, session, "get_edge", map[string]any{"kind": "related", "id": id.String()})
	require.False(t, got.IsError)
	assert.Equal(t, id.String(), structured(t, got)["id"])

	edges.EXPECT().ListEdges(mock.Anything, "related", sourceID, uuid.Nil, int32(1000)).Return([]domain.KnowledgeEdge{edge}, nil).Once()
	listed := callTool(t, session, "list_edges", map[string]any{"kind": "related", "source_id": sourceID.String(), "limit": 1000})
	require.False(t, listed.IsError)
	assert.Len(t, structured(t, listed)["edges"], 1)

	source := domain.KnowledgeRef{ID: sourceID, Kind: "note"}
	target := domain.KnowledgeRef{ID: targetID, Kind: "note"}
	edges.EXPECT().UpsertEdge(
		mock.Anything, "related", source, target,
		json.RawMessage(`{"strength":0.8}`), json.RawMessage(`{"actor":"test"}`), &revision,
	).Return(edge, nil).Once()
	upserted := callTool(t, session, "upsert_edge", map[string]any{
		"kind": "related", "source": map[string]any{"id": sourceID.String(), "kind": "note"},
		"target": map[string]any{"id": targetID.String(), "kind": "note"},
		"data":   map[string]any{"strength": 0.8}, "metadata": map[string]any{"actor": "test"}, "expected_revision": revision,
	})
	require.False(t, upserted.IsError)
	assert.Equal(t, sourceID.String(), structured(t, upserted)["source_id"])

	edges.EXPECT().DeleteEdge(mock.Anything, "related", id, &revision).Return(nil).Once()
	deleted := callTool(t, session, "delete_edge", map[string]any{"kind": "related", "id": id.String(), "expected_revision": revision})
	assert.False(t, deleted.IsError)

	historyResult := domain.KnowledgeEdgeHistory{
		Versions: []domain.KnowledgeEdgeVersion{{EdgeID: id, Version: 2}}, NextBefore: 2,
	}
	edges.EXPECT().EdgeHistory(mock.Anything, "related", id, int64(3), 500).Return(historyResult, nil).Once()
	history := callTool(t, session, "get_edge_history", map[string]any{"kind": "related", "id": id.String(), "before": 3, "limit": 500})
	require.False(t, history.IsError)
	assert.Len(t, structured(t, history)["versions"], 1)
}

func TestEdgeToolValidation(t *testing.T) {
	t.Parallel()
	session := consumerSession(t, NewMockNodes(t), NewMockEdges(t), NewMockSchema(t))
	id, sourceID, targetID := uuid.New().String(), uuid.New().String(), uuid.New().String()
	validSource := map[string]any{"id": sourceID, "kind": "note"}
	validTarget := map[string]any{"id": targetID, "kind": "note"}
	tests := []struct {
		name      string
		tool      string
		arguments map[string]any
	}{
		{name: "get malformed UUID", tool: "get_edge", arguments: map[string]any{"kind": "related", "id": "bad"}},
		{name: "get zero UUID", tool: "get_edge", arguments: map[string]any{"kind": "related", "id": uuid.Nil.String()}},
		{name: "list both selectors", tool: "list_edges", arguments: map[string]any{"source_id": sourceID, "target_id": targetID}},
		{name: "list malformed source", tool: "list_edges", arguments: map[string]any{"source_id": "bad"}},
		{name: "list zero source", tool: "list_edges", arguments: map[string]any{"source_id": uuid.Nil.String()}},
		{name: "list malformed target", tool: "list_edges", arguments: map[string]any{"target_id": "bad"}},
		{name: "list zero target", tool: "list_edges", arguments: map[string]any{"target_id": uuid.Nil.String()}},
		{name: "upsert malformed source", tool: "upsert_edge", arguments: map[string]any{"kind": "related", "source": map[string]any{"id": "bad", "kind": "note"}, "target": validTarget, "data": map[string]any{}}},
		{name: "upsert zero source", tool: "upsert_edge", arguments: map[string]any{"kind": "related", "source": map[string]any{"id": uuid.Nil.String(), "kind": "note"}, "target": validTarget, "data": map[string]any{}}},
		{name: "upsert malformed target", tool: "upsert_edge", arguments: map[string]any{"kind": "related", "source": validSource, "target": map[string]any{"id": "bad", "kind": "note"}, "data": map[string]any{}}},
		{name: "upsert zero target", tool: "upsert_edge", arguments: map[string]any{"kind": "related", "source": validSource, "target": map[string]any{"id": uuid.Nil.String(), "kind": "note"}, "data": map[string]any{}}},
		{name: "delete malformed UUID", tool: "delete_edge", arguments: map[string]any{"kind": "related", "id": "bad"}},
		{name: "delete zero UUID", tool: "delete_edge", arguments: map[string]any{"kind": "related", "id": uuid.Nil.String()}},
		{name: "history malformed UUID", tool: "get_edge_history", arguments: map[string]any{"kind": "related", "id": "bad"}},
		{name: "history zero UUID", tool: "get_edge_history", arguments: map[string]any{"kind": "related", "id": uuid.Nil.String()}},
		{name: "history negative before", tool: "get_edge_history", arguments: map[string]any{"kind": "related", "id": id, "before": -1}},
		{name: "history negative limit", tool: "get_edge_history", arguments: map[string]any{"kind": "related", "id": id, "limit": -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := callTool(t, session, tt.tool, tt.arguments)
			assert.True(t, result.IsError)
		})
	}
}

func TestSchemaDiscoveryTools(t *testing.T) {
	t.Parallel()
	nodeType := domain.KnowledgeNodeType{Kind: "note", DataSchema: json.RawMessage(`{"type":"object"}`)}
	edgeType := domain.KnowledgeEdgeType{Kind: "related", DataSchema: json.RawMessage(`{"type":"object"}`), Endpoints: []domain.KnowledgeEndpoint{{Source: "note", Target: "note"}}}
	schema := NewMockSchema(t)
	schema.EXPECT().GetNodeType(mock.Anything, "note").Return(nodeType, nil).Once()
	schema.EXPECT().ListNodeTypes(mock.Anything).Return([]domain.KnowledgeNodeType{nodeType}, nil).Once()
	schema.EXPECT().GetEdgeType(mock.Anything, "related").Return(edgeType, nil).Once()
	schema.EXPECT().ListEdgeTypes(mock.Anything).Return([]domain.KnowledgeEdgeType{edgeType}, nil).Once()
	session := consumerSession(t, NewMockNodes(t), NewMockEdges(t), schema)
	for _, tt := range []struct {
		tool      string
		arguments map[string]any
		field     string
	}{
		{tool: "get_node_type", arguments: map[string]any{"kind": "note"}, field: "kind"},
		{tool: "list_node_types", arguments: map[string]any{}, field: "node_types"},
		{tool: "get_edge_type", arguments: map[string]any{"kind": "related"}, field: "kind"},
		{tool: "list_edge_types", arguments: map[string]any{}, field: "edge_types"},
	} {
		result := callTool(t, session, tt.tool, tt.arguments)
		require.False(t, result.IsError, tt.tool)
		assert.Contains(t, structured(t, result), tt.field)
	}
}

func TestSchemaAdminTools(t *testing.T) {
	t.Parallel()
	schema := NewMockSchemaAdmin(t)
	srv, _ := NewAdmin(AdminDeps{
		Agents: NewMockAgents(t), Workflows: NewMockWorkflows(t), Runs: NewMockRuns(t), Tools: NewMockTools(t),
		Schema: NewMockSchema(t), SchemaAdmin: schema,
	})
	session := connect(t, srv)
	nodeSchema := map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}}}
	nodeRaw := json.RawMessage(`{"properties":{"title":{"type":"string"}},"type":"object"}`)
	schema.EXPECT().CreateNodeType(mock.Anything, "note", nodeRaw).
		Return(domain.KnowledgeNodeType{Kind: "note", DataSchema: nodeRaw}, nil).Once()
	createdNode := callTool(t, session, "create_node_type", map[string]any{"kind": "note", "data_schema": nodeSchema})
	require.False(t, createdNode.IsError)
	assert.Equal(t, "note", structured(t, createdNode)["kind"])

	endpoints := []domain.KnowledgeEndpoint{{Source: "note", Target: "note"}}
	edgeRaw := json.RawMessage(`{"type":"object"}`)
	schema.EXPECT().CreateEdgeType(mock.Anything, "related", edgeRaw, endpoints).
		Return(domain.KnowledgeEdgeType{Kind: "related", DataSchema: edgeRaw, Endpoints: endpoints}, nil).Once()
	createdEdge := callTool(t, session, "create_edge_type", map[string]any{
		"kind": "related", "data_schema": map[string]any{"type": "object"},
		"endpoints": []any{map[string]any{"source": "note", "target": "note"}},
	})
	require.False(t, createdEdge.IsError)
	assert.Equal(t, "related", structured(t, createdEdge)["kind"])
}
