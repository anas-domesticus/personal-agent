// Package knowledge provides native tools for writing and searching the
// application's predefined knowledge nodes.
package knowledge

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

var (
	//go:embed desc/upsert_knowledge_node.md
	upsertDesc string
	//go:embed desc/search_knowledge_nodes.md
	searchDesc string
)

// Store writes and searches predefined knowledge nodes. *app.Knowledge implements it.
type Store interface {
	Upsert(ctx context.Context, kind string, id uuid.UUID, data json.RawMessage, expectedRevision *int64) (domain.KnowledgeNode, error)
	Search(ctx context.Context, kind, query string, limit int32) ([]domain.KnowledgeNodeMatch, error)
}

// Tools returns the native knowledge-node upsert and semantic-search tools.
func Tools(store Store) ([]tool.InvokableTool, error) {
	return kit.Build(
		func() (tool.InvokableTool, error) {
			return kit.New("upsert_knowledge_node", upsertDesc, func(ctx context.Context, in upsertInput) (domain.KnowledgeNode, error) {
				return upsert(ctx, store, in)
			})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("search_knowledge_nodes", searchDesc, func(ctx context.Context, in searchInput) (searchOutput, error) {
				nodes, err := store.Search(ctx, in.Kind, in.Query, boundedLimit(in.Limit))
				return searchOutput{Nodes: nodes}, err
			})
		},
	)
}

type upsertInput struct {
	Kind             string         `json:"kind" jsonschema_description:"Predefined node kind: objective, workflow, claim, source or agent."`
	ID               string         `json:"id,omitempty" jsonschema_description:"Existing node id to update. Omit to create a node."`
	ExpectedRevision *int64         `json:"expected_revision,omitempty" jsonschema_description:"Required current revision for an optimistic-concurrency update. Must be omitted when creating a node."`
	Data             map[string]any `json:"data" jsonschema_description:"Node data, including title and description plus fields required by the selected kind."`
}

func upsert(ctx context.Context, store Store, in upsertInput) (domain.KnowledgeNode, error) {
	var id uuid.UUID
	var err error
	if in.ID != "" {
		id, err = uuid.Parse(in.ID)
		if err != nil {
			return domain.KnowledgeNode{}, fmt.Errorf("parse knowledge node id: %w", err)
		}
	} else if in.ExpectedRevision != nil {
		return domain.KnowledgeNode{}, fmt.Errorf("new knowledge node has an expected revision: %w", domain.ErrConflict)
	}
	data, err := json.Marshal(in.Data)
	if err != nil {
		return domain.KnowledgeNode{}, fmt.Errorf("encode knowledge node data: %w", err)
	}
	return store.Upsert(ctx, in.Kind, id, data, in.ExpectedRevision)
}

type searchInput struct {
	Query string `json:"query" jsonschema_description:"Meaning to compare with node titles and descriptions."`
	Kind  string `json:"kind,omitempty" jsonschema_description:"Optional predefined node kind to restrict results."`
	Limit int32  `json:"limit,omitempty" jsonschema_description:"Maximum results. Defaults to 10 and is capped at 100."`
}

type searchOutput struct {
	Nodes []domain.KnowledgeNodeMatch `json:"nodes"`
}

func boundedLimit(limit int32) int32 {
	if limit <= 0 {
		return 10
	}
	return min(limit, 100)
}
