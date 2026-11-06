// Package memory provides long-term memory tools.
package memory

import (
	"context"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

// Store is where memories live. *catalog.Memories implements it.
type Store interface {
	Remember(ctx context.Context, content string, tags []string, source string) (uuid.UUID, error)
	Search(ctx context.Context, query string, tags []string, limit int32) ([]domain.MemoryMatch, error)
}

func Tools(s Store) ([]tool.InvokableTool, error) {
	return kit.Build(
		func() (tool.InvokableTool, error) {
			return kit.New("search_memories",
				"Search long-term memory by meaning and keywords. Returns the best matches first.",
				func(ctx context.Context, in searchIn) ([]domain.MemoryMatch, error) {
					return s.Search(ctx, in.Query, in.Tags, kit.Limit(in.Limit, 10))
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("remember",
				"Save a fact, preference or observation to long-term memory. Keep each memory to one self-contained statement.",
				func(ctx context.Context, in rememberIn) (rememberOut, error) {
					source := "agent"
					if name := domain.Caller(ctx); name != "" {
						source = "agent:" + name
					}
					id, err := s.Remember(ctx, in.Content, in.Tags, source)
					return rememberOut{ID: id}, err
				})
		},
	)
}

type searchIn struct {
	Query string   `json:"query" jsonschema_description:"What to look for, in natural language."`
	Tags  []string `json:"tags,omitempty" jsonschema_description:"Only return memories that have all of these tags."`
	Limit int32    `json:"limit,omitempty" jsonschema_description:"Maximum results, default 10."`
}

type rememberIn struct {
	Content string   `json:"content" jsonschema_description:"The memory, as one self-contained statement."`
	Tags    []string `json:"tags,omitempty" jsonschema_description:"Short lowercase tags, e.g. home, work, preference."`
}

type rememberOut struct {
	ID uuid.UUID `json:"id"`
}
