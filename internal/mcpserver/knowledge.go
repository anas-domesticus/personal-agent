package mcpserver

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/anas-domesticus/personal-agent/internal/domain"
)

type getKnowledgeNodeInput struct {
	Kind string `json:"kind" jsonschema:"Predefined node kind: objective, workflow, claim, source or agent."`
	ID   string `json:"id" jsonschema:"Node UUID."`
}

type listKnowledgeNodesInput struct {
	Kind  string `json:"kind,omitempty" jsonschema:"Optional predefined node kind to restrict results."`
	Limit int32  `json:"limit,omitempty" jsonschema:"Maximum results, from 1 to 100. Defaults to 10."`
}

type knowledgeNodesOutput struct {
	Nodes []map[string]any `json:"nodes"`
}

type upsertKnowledgeNodeInput struct {
	Kind             string         `json:"kind" jsonschema:"Predefined node kind: objective, workflow, claim, source or agent."`
	ID               string         `json:"id,omitempty" jsonschema:"Existing node id to update. Omit to create a node."`
	ExpectedRevision *int64         `json:"expected_revision,omitempty" jsonschema:"Required current revision for an optimistic-concurrency update."`
	Data             map[string]any `json:"data" jsonschema:"Node data, including title and description plus fields required by the selected kind."`
}

type searchKnowledgeNodesInput struct {
	Query string `json:"query" jsonschema:"Meaning to compare with node titles and descriptions."`
	Kind  string `json:"kind,omitempty" jsonschema:"Optional predefined node kind to restrict results."`
	Limit int32  `json:"limit,omitempty" jsonschema:"Maximum results, from 1 to 100. Defaults to 10."`
}

type knowledgeNodeMatchesOutput struct {
	Nodes []map[string]any `json:"nodes"`
}

type addSupportedByInput struct {
	ClaimID  string `json:"claim_id" jsonschema:"Claim node UUID."`
	SourceID string `json:"source_id" jsonschema:"Source node UUID."`
}

type deleteSupportedByInput struct {
	EdgeID string `json:"edge_id" jsonschema:"Supported-by relationship UUID."`
}

type listSupportedByInput struct {
	ClaimID  string `json:"claim_id,omitempty" jsonschema:"Claim node UUID. Specify exactly one selector."`
	SourceID string `json:"source_id,omitempty" jsonschema:"Source node UUID. Specify exactly one selector."`
}

type supportedByEdgesOutput struct {
	Edges []map[string]any `json:"edges"`
}

func registerKnowledgeTools(s *mcp.Server, knowledge Knowledge) {
	add(s, "get_knowledge_node", "Read a predefined knowledge node's current data by kind and UUID.", readOnly, func(ctx context.Context, in getKnowledgeNodeInput) (map[string]any, error) {
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, fmt.Errorf("parse knowledge node id: %w", err)
		}
		node, err := knowledge.Get(ctx, in.Kind, id)
		if err != nil {
			return nil, err
		}
		return object(node)
	})
	add(s, "list_knowledge_nodes", "List current knowledge nodes in deterministic creation order, optionally filtered by kind.", readOnly, func(ctx context.Context, in listKnowledgeNodesInput) (knowledgeNodesOutput, error) {
		out, err := knowledge.List(ctx, in.Kind, boundedLimit(in.Limit))
		if err != nil {
			return knowledgeNodesOutput{}, err
		}
		nodes, err := objects(out)
		return knowledgeNodesOutput{Nodes: nodes}, err
	})
	add(s, "upsert_knowledge_node", "Create or update a predefined knowledge node and queue its title and description embeddings.", writing, func(ctx context.Context, in upsertKnowledgeNodeInput) (map[string]any, error) {
		var id uuid.UUID
		var err error
		if in.ID != "" {
			id, err = uuid.Parse(in.ID)
			if err != nil {
				return nil, fmt.Errorf("parse knowledge node id: %w", err)
			}
			if id == uuid.Nil {
				return nil, fmt.Errorf("knowledge node id must not be zero")
			}
		} else if in.ExpectedRevision != nil {
			return nil, fmt.Errorf("new knowledge node has an expected revision: %w", domain.ErrConflict)
		}
		node, err := knowledge.Upsert(ctx, in.Kind, id, rawObject(in.Data), in.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		return object(node)
	})
	add(s, "search_knowledge_nodes", "Find current knowledge nodes by semantic similarity to their titles and descriptions.", readOnly, func(ctx context.Context, in searchKnowledgeNodesInput) (knowledgeNodeMatchesOutput, error) {
		out, err := knowledge.Search(ctx, in.Kind, in.Query, boundedLimit(in.Limit))
		if err != nil {
			return knowledgeNodeMatchesOutput{}, err
		}
		nodes, err := objects(out)
		return knowledgeNodeMatchesOutput{Nodes: nodes}, err
	})
	add(s, "add_supported_by", "Create or return the relationship showing that a source supports a claim.", idempotent, func(ctx context.Context, in addSupportedByInput) (map[string]any, error) {
		claimID, err := uuid.Parse(in.ClaimID)
		if err != nil {
			return nil, fmt.Errorf("parse claim id: %w", err)
		}
		sourceID, err := uuid.Parse(in.SourceID)
		if err != nil {
			return nil, fmt.Errorf("parse source id: %w", err)
		}
		edge, err := knowledge.AddSupportedBy(ctx, claimID, sourceID)
		if err != nil {
			return nil, err
		}
		return object(edge)
	})
	add(s, "delete_supported_by", "Delete a claim-to-source support relationship by its edge ID.", writing, func(ctx context.Context, in deleteSupportedByInput) (struct{}, error) {
		edgeID, err := uuid.Parse(in.EdgeID)
		if err != nil {
			return struct{}{}, fmt.Errorf("parse supported-by edge id: %w", err)
		}
		return struct{}{}, knowledge.DeleteSupportedBy(ctx, edgeID)
	})
	add(s, "list_supported_by", "List support relationships for exactly one claim or source in creation order.", readOnly, func(ctx context.Context, in listSupportedByInput) (supportedByEdgesOutput, error) {
		var claimID, sourceID uuid.UUID
		var err error
		if in.ClaimID != "" {
			claimID, err = uuid.Parse(in.ClaimID)
			if err != nil {
				return supportedByEdgesOutput{}, fmt.Errorf("parse claim id: %w", err)
			}
		}
		if in.SourceID != "" {
			sourceID, err = uuid.Parse(in.SourceID)
			if err != nil {
				return supportedByEdgesOutput{}, fmt.Errorf("parse source id: %w", err)
			}
		}
		out, err := knowledge.ListSupportedBy(ctx, claimID, sourceID)
		if err != nil {
			return supportedByEdgesOutput{}, err
		}
		edges, err := objects(out)
		return supportedByEdgesOutput{Edges: edges}, err
	})
}
