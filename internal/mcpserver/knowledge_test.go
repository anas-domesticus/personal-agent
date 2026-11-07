package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
)

func TestKnowledgeTools(t *testing.T) {
	t.Parallel()
	knowledge := NewMockKnowledge(t)
	srv, _ := New(Deps{
		Agents: NewMockAgents(t), Workflows: NewMockWorkflows(t), Runs: NewMockRuns(t), Knowledge: knowledge, Tools: NewMockTools(t),
	})
	session := connect(t, srv)
	id := uuid.New()
	revision := int64(2)
	node := domain.KnowledgeNode{ID: id, Kind: "objective", Revision: revision, Title: "Grow herbs", Description: "Keep the kitchen supplied"}
	knowledge.EXPECT().Upsert(mock.Anything, "objective", id, json.RawMessage(`{"description":"Keep the kitchen supplied","title":"Grow herbs"}`), &revision).
		Return(node, nil).Once()
	upserted, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "upsert_knowledge_node",
		Arguments: map[string]any{
			"kind": "objective", "id": id.String(), "expected_revision": revision,
			"data": map[string]any{"title": "Grow herbs", "description": "Keep the kitchen supplied"},
		},
	})
	require.NoError(t, err)
	require.False(t, upserted.IsError)
	assert.Equal(t, "Grow herbs", structured(t, upserted)["title"])

	knowledge.EXPECT().Search(mock.Anything, "objective", "gardening", int32(10)).Return([]domain.KnowledgeNodeMatch{{KnowledgeNode: node, Score: 0.9}}, nil).Once()
	searched, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_knowledge_nodes", Arguments: map[string]any{"kind": "objective", "query": "gardening"},
	})
	require.NoError(t, err)
	require.False(t, searched.IsError)
	matches := structured(t, searched)["nodes"].([]any)
	require.Len(t, matches, 1)
	assert.Equal(t, id.String(), matches[0].(map[string]any)["id"])

	invalid, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "upsert_knowledge_node", Arguments: map[string]any{"kind": "claim", "expected_revision": 1, "data": map[string]any{}},
	})
	require.NoError(t, err)
	assert.True(t, invalid.IsError)
}

func TestSupportedByTools(t *testing.T) {
	t.Parallel()
	knowledge := NewMockKnowledge(t)
	srv, _ := New(Deps{
		Agents: NewMockAgents(t), Workflows: NewMockWorkflows(t), Runs: NewMockRuns(t), Knowledge: knowledge, Tools: NewMockTools(t),
	})
	session := connect(t, srv)
	claimID, sourceID, edgeID := uuid.New(), uuid.New(), uuid.New()
	edge := domain.KnowledgeEdge{
		ID: edgeID, Kind: "supported_by", SourceID: claimID, SourceKind: "claim",
		TargetID: sourceID, TargetKind: "source", Data: json.RawMessage(`{}`), Revision: 1,
	}
	knowledge.EXPECT().AddSupportedBy(mock.Anything, claimID, sourceID).Return(edge, nil).Once()
	added, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "add_supported_by", Arguments: map[string]any{"claim_id": claimID.String(), "source_id": sourceID.String()},
	})
	require.NoError(t, err)
	require.False(t, added.IsError)
	content := structured(t, added)
	assert.Equal(t, edgeID.String(), content["id"])
	assert.Equal(t, claimID.String(), content["source_id"])
	assert.Equal(t, sourceID.String(), content["target_id"])
	assert.Equal(t, map[string]any{}, content["data"])

	knowledge.EXPECT().ListSupportedBy(mock.Anything, claimID, uuid.Nil).Return([]domain.KnowledgeEdge{edge}, nil).Once()
	listed, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_supported_by", Arguments: map[string]any{"claim_id": claimID.String()},
	})
	require.NoError(t, err)
	require.False(t, listed.IsError)
	edges := structured(t, listed)["edges"].([]any)
	require.Len(t, edges, 1)
	assert.Equal(t, edgeID.String(), edges[0].(map[string]any)["id"])

	knowledge.EXPECT().DeleteSupportedBy(mock.Anything, edgeID).Return(nil).Once()
	deleted, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "delete_supported_by", Arguments: map[string]any{"edge_id": edgeID.String()},
	})
	require.NoError(t, err)
	assert.False(t, deleted.IsError)
}

func TestSupportedByToolsRejectInvalidInput(t *testing.T) {
	t.Parallel()
	knowledge := NewMockKnowledge(t)
	srv, _ := New(Deps{
		Agents: NewMockAgents(t), Workflows: NewMockWorkflows(t), Runs: NewMockRuns(t), Knowledge: knowledge, Tools: NewMockTools(t),
	})
	session := connect(t, srv)
	claimID, sourceID := uuid.New(), uuid.New()
	knowledge.EXPECT().ListSupportedBy(mock.Anything, uuid.Nil, uuid.Nil).Return(nil, errors.New("exactly one selector required")).Once()
	knowledge.EXPECT().ListSupportedBy(mock.Anything, claimID, sourceID).Return(nil, errors.New("exactly one selector required")).Once()

	tests := []struct {
		name      string
		tool      string
		arguments map[string]any
	}{
		{name: "add claim UUID", tool: "add_supported_by", arguments: map[string]any{"claim_id": "bad", "source_id": sourceID.String()}},
		{name: "add source UUID", tool: "add_supported_by", arguments: map[string]any{"claim_id": claimID.String(), "source_id": "bad"}},
		{name: "delete edge UUID", tool: "delete_supported_by", arguments: map[string]any{"edge_id": "bad"}},
		{name: "list claim UUID", tool: "list_supported_by", arguments: map[string]any{"claim_id": "bad"}},
		{name: "list source UUID", tool: "list_supported_by", arguments: map[string]any{"source_id": "bad"}},
		{name: "list without selector", tool: "list_supported_by", arguments: map[string]any{}},
		{name: "list with both selectors", tool: "list_supported_by", arguments: map[string]any{"claim_id": claimID.String(), "source_id": sourceID.String()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tt.tool, Arguments: tt.arguments})
			require.NoError(t, err)
			assert.True(t, result.IsError)
		})
	}
}
