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
