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
	getEdgeTool = iota
	listEdgesTool
	upsertEdgeTool
	deleteEdgeTool
	getEdgeHistoryTool
)

var (
	testEdgeID   = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	testSourceID = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	testTargetID = uuid.MustParse("44444444-4444-4444-8444-444444444444")
)

func edgeTool(t *testing.T, edges *knowledge.MockEdges, index int) tool.InvokableTool {
	t.Helper()
	tools, err := knowledge.EdgeTools(edges)
	require.NoError(t, err)
	return tools[index]
}

func testEdge() domain.KnowledgeEdge {
	return domain.KnowledgeEdge{
		ID: testEdgeID, Kind: "supported_by", Revision: 2,
		SourceID: testSourceID, SourceKind: "claim", TargetID: testTargetID, TargetKind: "source",
		Data: json.RawMessage(`{}`),
	}
}

func TestGetEdge(t *testing.T) {
	t.Parallel()

	edge := testEdge()
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockEdges)
		want    domain.KnowledgeEdge
		wantErr string
	}{
		{
			name: "current edge",
			args: map[string]any{"kind": "supported_by", "id": testEdgeID.String()},
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().GetEdge(mock.Anything, "supported_by", testEdgeID).Return(edge, nil).Once()
			},
			want: edge,
		},
		{
			name: "service error",
			args: map[string]any{"kind": "supported_by", "id": testEdgeID.String()},
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().GetEdge(mock.Anything, "supported_by", testEdgeID).Return(domain.KnowledgeEdge{}, errors.New("edge unavailable")).Once()
			},
			wantErr: "edge unavailable",
		},
		{name: "invalid UUID", args: map[string]any{"kind": "supported_by", "id": "bad"}, wantErr: "parse knowledge edge id"},
		{name: "zero UUID", args: map[string]any{"kind": "supported_by", "id": uuid.Nil.String()}, wantErr: "knowledge edge id must not be zero"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			edges := knowledge.NewMockEdges(t)
			if tt.setup != nil {
				tt.setup(edges)
			}
			got := run[domain.KnowledgeEdge](t, edgeTool(t, edges, getEdgeTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data)
		})
	}
}

type edgesOutput struct {
	Edges []domain.KnowledgeEdge `json:"edges"`
}

func TestListEdges(t *testing.T) {
	t.Parallel()

	edge := testEdge()
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockEdges)
		want    []domain.KnowledgeEdge
		wantErr string
	}{
		{
			name: "defaults without endpoint filter",
			args: map[string]any{},
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().ListEdges(mock.Anything, "", uuid.Nil, uuid.Nil, int32(0)).Return([]domain.KnowledgeEdge{edge}, nil).Once()
			},
			want: []domain.KnowledgeEdge{edge},
		},
		{
			name: "kind source endpoint and limit",
			args: map[string]any{"kind": "supported_by", "source_id": testSourceID.String(), "limit": 5},
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().ListEdges(mock.Anything, "supported_by", testSourceID, uuid.Nil, int32(5)).Return([]domain.KnowledgeEdge{}, nil).Once()
			},
			want: []domain.KnowledgeEdge{},
		},
		{
			name: "target endpoint",
			args: map[string]any{"target_id": testTargetID.String()},
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().ListEdges(mock.Anything, "", uuid.Nil, testTargetID, int32(0)).Return([]domain.KnowledgeEdge{edge}, nil).Once()
			},
			want: []domain.KnowledgeEdge{edge},
		},
		{name: "both endpoint selectors", args: map[string]any{"source_id": testSourceID.String(), "target_id": testTargetID.String()}, wantErr: "specify exactly one or neither edge endpoint selector"},
		{name: "invalid source UUID", args: map[string]any{"source_id": "bad"}, wantErr: "parse knowledge edge source id"},
		{name: "invalid target UUID", args: map[string]any{"target_id": "bad"}, wantErr: "parse knowledge edge target id"},
		{name: "zero source UUID", args: map[string]any{"source_id": uuid.Nil.String()}, wantErr: "knowledge edge source id must not be zero"},
		{name: "zero target UUID", args: map[string]any{"target_id": uuid.Nil.String()}, wantErr: "knowledge edge target id must not be zero"},
		{
			name: "service error",
			args: map[string]any{"kind": "supported_by"},
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().ListEdges(mock.Anything, "supported_by", uuid.Nil, uuid.Nil, int32(0)).Return(nil, errors.New("list failed")).Once()
			},
			wantErr: "list failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			edges := knowledge.NewMockEdges(t)
			if tt.setup != nil {
				tt.setup(edges)
			}
			got := run[edgesOutput](t, edgeTool(t, edges, listEdgesTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data.Edges)
		})
	}
}

