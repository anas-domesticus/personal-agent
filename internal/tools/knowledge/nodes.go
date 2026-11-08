package knowledge

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

var (
	//go:embed desc/get_node.md
	getNodeDesc string
	//go:embed desc/list_nodes.md
	listNodesDesc string
	//go:embed desc/upsert_node.md
	upsertNodeDesc string
	//go:embed desc/delete_node.md
	deleteNodeDesc string
	//go:embed desc/search_nodes.md
	searchNodesDesc string
	//go:embed desc/get_node_history.md
	getNodeHistoryDesc string
)

// NodeTools returns the native tools for current nodes, search and history.
func NodeTools(nodes Nodes) ([]tool.InvokableTool, error) {
	return kit.Build(
		func() (tool.InvokableTool, error) {
			return kit.New("get_node", getNodeDesc, func(ctx context.Context, in nodeIDInput) (domain.KnowledgeNode, error) {
				id, err := parseID(in.ID, "knowledge node id")
				if err != nil {
					return domain.KnowledgeNode{}, err
				}
				return nodes.GetNode(ctx, in.Kind, id)
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("list_nodes", listNodesDesc, func(ctx context.Context, in listNodesInput) (listNodesOutput, error) {
				out, err := nodes.ListNodes(ctx, in.Kind, in.Limit)
				return listNodesOutput{Nodes: out}, err
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("upsert_node", upsertNodeDesc, func(ctx context.Context, in upsertNodeInput) (domain.KnowledgeNode, error) {
				return upsertNode(ctx, nodes, in)
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("delete_node", deleteNodeDesc, func(ctx context.Context, in deleteNodeInput) (deleteOutput, error) {
				id, err := parseID(in.ID, "knowledge node id")
				if err != nil {
					return deleteOutput{}, err
				}
				return deleteOutput{}, nodes.DeleteNode(ctx, in.Kind, id, in.ExpectedRevision)
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("search_nodes", searchNodesDesc, func(ctx context.Context, in searchNodesInput) (searchNodesOutput, error) {
				out, err := nodes.SearchNodes(ctx, in.Kind, in.Query, in.Limit)
				return searchNodesOutput{Nodes: out}, err
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("get_node_history", getNodeHistoryDesc, func(ctx context.Context, in historyInput) (domain.KnowledgeNodeHistory, error) {
				id, err := parseID(in.ID, "knowledge node id")
				if err != nil {
					return domain.KnowledgeNodeHistory{}, err
				}
				if err := validateHistory(in.Before, in.Limit); err != nil {
					return domain.KnowledgeNodeHistory{}, err
				}
				return nodes.NodeHistory(ctx, in.Kind, id, in.Before, in.Limit)
			})
		},
	)
}

type nodeIDInput struct {
	Kind string `json:"kind" jsonschema_description:"Knowledge node kind."`
	ID   string `json:"id" jsonschema_description:"Node UUID."`
}

type listNodesInput struct {
	Kind  string `json:"kind,omitempty" jsonschema_description:"Optional knowledge node kind to restrict results."`
	Limit int32  `json:"limit,omitempty" jsonschema_description:"Maximum results, from 1 to 100. Defaults to 10."`
}

type listNodesOutput struct {
	Nodes []domain.KnowledgeNode `json:"nodes"`
}

type upsertNodeInput struct {
	Kind             string         `json:"kind" jsonschema_description:"Knowledge node kind."`
	ID               string         `json:"id,omitempty" jsonschema_description:"Existing node id to update. Omit to create a node."`
	ExpectedRevision *int64         `json:"expected_revision,omitempty" jsonschema_description:"Required current revision for an optimistic-concurrency update. Must be omitted when creating a node."`
	Data             map[string]any `json:"data" jsonschema_description:"Node data, including title and description plus fields required by the selected kind."`
	Metadata         map[string]any `json:"metadata,omitempty" jsonschema_description:"Optional JSON object stored as metadata with this node revision."`
}

func upsertNode(ctx context.Context, nodes Nodes, in upsertNodeInput) (domain.KnowledgeNode, error) {
	id, err := parseOptionalID(in.ID, "knowledge node id")
	if err != nil {
		return domain.KnowledgeNode{}, err
	}
	if in.ID == "" && in.ExpectedRevision != nil {
		return domain.KnowledgeNode{}, fmt.Errorf("new knowledge node has an expected revision: %w", domain.ErrConflict)
	}
	data, err := rawObject("knowledge node data", in.Data)
	if err != nil {
		return domain.KnowledgeNode{}, err
	}
	metadata, err := rawObject("knowledge node metadata", in.Metadata)
	if err != nil {
		return domain.KnowledgeNode{}, err
	}
	return nodes.UpsertNode(ctx, in.Kind, id, data, metadata, in.ExpectedRevision)
}

type deleteNodeInput struct {
	Kind             string `json:"kind" jsonschema_description:"Knowledge node kind."`
	ID               string `json:"id" jsonschema_description:"Node UUID."`
	ExpectedRevision *int64 `json:"expected_revision,omitempty" jsonschema_description:"Current revision required for an optimistic-concurrency delete."`
}

type searchNodesInput struct {
	Query string `json:"query" jsonschema_description:"Meaning to compare with node titles and descriptions."`
	Kind  string `json:"kind,omitempty" jsonschema_description:"Optional knowledge node kind to restrict results."`
	Limit int32  `json:"limit,omitempty" jsonschema_description:"Maximum results, from 1 to 100. Defaults to 10."`
}

type searchNodesOutput struct {
	Nodes []domain.KnowledgeNodeMatch `json:"nodes"`
}

type historyInput struct {
	Kind   string `json:"kind" jsonschema_description:"Knowledge graph entity kind."`
	ID     string `json:"id" jsonschema_description:"Knowledge graph entity UUID."`
	Before int64  `json:"before,omitempty" jsonschema_description:"Return versions strictly before this version. Zero starts with the newest version."`
	Limit  int    `json:"limit,omitempty" jsonschema_description:"Maximum versions to return. Zero uses the service default."`
}
