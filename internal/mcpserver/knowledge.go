package mcpserver

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/anas-domesticus/personal-agent/internal/domain"
)

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

func registerKnowledgeTools(s *mcp.Server, knowledge Knowledge) {
	add(s, "upsert_knowledge_node", "Create or update a predefined knowledge node and queue its title and description embeddings.", writing, func(ctx context.Context, in upsertKnowledgeNodeInput) (map[string]any, error) {
		var id uuid.UUID
		var err error
		if in.ID != "" {
			id, err = uuid.Parse(in.ID)
			if err != nil {
				return nil, fmt.Errorf("parse knowledge node id: %w", err)
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
}
