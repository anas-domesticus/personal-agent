package knowledge

import (
	"context"
	_ "embed"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

var (
	//go:embed desc/get_node_type.md
	getNodeTypeDesc string
	//go:embed desc/list_node_types.md
	listNodeTypesDesc string
	//go:embed desc/get_edge_type.md
	getEdgeTypeDesc string
	//go:embed desc/list_edge_types.md
	listEdgeTypesDesc string
)

// SchemaTools returns the native read-only knowledge-schema tools.
func SchemaTools(schema Schema) ([]tool.InvokableTool, error) {
	return kit.Build(
		func() (tool.InvokableTool, error) {
			return kit.New("get_node_type", getNodeTypeDesc, func(ctx context.Context, in schemaKindInput) (domain.KnowledgeNodeType, error) {
				return schema.GetNodeType(ctx, in.Kind)
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("list_node_types", listNodeTypesDesc, func(ctx context.Context, _ struct{}) (listNodeTypesOutput, error) {
				out, err := schema.ListNodeTypes(ctx)
				return listNodeTypesOutput{NodeTypes: out}, err
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("get_edge_type", getEdgeTypeDesc, func(ctx context.Context, in schemaKindInput) (domain.KnowledgeEdgeType, error) {
				return schema.GetEdgeType(ctx, in.Kind)
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("list_edge_types", listEdgeTypesDesc, func(ctx context.Context, _ struct{}) (listEdgeTypesOutput, error) {
				out, err := schema.ListEdgeTypes(ctx)
				return listEdgeTypesOutput{EdgeTypes: out}, err
			})
		},
	)
}

type schemaKindInput struct {
	Kind string `json:"kind" jsonschema_description:"Knowledge graph entity kind."`
}

type listNodeTypesOutput struct {
	NodeTypes []domain.KnowledgeNodeType `json:"node_types"`
}

type listEdgeTypesOutput struct {
	EdgeTypes []domain.KnowledgeEdgeType `json:"edge_types"`
}
