package knowledge_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/knowledge"
)

const (
	getNodeTypeTool = iota
	listNodeTypesTool
	getEdgeTypeTool
	listEdgeTypesTool
)

func schemaTool(t *testing.T, schema *knowledge.MockSchema, index int) tool.InvokableTool {
	t.Helper()
	tools, err := knowledge.SchemaTools(schema)
	require.NoError(t, err)
	return tools[index]
}

func TestGetNodeType(t *testing.T) {
	t.Parallel()

	nodeType := domain.KnowledgeNodeType{Kind: "objective", Versioned: true, DataSchema: json.RawMessage(`{"type":"object"}`)}
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockSchema)
		want    domain.KnowledgeNodeType
		wantErr string
	}{
		{
			name: "schema",
			args: map[string]any{"kind": "objective"},
			setup: func(schema *knowledge.MockSchema) {
				schema.EXPECT().GetNodeType(mock.Anything, "objective").Return(nodeType, nil).Once()
			},
			want: nodeType,
		},
		{
			name: "service error",
			args: map[string]any{"kind": "objective"},
			setup: func(schema *knowledge.MockSchema) {
				schema.EXPECT().GetNodeType(mock.Anything, "objective").Return(domain.KnowledgeNodeType{}, errors.New("schema not found")).Once()
			},
			wantErr: "schema not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			schema := knowledge.NewMockSchema(t)
			tt.setup(schema)
			got := run[domain.KnowledgeNodeType](t, schemaTool(t, schema, getNodeTypeTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data)
		})
	}
}

type nodeTypesOutput struct {
	NodeTypes []domain.KnowledgeNodeType `json:"node_types"`
}

func TestListNodeTypes(t *testing.T) {
	t.Parallel()

	nodeTypes := []domain.KnowledgeNodeType{{Kind: "objective", Versioned: true, DataSchema: json.RawMessage(`{"type":"object"}`)}}
	tests := []struct {
		name    string
		setup   func(*knowledge.MockSchema)
		want    []domain.KnowledgeNodeType
		wantErr string
	}{
		{
			name: "schemas",
			setup: func(schema *knowledge.MockSchema) {
				schema.EXPECT().ListNodeTypes(mock.Anything).Return(nodeTypes, nil).Once()
			},
			want: nodeTypes,
		},
		{
			name: "service error",
			setup: func(schema *knowledge.MockSchema) {
				schema.EXPECT().ListNodeTypes(mock.Anything).Return(nil, errors.New("list failed")).Once()
			},
			wantErr: "list failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			schema := knowledge.NewMockSchema(t)
			tt.setup(schema)
			got := run[nodeTypesOutput](t, schemaTool(t, schema, listNodeTypesTool), map[string]any{})
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data.NodeTypes)
		})
	}
}

func TestGetEdgeType(t *testing.T) {
	t.Parallel()

	edgeType := domain.KnowledgeEdgeType{
		Kind: "supported_by", DataSchema: json.RawMessage(`{"type":"object"}`),
		Endpoints: []domain.KnowledgeEndpoint{{Source: "claim", Target: "source"}},
	}
	tests := []struct {
		name    string
		args    map[string]any
		setup   func(*knowledge.MockSchema)
		want    domain.KnowledgeEdgeType
		wantErr string
	}{
		{
			name: "schema",
			args: map[string]any{"kind": "supported_by"},
			setup: func(schema *knowledge.MockSchema) {
				schema.EXPECT().GetEdgeType(mock.Anything, "supported_by").Return(edgeType, nil).Once()
			},
			want: edgeType,
		},
		{
			name: "service error",
			args: map[string]any{"kind": "supported_by"},
			setup: func(schema *knowledge.MockSchema) {
				schema.EXPECT().GetEdgeType(mock.Anything, "supported_by").Return(domain.KnowledgeEdgeType{}, errors.New("schema not found")).Once()
			},
			wantErr: "schema not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			schema := knowledge.NewMockSchema(t)
			tt.setup(schema)
			got := run[domain.KnowledgeEdgeType](t, schemaTool(t, schema, getEdgeTypeTool), tt.args)
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data)
		})
	}
}

type edgeTypesOutput struct {
	EdgeTypes []domain.KnowledgeEdgeType `json:"edge_types"`
}

func TestListEdgeTypes(t *testing.T) {
	t.Parallel()

	edgeTypes := []domain.KnowledgeEdgeType{{
		Kind: "supported_by", DataSchema: json.RawMessage(`{"type":"object"}`),
		Endpoints: []domain.KnowledgeEndpoint{{Source: "claim", Target: "source"}},
	}}
	tests := []struct {
		name    string
		setup   func(*knowledge.MockSchema)
		want    []domain.KnowledgeEdgeType
		wantErr string
	}{
		{
			name: "schemas",
			setup: func(schema *knowledge.MockSchema) {
				schema.EXPECT().ListEdgeTypes(mock.Anything).Return(edgeTypes, nil).Once()
			},
			want: edgeTypes,
		},
		{
			name: "service error",
			setup: func(schema *knowledge.MockSchema) {
				schema.EXPECT().ListEdgeTypes(mock.Anything).Return(nil, errors.New("list failed")).Once()
			},
			wantErr: "list failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			schema := knowledge.NewMockSchema(t)
			tt.setup(schema)
			got := run[edgeTypesOutput](t, schemaTool(t, schema, listEdgeTypesTool), map[string]any{})
			if tt.wantErr != "" {
				requireError(t, got, tt.wantErr)
				return
			}
			requireSuccess(t, got)
			assert.Equal(t, tt.want, got.Data.EdgeTypes)
		})
	}
}
