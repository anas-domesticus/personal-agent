package knowledge_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/knowledge"
)

const (
	getNodeTool = iota
	listNodesTool
	upsertNodeTool
	deleteNodeTool
	searchNodesTool
	getNodeHistoryTool
)

var testNodeID = uuid.MustParse("11111111-1111-4111-8111-111111111111")

func nodeTool(t *testing.T, nodes *knowledge.MockNodes, index int) tool.InvokableTool {
	t.Helper()
	tools, err := knowledge.NodeTools(nodes)
	require.NoError(t, err)
	return tools[index]
}

func TestGetNode(t *testing.T) {
	t.Parallel()

	node := domain.KnowledgeNode{ID: testNodeID, Kind: "objective", Revision: 2, Title: "Grow herbs", Data: json.RawMessage(`{"title":"Grow herbs"}`)}
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockNodes)
		want    domain.KnowledgeNode
		wantErr string
	}{
		{
			name: "current node",
			args: map[string]any{"kind": "objective", "id": testNodeID.String()},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().GetNode(mock.Anything, "objective", testNodeID).Return(node, nil).Once()
			},
			want: node,
		},
		{
			name: "service error",
			args: map[string]any{"kind": "objective", "id": testNodeID.String()},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().GetNode(mock.Anything, "objective", testNodeID).Return(domain.KnowledgeNode{}, errors.New("node unavailable")).Once()
			},
			wantErr: "node unavailable",
		},
		{name: "invalid UUID", args: map[string]any{"kind": "objective", "id": "bad"}, wantErr: "parse knowledge node id"},
		{name: "zero UUID", args: map[string]any{"kind": "objective", "id": uuid.Nil.String()}, wantErr: "knowledge node id must not be zero"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			nodes := knowledge.NewMockNodes(t)
			if tt.setup != nil {
				tt.setup(nodes)
			}
			got := run[domain.KnowledgeNode](t, nodeTool(t, nodes, getNodeTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data)
		})
	}
}

type nodesOutput struct {
	Nodes []domain.KnowledgeNode `json:"nodes"`
}

func TestListNodes(t *testing.T) {
	t.Parallel()

	node := domain.KnowledgeNode{ID: testNodeID, Kind: "claim", Title: "Herbs are useful", Data: json.RawMessage(`{}`)}
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockNodes)
		want    []domain.KnowledgeNode
		wantErr string
	}{
		{
			name: "defaults",
			args: map[string]any{},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().ListNodes(mock.Anything, "", int32(0)).Return([]domain.KnowledgeNode{node}, nil).Once()
			},
			want: []domain.KnowledgeNode{node},
		},
		{
			name: "kind and limit forwarded without local clamping",
			args: map[string]any{"kind": "claim", "limit": 1000},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().ListNodes(mock.Anything, "claim", int32(1000)).Return([]domain.KnowledgeNode{}, nil).Once()
			},
			want: []domain.KnowledgeNode{},
		},
		{
			name: "service error",
			args: map[string]any{"kind": "claim"},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().ListNodes(mock.Anything, "claim", int32(0)).Return(nil, errors.New("list failed")).Once()
			},
			wantErr: "list failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			nodes := knowledge.NewMockNodes(t)
			tt.setup(nodes)
			got := run[nodesOutput](t, nodeTool(t, nodes, listNodesTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data.Nodes)
		})
	}
}

func TestUpsertNode(t *testing.T) {
	t.Parallel()

	revision := int64(2)
	node := domain.KnowledgeNode{ID: testNodeID, Kind: "objective", Revision: revision, Title: "Grow herbs", Data: json.RawMessage(`{"title":"Grow herbs"}`)}
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockNodes)
		want    domain.KnowledgeNode
		wantErr string
	}{
		{
			name: "create without id revision or metadata",
			args: map[string]any{"kind": "objective", "data": map[string]any{"title": "Grow herbs"}},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().UpsertNode(mock.Anything, "objective", uuid.Nil, json.RawMessage(`{"title":"Grow herbs"}`), json.RawMessage(nil), (*int64)(nil)).Return(node, nil).Once()
			},
			want: node,
		},
		{
			name: "update with revision and metadata",
			args: map[string]any{"kind": "objective", "id": testNodeID.String(), "expected_revision": revision, "data": map[string]any{"title": "Grow herbs"}, "metadata": map[string]any{"actor": "test"}},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().UpsertNode(mock.Anything, "objective", testNodeID, json.RawMessage(`{"title":"Grow herbs"}`), json.RawMessage(`{"actor":"test"}`), &revision).Return(node, nil).Once()
			},
			want: node,
		},
		{name: "invalid UUID", args: map[string]any{"kind": "objective", "id": "bad", "data": map[string]any{}}, wantErr: "parse knowledge node id"},
		{name: "zero UUID", args: map[string]any{"kind": "objective", "id": uuid.Nil.String(), "data": map[string]any{}}, wantErr: "knowledge node id must not be zero"},
		{name: "create with expected revision", args: map[string]any{"kind": "objective", "expected_revision": revision, "data": map[string]any{}}, wantErr: "new knowledge node has an expected revision: conflict"},
		{
			name: "service error",
			args: map[string]any{"kind": "objective", "data": map[string]any{}},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().UpsertNode(mock.Anything, "objective", uuid.Nil, json.RawMessage(`{}`), json.RawMessage(nil), (*int64)(nil)).Return(domain.KnowledgeNode{}, errors.New("write failed")).Once()
			},
			wantErr: "write failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			nodes := knowledge.NewMockNodes(t)
			if tt.setup != nil {
				tt.setup(nodes)
			}
			got := run[domain.KnowledgeNode](t, nodeTool(t, nodes, upsertNodeTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data)
		})
	}
}

