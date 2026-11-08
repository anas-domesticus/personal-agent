package mcpserver

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/anas-domesticus/personal-agent/internal/domain"
)

type nodeIDInput struct {
	Kind string `json:"kind" jsonschema:"Registered node kind."`
	ID   string `json:"id" jsonschema:"Node UUID."`
}

type listNodesInput struct {
	Kind  string `json:"kind,omitempty" jsonschema:"Optional node kind to restrict results."`
	Limit int32  `json:"limit,omitempty" jsonschema:"Maximum results. The service applies its default and maximum."`
}

type nodesOutput struct {
	Nodes []map[string]any `json:"nodes"`
}

type upsertNodeInput struct {
	Kind             string         `json:"kind" jsonschema:"Registered node kind."`
	ID               string         `json:"id,omitempty" jsonschema:"Existing node UUID to update. Omit to create a node."`
	Data             map[string]any `json:"data" jsonschema:"Node data object validated by the registered node schema."`
	Metadata         map[string]any `json:"metadata,omitempty" jsonschema:"Optional immutable version metadata object."`
	ExpectedRevision *int64         `json:"expected_revision,omitempty" jsonschema:"Required current revision for an optimistic-concurrency update."`
}

type deleteNodeInput struct {
	Kind             string `json:"kind" jsonschema:"Registered node kind."`
	ID               string `json:"id" jsonschema:"Node UUID."`
	ExpectedRevision *int64 `json:"expected_revision,omitempty" jsonschema:"Required current revision for optimistic concurrency."`
}

type searchNodesInput struct {
	Kind  string `json:"kind,omitempty" jsonschema:"Optional node kind to restrict results."`
	Query string `json:"query" jsonschema:"Meaning to compare with node titles and descriptions."`
	Limit int32  `json:"limit,omitempty" jsonschema:"Maximum results. The service applies its default and maximum."`
}

type nodeMatchesOutput struct {
	Nodes []map[string]any `json:"nodes"`
}

type nodeHistoryInput struct {
	Kind   string `json:"kind" jsonschema:"Registered node kind."`
	ID     string `json:"id" jsonschema:"Node UUID."`
	Before int64  `json:"before,omitempty" jsonschema:"Nonnegative exclusive version cursor. Zero starts at the newest version."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Nonnegative maximum snapshots. The service applies its default and maximum."`
}

type edgeIDInput struct {
	Kind string `json:"kind" jsonschema:"Registered edge kind."`
	ID   string `json:"id" jsonschema:"Edge UUID."`
}

type listEdgesInput struct {
	Kind     string `json:"kind,omitempty" jsonschema:"Optional edge kind to restrict results."`
	SourceID string `json:"source_id,omitempty" jsonschema:"Optional source node UUID. Specify at most one endpoint selector."`
	TargetID string `json:"target_id,omitempty" jsonschema:"Optional target node UUID. Specify at most one endpoint selector."`
	Limit    int32  `json:"limit,omitempty" jsonschema:"Maximum results. The service applies its default and maximum."`
}

type edgesOutput struct {
	Edges []map[string]any `json:"edges"`
}

type knowledgeRefInput struct {
	ID   string `json:"id" jsonschema:"Node UUID."`
	Kind string `json:"kind" jsonschema:"Registered node kind."`
}

type upsertEdgeInput struct {
	Kind             string            `json:"kind" jsonschema:"Registered edge kind."`
	Source           knowledgeRefInput `json:"source" jsonschema:"Source node reference."`
	Target           knowledgeRefInput `json:"target" jsonschema:"Target node reference."`
	Data             map[string]any    `json:"data" jsonschema:"Edge data object validated by the registered edge schema."`
	Metadata         map[string]any    `json:"metadata,omitempty" jsonschema:"Optional immutable version metadata object."`
	ExpectedRevision *int64            `json:"expected_revision,omitempty" jsonschema:"Required current revision for an optimistic-concurrency update."`
}

type deleteEdgeInput struct {
	Kind             string `json:"kind" jsonschema:"Registered edge kind."`
	ID               string `json:"id" jsonschema:"Edge UUID."`
	ExpectedRevision *int64 `json:"expected_revision,omitempty" jsonschema:"Required current revision for optimistic concurrency."`
}

type edgeHistoryInput struct {
	Kind   string `json:"kind" jsonschema:"Registered edge kind."`
	ID     string `json:"id" jsonschema:"Edge UUID."`
	Before int64  `json:"before,omitempty" jsonschema:"Nonnegative exclusive version cursor. Zero starts at the newest version."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Nonnegative maximum snapshots. The service applies its default and maximum."`
}

type schemaKindInput struct {
	Kind string `json:"kind" jsonschema:"Registered schema kind."`
}

type nodeTypesOutput struct {
	NodeTypes []map[string]any `json:"node_types"`
}

