package knowledge_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/tools/knowledge"
)

func TestTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func() ([]tool.InvokableTool, error)
		want  []string
	}{
		{
			name: "nodes",
			build: func() ([]tool.InvokableTool, error) {
				return knowledge.NodeTools(knowledge.NewMockNodes(t))
			},
			want: []string{"get_node", "list_nodes", "upsert_node", "delete_node", "search_nodes", "get_node_history"},
		},
		{
			name: "edges",
			build: func() ([]tool.InvokableTool, error) {
				return knowledge.EdgeTools(knowledge.NewMockEdges(t))
			},
			want: []string{"get_edge", "list_edges", "upsert_edge", "delete_edge", "get_edge_history"},
		},
		{
			name: "schema",
			build: func() ([]tool.InvokableTool, error) {
				return knowledge.SchemaTools(knowledge.NewMockSchema(t))
			},
			want: []string{"get_node_type", "list_node_types", "get_edge_type", "list_edge_types"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tools, err := tt.build()
			require.NoError(t, err)
			require.Len(t, tools, len(tt.want))
			for i, tl := range tools {
				assert.Equal(t, tt.want[i], kit.Name(tl))
				info, err := tl.Info(context.Background())
				require.NoError(t, err)
				assert.NotEmpty(t, info.Desc)
			}
		})
	}
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

func requireSuccess[D any](t *testing.T, got result[D]) {
	t.Helper()
	require.True(t, got.OK, got.Error)
	assert.Empty(t, got.Error)
}

func requireError[D any](t *testing.T, got result[D], want string) {
	t.Helper()
	assert.False(t, got.OK)
	assert.Contains(t, got.Error, want)
}