func TestDeleteNode(t *testing.T) {
	t.Parallel()

	revision := int64(3)
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockNodes)
		wantErr string
	}{
		{
			name: "delete with optimistic concurrency",
			args: map[string]any{"kind": "objective", "id": testNodeID.String(), "expected_revision": revision},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().DeleteNode(mock.Anything, "objective", testNodeID, &revision).Return(nil).Once()
			},
		},
		{
			name: "delete without expected revision",
			args: map[string]any{"kind": "objective", "id": testNodeID.String()},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().DeleteNode(mock.Anything, "objective", testNodeID, (*int64)(nil)).Return(nil).Once()
			},
		},
		{name: "invalid UUID", args: map[string]any{"kind": "objective", "id": "bad"}, wantErr: "parse knowledge node id"},
		{name: "zero UUID", args: map[string]any{"kind": "objective", "id": uuid.Nil.String()}, wantErr: "knowledge node id must not be zero"},
		{
			name: "service error",
			args: map[string]any{"kind": "objective", "id": testNodeID.String()},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().DeleteNode(mock.Anything, "objective", testNodeID, (*int64)(nil)).Return(errors.New("node has edges")).Once()
			},
			wantErr: "node has edges",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			nodes := knowledge.NewMockNodes(t)
			if tt.setup != nil {
				tt.setup(nodes)
			}
			got := run[map[string]any](t, nodeTool(t, nodes, deleteNodeTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Empty(t, got.Data)
		})
	}
}

type nodeMatchesOutput struct {
	Nodes []domain.KnowledgeNodeMatch `json:"nodes"`
}

func TestSearchNodes(t *testing.T) {
	t.Parallel()

	matches := []domain.KnowledgeNodeMatch{{KnowledgeNode: domain.KnowledgeNode{ID: testNodeID, Kind: "objective", Title: "Grow herbs", Data: json.RawMessage(`{}`)}, Score: 0.9}}
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockNodes)
		want    []domain.KnowledgeNodeMatch
		wantErr string
	}{
		{
			name: "defaults",
			args: map[string]any{"query": "gardening"},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().SearchNodes(mock.Anything, "", "gardening", int32(0)).Return(matches, nil).Once()
			},
			want: matches,
		},
		{
			name: "kind and limit",
			args: map[string]any{"kind": "objective", "query": "gardening", "limit": 3},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().SearchNodes(mock.Anything, "objective", "gardening", int32(3)).Return([]domain.KnowledgeNodeMatch{}, nil).Once()
			},
			want: []domain.KnowledgeNodeMatch{},
		},
		{
			name: "service error",
			args: map[string]any{"query": "gardening"},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().SearchNodes(mock.Anything, "", "gardening", int32(0)).Return(nil, errors.New("embedding unavailable")).Once()
			},
			wantErr: "embedding unavailable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			nodes := knowledge.NewMockNodes(t)
			tt.setup(nodes)
			got := run[nodeMatchesOutput](t, nodeTool(t, nodes, searchNodesTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data.Nodes)
		})
	}
}

func TestGetNodeHistory(t *testing.T) {
	t.Parallel()

	history := domain.KnowledgeNodeHistory{
		Versions:   []domain.KnowledgeNodeVersion{{NodeID: testNodeID, Version: 2, Data: json.RawMessage(`{"title":"Grow herbs"}`), Metadata: json.RawMessage(`{"actor":"test"}`)}},
		NextBefore: 2,
	}
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockNodes)
		want    domain.KnowledgeNodeHistory
		wantErr string
	}{
		{
			name: "default page",
			args: map[string]any{"kind": "objective", "id": testNodeID.String()},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().NodeHistory(mock.Anything, "objective", testNodeID, int64(0), 0).Return(history, nil).Once()
			},
			want: history,
		},
		{
			name: "cursor and limit",
			args: map[string]any{"kind": "objective", "id": testNodeID.String(), "before": 4, "limit": 2},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().NodeHistory(mock.Anything, "objective", testNodeID, int64(4), 2).Return(history, nil).Once()
			},
			want: history,
		},
		{name: "invalid UUID", args: map[string]any{"kind": "objective", "id": "bad"}, wantErr: "parse knowledge node id"},
		{name: "zero UUID", args: map[string]any{"kind": "objective", "id": uuid.Nil.String()}, wantErr: "knowledge node id must not be zero"},
		{name: "negative before", args: map[string]any{"kind": "objective", "id": testNodeID.String(), "before": -1}, wantErr: "history before must not be negative"},
		{name: "negative limit", args: map[string]any{"kind": "objective", "id": testNodeID.String(), "limit": -1}, wantErr: "history limit must not be negative"},
		{
			name: "service error",
			args: map[string]any{"kind": "objective", "id": testNodeID.String()},
			setup: func(nodes *knowledge.MockNodes) {
				nodes.EXPECT().NodeHistory(mock.Anything, "objective", testNodeID, int64(0), 0).Return(domain.KnowledgeNodeHistory{}, errors.New("history unavailable")).Once()
			},
			wantErr: "history unavailable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			nodes := knowledge.NewMockNodes(t)
			if tt.setup != nil {
				tt.setup(nodes)
			}
			got := run[domain.KnowledgeNodeHistory](t, nodeTool(t, nodes, getNodeHistoryTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data)
		})
	}
}
