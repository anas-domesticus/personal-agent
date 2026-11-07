package knowledge_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/tools/knowledge"
)

func TestTools(t *testing.T) {
	t.Parallel()

	tools, err := knowledge.Tools(knowledge.NewMockStore(t))
	require.NoError(t, err)
	require.Len(t, tools, 2)
	assert.Equal(t, "upsert_knowledge_node", kit.Name(tools[0]))
	assert.Equal(t, "search_knowledge_nodes", kit.Name(tools[1]))
}

type searchOutput struct {
	Nodes []domain.KnowledgeNodeMatch `json:"nodes"`
}

type result[D any] struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Data  D      `json:"data"`
}

func run[D any](t *testing.T, tl tool.InvokableTool, args map[string]any) result[D] {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	out, err := tl.InvokableRun(context.Background(), string(raw))
	require.NoError(t, err)
	var got result[D]
	require.NoError(t, json.Unmarshal([]byte(out), &got), "decoding %q", out)
	return got
}

func TestUpsertKnowledgeNode(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("12345678-1234-5678-9234-567812345678")
	revision := int64(2)
	node := domain.KnowledgeNode{
		ID: id, Kind: "objective", Revision: revision,
		Title: "Grow herbs", Description: "Keep the kitchen supplied",
		Data: json.RawMessage(`{"title":"Grow herbs","description":"Keep the kitchen supplied"}`),
	}
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockStore)
		want    domain.KnowledgeNode
		wantErr string
	}{
		{
			name: "create",
			args: map[string]any{"kind": "objective", "data": map[string]any{"title": "Grow herbs"}},
			setup: func(store *knowledge.MockStore) {
				store.EXPECT().Upsert(mock.Anything, "objective", uuid.Nil, json.RawMessage(`{"title":"Grow herbs"}`), (*int64)(nil)).Return(node, nil).Once()
			},
			want: node,
		},
		{
			name: "update",
			args: map[string]any{"kind": "objective", "id": id.String(), "expected_revision": revision, "data": map[string]any{"title": "Grow herbs"}},
			setup: func(store *knowledge.MockStore) {
				store.EXPECT().Upsert(mock.Anything, "objective", id, json.RawMessage(`{"title":"Grow herbs"}`), &revision).Return(node, nil).Once()
			},
			want: node,
		},
		{
			name:    "invalid UUID",
			args:    map[string]any{"kind": "objective", "id": "not-a-uuid", "data": map[string]any{}},
			wantErr: "parse knowledge node id",
		},
		{
			name:    "zero UUID",
			args:    map[string]any{"kind": "objective", "id": uuid.Nil.String(), "data": map[string]any{}},
			wantErr: "knowledge node id must not be zero",
		},
		{
			name:    "new node with expected revision",
			args:    map[string]any{"kind": "objective", "expected_revision": revision, "data": map[string]any{}},
			wantErr: "new knowledge node has an expected revision: conflict",
		},
		{
			name: "service error",
			args: map[string]any{"kind": "objective", "data": map[string]any{}},
			setup: func(store *knowledge.MockStore) {
				store.EXPECT().Upsert(mock.Anything, "objective", uuid.Nil, json.RawMessage(`{}`), (*int64)(nil)).Return(domain.KnowledgeNode{}, errors.New("database unavailable")).Once()
			},
			wantErr: "database unavailable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := knowledge.NewMockStore(t)
			if tt.setup != nil {
				tt.setup(store)
			}
			tools, err := knowledge.Tools(store)
			require.NoError(t, err)

			got := run[domain.KnowledgeNode](t, tools[0], tt.args)
			if tt.wantErr != "" {
				assert.False(t, got.OK)
				assert.ErrorContains(t, errors.New(got.Error), tt.wantErr)
				return
			}
			require.True(t, got.OK, got.Error)
			assert.Equal(t, tt.want, got.Data)
		})
	}
}

func TestSearchKnowledgeNodes(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("12345678-1234-5678-9234-567812345678")
	matches := []domain.KnowledgeNodeMatch{{
		KnowledgeNode: domain.KnowledgeNode{ID: id, Kind: "objective", Title: "Grow herbs", Data: json.RawMessage(`null`)},
		Score:         0.9,
	}}
	tests := []struct {
		name      string
		args      map[string]any
		wantLimit int32
		response  []domain.KnowledgeNodeMatch
		err       error
		want      []domain.KnowledgeNodeMatch
		wantErr   string
	}{
		{name: "default limit passed to service", args: map[string]any{"kind": "objective", "query": "gardening"}, wantLimit: 0, response: matches, want: matches},
		{name: "explicit limit", args: map[string]any{"query": "gardening", "limit": 3}, wantLimit: 3, response: []domain.KnowledgeNodeMatch{}, want: []domain.KnowledgeNodeMatch{}},
		{name: "limit cap passed to service", args: map[string]any{"query": "gardening", "limit": 101}, wantLimit: 101, response: []domain.KnowledgeNodeMatch{}, want: []domain.KnowledgeNodeMatch{}},
		{name: "negative limit passed to service", args: map[string]any{"query": "gardening", "limit": -1}, wantLimit: -1, response: []domain.KnowledgeNodeMatch{}, want: []domain.KnowledgeNodeMatch{}},
		{name: "service error", args: map[string]any{"query": "gardening"}, wantLimit: 0, err: errors.New("embedding unavailable"), wantErr: "embedding unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := knowledge.NewMockStore(t)
			kind, _ := tt.args["kind"].(string)
			store.EXPECT().Search(mock.Anything, kind, "gardening", tt.wantLimit).Return(tt.response, tt.err).Once()
			tools, err := knowledge.Tools(store)
			require.NoError(t, err)

			got := run[searchOutput](t, tools[1], tt.args)
			if tt.wantErr != "" {
				assert.False(t, got.OK)
				assert.ErrorContains(t, errors.New(got.Error), tt.wantErr)
				return
			}
			require.True(t, got.OK, got.Error)
			assert.Equal(t, tt.want, got.Data.Nodes)
		})
	}
}
