package knowledge

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

var (
	//go:embed desc/get_edge.md
	getEdgeDesc string
	//go:embed desc/list_edges.md
	listEdgesDesc string
	//go:embed desc/upsert_edge.md
	upsertEdgeDesc string
	//go:embed desc/delete_edge.md
	deleteEdgeDesc string
	//go:embed desc/get_edge_history.md
	getEdgeHistoryDesc string
)

// EdgeTools returns the native tools for current edges and their history.
func EdgeTools(edges Edges) ([]tool.InvokableTool, error) {
	return kit.Build(
		func() (tool.InvokableTool, error) {
			return kit.New("get_edge", getEdgeDesc, func(ctx context.Context, in edgeIDInput) (domain.KnowledgeEdge, error) {
				id, err := parseID(in.ID, "knowledge edge id")
				if err != nil {
					return domain.KnowledgeEdge{}, err
				}
				return edges.GetEdge(ctx, in.Kind, id)
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("list_edges", listEdgesDesc, func(ctx context.Context, in listEdgesInput) (listEdgesOutput, error) {
				sourceID, targetID, err := in.endpointIDs()
				if err != nil {
					return listEdgesOutput{}, err
				}
				out, err := edges.ListEdges(ctx, in.Kind, sourceID, targetID, in.Limit)
				return listEdgesOutput{Edges: out}, err
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("upsert_edge", upsertEdgeDesc, func(ctx context.Context, in upsertEdgeInput) (domain.KnowledgeEdge, error) {
				return upsertEdge(ctx, edges, in)
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("delete_edge", deleteEdgeDesc, func(ctx context.Context, in deleteEdgeInput) (deleteOutput, error) {
				id, err := parseID(in.ID, "knowledge edge id")
				if err != nil {
					return deleteOutput{}, err
				}
				return deleteOutput{}, edges.DeleteEdge(ctx, in.Kind, id, in.ExpectedRevision)
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("get_edge_history", getEdgeHistoryDesc, func(ctx context.Context, in historyInput) (domain.KnowledgeEdgeHistory, error) {
				id, err := parseID(in.ID, "knowledge edge id")
				if err != nil {
					return domain.KnowledgeEdgeHistory{}, err
				}
				if err := validateHistory(in.Before, in.Limit); err != nil {
					return domain.KnowledgeEdgeHistory{}, err
				}
				return edges.EdgeHistory(ctx, in.Kind, id, in.Before, in.Limit)
			})
		},
	)
}

type edgeIDInput struct {
	Kind string `json:"kind" jsonschema_description:"Knowledge edge kind."`
	ID   string `json:"id" jsonschema_description:"Edge UUID."`
}

type listEdgesInput struct {
	Kind     string `json:"kind,omitempty" jsonschema_description:"Optional knowledge edge kind to restrict results."`
	SourceID string `json:"source_id,omitempty" jsonschema_description:"Optional source node UUID. Specify at most one endpoint selector."`
	TargetID string `json:"target_id,omitempty" jsonschema_description:"Optional target node UUID. Specify at most one endpoint selector."`
	Limit    int32  `json:"limit,omitempty" jsonschema_description:"Maximum results, from 1 to 100. Defaults to 10."`
}

func (in listEdgesInput) endpointIDs() (uuid.UUID, uuid.UUID, error) {
	if in.SourceID != "" && in.TargetID != "" {
		return uuid.Nil, uuid.Nil, fmt.Errorf("specify exactly one or neither edge endpoint selector")
	}
	sourceID, err := parseOptionalID(in.SourceID, "knowledge edge source id")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	targetID, err := parseOptionalID(in.TargetID, "knowledge edge target id")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return sourceID, targetID, nil
}

type listEdgesOutput struct {
	Edges []domain.KnowledgeEdge `json:"edges"`
}

type knowledgeRefInput struct {
	Kind string `json:"kind" jsonschema_description:"Endpoint node kind."`
	ID   string `json:"id" jsonschema_description:"Endpoint node UUID."`
}

func (in knowledgeRefInput) ref(name string) (domain.KnowledgeRef, error) {
	id, err := parseID(in.ID, name)
	if err != nil {
		return domain.KnowledgeRef{}, err
	}
	return domain.KnowledgeRef{ID: id, Kind: in.Kind}, nil
}

type upsertEdgeInput struct {
	Kind             string            `json:"kind" jsonschema_description:"Knowledge edge kind."`
	Source           knowledgeRefInput `json:"source" jsonschema_description:"Source node reference, containing its kind and id."`
	Target           knowledgeRefInput `json:"target" jsonschema_description:"Target node reference, containing its kind and id."`
	Data             map[string]any    `json:"data" jsonschema_description:"Edge data matching the selected edge kind's schema."`
	Metadata         map[string]any    `json:"metadata,omitempty" jsonschema_description:"Optional JSON object stored as metadata with this edge revision."`
	ExpectedRevision *int64            `json:"expected_revision,omitempty" jsonschema_description:"Current revision required for an optimistic-concurrency update."`
}

func upsertEdge(ctx context.Context, edges Edges, in upsertEdgeInput) (domain.KnowledgeEdge, error) {
	source, err := in.Source.ref("knowledge edge source id")
	if err != nil {
		return domain.KnowledgeEdge{}, err
	}
	target, err := in.Target.ref("knowledge edge target id")
	if err != nil {
		return domain.KnowledgeEdge{}, err
	}
	data, err := rawObject("knowledge edge data", in.Data)
	if err != nil {
		return domain.KnowledgeEdge{}, err
	}
	metadata, err := rawObject("knowledge edge metadata", in.Metadata)
	if err != nil {
		return domain.KnowledgeEdge{}, err
	}
	return edges.UpsertEdge(ctx, in.Kind, source, target, data, metadata, in.ExpectedRevision)
}

type deleteEdgeInput struct {
	Kind             string `json:"kind" jsonschema_description:"Knowledge edge kind."`
	ID               string `json:"id" jsonschema_description:"Edge UUID."`
	ExpectedRevision *int64 `json:"expected_revision,omitempty" jsonschema_description:"Current revision required for an optimistic-concurrency delete."`
}