func edgeArgs() map[string]any {
	return map[string]any{
		"kind":   "supported_by",
		"source": map[string]any{"kind": "claim", "id": testSourceID.String()},
		"target": map[string]any{"kind": "source", "id": testTargetID.String()},
		"data":   map[string]any{"note": "direct support"},
	}
}

func TestUpsertEdge(t *testing.T) {
	t.Parallel()

	edge := testEdge()
	revision := int64(2)
	source := domain.KnowledgeRef{ID: testSourceID, Kind: "claim"}
	target := domain.KnowledgeRef{ID: testTargetID, Kind: "source"}
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockEdges)
		want    domain.KnowledgeEdge
		wantErr string
	}{
		{
			name: "create or unconditional update",
			args: edgeArgs(),
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().UpsertEdge(mock.Anything, "supported_by", source, target, json.RawMessage(`{"note":"direct support"}`), json.RawMessage(nil), (*int64)(nil)).Return(edge, nil).Once()
			},
			want: edge,
		},
		{
			name: "update with expected revision and metadata",
			args: func() map[string]any {
				args := edgeArgs()
				args["expected_revision"] = revision
				args["metadata"] = map[string]any{"actor": "test"}
				return args
			}(),
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().UpsertEdge(mock.Anything, "supported_by", source, target, json.RawMessage(`{"note":"direct support"}`), json.RawMessage(`{"actor":"test"}`), &revision).Return(edge, nil).Once()
			},
			want: edge,
		},
		{
			name: "invalid source UUID",
			args: func() map[string]any {
				args := edgeArgs()
				args["source"] = map[string]any{"kind": "claim", "id": "bad"}
				return args
			}(),
			wantErr: "parse knowledge edge source id",
		},
		{
			name: "zero source UUID",
			args: func() map[string]any {
				args := edgeArgs()
				args["source"] = map[string]any{"kind": "claim", "id": uuid.Nil.String()}
				return args
			}(),
			wantErr: "knowledge edge source id must not be zero",
		},
		{
			name: "invalid target UUID",
			args: func() map[string]any {
				args := edgeArgs()
				args["target"] = map[string]any{"kind": "source", "id": "bad"}
				return args
			}(),
			wantErr: "parse knowledge edge target id",
		},
		{
			name: "zero target UUID",
			args: func() map[string]any {
				args := edgeArgs()
				args["target"] = map[string]any{"kind": "source", "id": uuid.Nil.String()}
				return args
			}(),
			wantErr: "knowledge edge target id must not be zero",
		},
		{
			name: "service error",
			args: edgeArgs(),
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().UpsertEdge(mock.Anything, "supported_by", source, target, json.RawMessage(`{"note":"direct support"}`), json.RawMessage(nil), (*int64)(nil)).Return(domain.KnowledgeEdge{}, errors.New("endpoint kind not permitted")).Once()
			},
			wantErr: "endpoint kind not permitted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			edges := knowledge.NewMockEdges(t)
			if tt.setup != nil {
				tt.setup(edges)
			}
			got := run[domain.KnowledgeEdge](t, edgeTool(t, edges, upsertEdgeTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data)
		})
	}
}