type edgeTypesOutput struct {
	EdgeTypes []map[string]any `json:"edge_types"`
}

type createNodeTypeInput struct {
	Kind       string         `json:"kind" jsonschema:"New dynamic node kind."`
	DataSchema map[string]any `json:"data_schema" jsonschema:"JSON Schema object for node data."`
}

type createEdgeTypeInput struct {
	Kind       string                     `json:"kind" jsonschema:"New dynamic edge kind."`
	DataSchema map[string]any             `json:"data_schema" jsonschema:"JSON Schema object for edge data."`
	Endpoints  []domain.KnowledgeEndpoint `json:"endpoints" jsonschema:"Permitted source and target node-kind pairs."`
}

func registerNodeTools(s *mcp.Server, nodes Nodes) {
	add(s, "get_node", "Read a node's current data by kind and UUID.", readOnly, func(ctx context.Context, in nodeIDInput) (map[string]any, error) {
		id, err := parseNonZeroUUID(in.ID, "node id")
		if err != nil {
			return nil, err
		}
		out, err := nodes.GetNode(ctx, in.Kind, id)
		if err != nil {
			return nil, err
		}
		return object(out)
	})
	add(s, "list_nodes", "List current nodes in deterministic order, optionally filtered by kind.", readOnly, func(ctx context.Context, in listNodesInput) (nodesOutput, error) {
		out, err := nodes.ListNodes(ctx, in.Kind, in.Limit)
		if err != nil {
			return nodesOutput{}, err
		}
		items, err := objects(out)
		return nodesOutput{Nodes: items}, err
	})
	add(s, "upsert_node", "Create or update a node and record its immutable version history.", writing, func(ctx context.Context, in upsertNodeInput) (map[string]any, error) {
		var id uuid.UUID
		var err error
		if in.ID != "" {
			id, err = parseNonZeroUUID(in.ID, "node id")
			if err != nil {
				return nil, err
			}
		} else if in.ExpectedRevision != nil {
			return nil, fmt.Errorf("new node has an expected revision: %w", domain.ErrConflict)
		}
		out, err := nodes.UpsertNode(ctx, in.Kind, id, rawObject(in.Data), rawObject(in.Metadata), in.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		return object(out)
	})
	add(s, "delete_node", "Delete a node when no edges reference it, optionally checking its revision.", writing, func(ctx context.Context, in deleteNodeInput) (struct{}, error) {
		id, err := parseNonZeroUUID(in.ID, "node id")
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, nodes.DeleteNode(ctx, in.Kind, id, in.ExpectedRevision)
	})
	add(s, "search_nodes", "Find current nodes by semantic similarity to their titles and descriptions.", readOnly, func(ctx context.Context, in searchNodesInput) (nodeMatchesOutput, error) {
		out, err := nodes.SearchNodes(ctx, in.Kind, in.Query, in.Limit)
		if err != nil {
			return nodeMatchesOutput{}, err
		}
		items, err := objects(out)
		return nodeMatchesOutput{Nodes: items}, err
	})
	add(s, "get_node_history", "Read a newest-first page of immutable node versions.", readOnly, func(ctx context.Context, in nodeHistoryInput) (map[string]any, error) {
		id, err := historyID(in.ID, in.Before, in.Limit, "node")
		if err != nil {
			return nil, err
		}
		out, err := nodes.NodeHistory(ctx, in.Kind, id, in.Before, in.Limit)
		if err != nil {
			return nil, err
		}
		return object(out)
	})
}

func registerEdgeTools(s *mcp.Server, edges Edges) {
	add(s, "get_edge", "Read an edge's current data by kind and UUID.", readOnly, func(ctx context.Context, in edgeIDInput) (map[string]any, error) {
		id, err := parseNonZeroUUID(in.ID, "edge id")
		if err != nil {
			return nil, err
		}
		out, err := edges.GetEdge(ctx, in.Kind, id)
		if err != nil {
			return nil, err
		}
		return object(out)
	})
	add(s, "list_edges", "List current edges, optionally filtered by kind and at most one endpoint.", readOnly, func(ctx context.Context, in listEdgesInput) (edgesOutput, error) {
		if in.SourceID != "" && in.TargetID != "" {
			return edgesOutput{}, fmt.Errorf("specify exactly one or neither edge endpoint selector")
		}
		var sourceID, targetID uuid.UUID
		var err error
		if in.SourceID != "" {
			sourceID, err = parseNonZeroUUID(in.SourceID, "source id")
			if err != nil {
				return edgesOutput{}, err
			}
		}
		if in.TargetID != "" {
			targetID, err = parseNonZeroUUID(in.TargetID, "target id")
			if err != nil {
				return edgesOutput{}, err
			}
		}
		out, err := edges.ListEdges(ctx, in.Kind, sourceID, targetID, in.Limit)
		if err != nil {
			return edgesOutput{}, err
		}
		items, err := objects(out)
		return edgesOutput{Edges: items}, err
	})
	add(s, "upsert_edge", "Create or update an edge between nested source and target node references.", writing, func(ctx context.Context, in upsertEdgeInput) (map[string]any, error) {
		source, err := knowledgeRef(in.Source, "source")
		if err != nil {
			return nil, err
		}
		target, err := knowledgeRef(in.Target, "target")
		if err != nil {
			return nil, err
		}
		out, err := edges.UpsertEdge(ctx, in.Kind, source, target, rawObject(in.Data), rawObject(in.Metadata), in.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		return object(out)
	})
	add(s, "delete_edge", "Delete an edge, optionally checking its revision.", writing, func(ctx context.Context, in deleteEdgeInput) (struct{}, error) {
		id, err := parseNonZeroUUID(in.ID, "edge id")
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, edges.DeleteEdge(ctx, in.Kind, id, in.ExpectedRevision)
	})
	add(s, "get_edge_history", "Read a newest-first page of immutable edge versions.", readOnly, func(ctx context.Context, in edgeHistoryInput) (map[string]any, error) {
		id, err := historyID(in.ID, in.Before, in.Limit, "edge")
		if err != nil {
			return nil, err
		}
		out, err := edges.EdgeHistory(ctx, in.Kind, id, in.Before, in.Limit)
		if err != nil {
			return nil, err
		}
		return object(out)
	})
}

func registerSchemaTools(s *mcp.Server, schema Schema) {
	add(s, "get_node_type", "Read a registered node type and its data schema.", readOnly, func(ctx context.Context, in schemaKindInput) (map[string]any, error) {
		out, err := schema.GetNodeType(ctx, in.Kind)
		if err != nil {
			return nil, err
		}
		return object(out)
	})
	add(s, "list_node_types", "List registered node types and their data schemas.", readOnly, func(ctx context.Context, _ struct{}) (nodeTypesOutput, error) {
		out, err := schema.ListNodeTypes(ctx)
		if err != nil {
			return nodeTypesOutput{}, err
		}
		items, err := objects(out)
		return nodeTypesOutput{NodeTypes: items}, err
	})
	add(s, "get_edge_type", "Read a registered edge type, its data schema and permitted endpoints.", readOnly, func(ctx context.Context, in schemaKindInput) (map[string]any, error) {
		out, err := schema.GetEdgeType(ctx, in.Kind)
		if err != nil {
			return nil, err
		}
		return object(out)
	})
	add(s, "list_edge_types", "List registered edge types, their data schemas and permitted endpoints.", readOnly, func(ctx context.Context, _ struct{}) (edgeTypesOutput, error) {
		out, err := schema.ListEdgeTypes(ctx)
		if err != nil {
			return edgeTypesOutput{}, err
		}
		items, err := objects(out)
		return edgeTypesOutput{EdgeTypes: items}, err
	})
}

func registerSchemaAdminTools(s *mcp.Server, schema SchemaAdmin) {
	add(s, "create_node_type", "Create a versioned dynamic node type with a JSON Schema data object.", additive, func(ctx context.Context, in createNodeTypeInput) (map[string]any, error) {
		out, err := schema.CreateNodeType(ctx, in.Kind, rawObject(in.DataSchema))
		if err != nil {
			return nil, err
		}
		return object(out)
	})
	add(s, "create_edge_type", "Create a dynamic edge type with a JSON Schema data object and permitted endpoints.", additive, func(ctx context.Context, in createEdgeTypeInput) (map[string]any, error) {
		out, err := schema.CreateEdgeType(ctx, in.Kind, rawObject(in.DataSchema), in.Endpoints)
		if err != nil {
			return nil, err
		}
		return object(out)
	})
}

func parseNonZeroUUID(value, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse %s: %w", field, err)
	}
	if id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%s must not be zero", field)
	}
	return id, nil
}

func historyID(value string, before int64, limit int, kind string) (uuid.UUID, error) {
	id, err := parseNonZeroUUID(value, kind+" id")
	if err != nil {
		return uuid.Nil, err
	}
	if before < 0 {
		return uuid.Nil, fmt.Errorf("%s history before cursor must be nonnegative", kind)
	}
	if limit < 0 {
		return uuid.Nil, fmt.Errorf("%s history limit must be nonnegative", kind)
	}
	return id, nil
}

func knowledgeRef(in knowledgeRefInput, field string) (domain.KnowledgeRef, error) {
	id, err := parseNonZeroUUID(in.ID, field+" id")
	if err != nil {
		return domain.KnowledgeRef{}, err
	}
	return domain.KnowledgeRef{ID: id, Kind: in.Kind}, nil
}