func TestDeleteEdge(t *testing.T) {
	t.Parallel()

	revision := int64(2)
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockEdges)
		wantErr string
	}{
		{
			name: "delete with optimistic concurrency",
			args: map[string]any{"kind": "supported_by", "id": testEdgeID.String(), "expected_revision": revision},
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().DeleteEdge(mock.Anything, "supported_by", testEdgeID, &revision).Return(nil).Once()
			},
		},
		{
			name: "delete without expected revision",
			args: map[string]any{"kind": "supported_by", "id": testEdgeID.String()},
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().DeleteEdge(mock.Anything, "supported_by", testEdgeID, (*int64)(nil)).Return(nil).Once()
			},
		},
		{name: "invalid UUID", args: map[string]any{"kind": "supported_by", "id": "bad"}, wantErr: "parse knowledge edge id"},
		{name: "zero UUID", args: map[string]any{"kind": "supported_by", "id": uuid.Nil.String()}, wantErr: "knowledge edge id must not be zero"},
		{
			name: "service error",
			args: map[string]any{"kind": "supported_by", "id": testEdgeID.String()},
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().DeleteEdge(mock.Anything, "supported_by", testEdgeID, (*int64)(nil)).Return(errors.New("edge not found")).Once()
			},
			wantErr: "edge not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			edges := knowledge.NewMockEdges(t)
			if tt.setup != nil {
				tt.setup(edges)
			}
			got := run[map[string]any](t, edgeTool(t, edges, deleteEdgeTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Empty(t, got.Data)
		})
	}
}

func TestGetEdgeHistory(t *testing.T) {
	t.Parallel()

	history := domain.KnowledgeEdgeHistory{
		Versions:   []domain.KnowledgeEdgeVersion{{EdgeID: testEdgeID, Version: 2, Data: json.RawMessage(`{}`), Metadata: json.RawMessage(`{"actor":"test"}`)}},
		NextBefore: 2,
	}
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockEdges)
		want    domain.KnowledgeEdgeHistory
		wantErr string
	}{
		{
			name: "default page",
			args: map[string]any{"kind": "supported_by", "id": testEdgeID.String()},
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().EdgeHistory(mock.Anything, "supported_by", testEdgeID, int64(0), 0).Return(history, nil).Once()
			},
			want: history,
		},
		{
			name: "cursor and limit",
			args: map[string]any{"kind": "supported_by", "id": testEdgeID.String(), "before": 4, "limit": 2},
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().EdgeHistory(mock.Anything, "supported_by", testEdgeID, int64(4), 2).Return(history, nil).Once()
			},
			want: history,
		},
		{name: "invalid UUID", args: map[string]any{"kind": "supported_by", "id": "bad"}, wantErr: "parse knowledge edge id"},
		{name: "zero UUID", args: map[string]any{"kind": "supported_by", "id": uuid.Nil.String()}, wantErr: "knowledge edge id must not be zero"},
		{name: "negative before", args: map[string]any{"kind": "supported_by", "id": testEdgeID.String(), "before": -1}, wantErr: "history before must not be negative"},
		{name: "negative limit", args: map[string]any{"kind": "supported_by", "id": testEdgeID.String(), "limit": -1}, wantErr: "history limit must not be negative"},
		{
			name: "service error",
			args: map[string]any{"kind": "supported_by", "id": testEdgeID.String()},
			setup: func(edges *knowledge.MockEdges) {
				edges.EXPECT().EdgeHistory(mock.Anything, "supported_by", testEdgeID, int64(0), 0).Return(domain.KnowledgeEdgeHistory{}, errors.New("history unavailable")).Once()
			},
			wantErr: "history unavailable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			edges := knowledge.NewMockEdges(t)
			if tt.setup != nil {
				tt.setup(edges)
			}
			got := run[domain.KnowledgeEdgeHistory](t, edgeTool(t, edges, getEdgeHistoryTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data)
		})
	}
}
