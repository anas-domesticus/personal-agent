// Package memory provides long-term memory tools.
package memory

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

func Tools(c *catalog.Catalog) ([]tool.InvokableTool, error) {
	return kit.Build(
		func() (tool.InvokableTool, error) {
			return kit.New("search_memories",
				"Search long-term memory by meaning and keywords. Returns the best matches first.",
				func(ctx context.Context, in searchIn) ([]memoryOut, error) {
					rows, err := c.SearchMemories(ctx, in.Query, in.Tags, kit.Limit(in.Limit, 10))
					if err != nil {
						return nil, err
					}
					out := make([]memoryOut, len(rows))
					for i, r := range rows {
						out[i] = memoryOut{ID: r.ID, Content: r.Content, Tags: r.Tags, Source: r.Source, CreatedAt: r.CreatedAt, Score: r.Score}
					}
					return out, nil
				})
		},
		func() (tool.InvokableTool, error) {
			return kit.New("remember",
				"Save a fact, preference or observation to long-term memory. Keep each memory to one self-contained statement.",
				func(ctx context.Context, in rememberIn) (rememberOut, error) {
					source := "agent"
					if name := kit.Agent(ctx); name != "" {
						source = "agent:" + name
					}
					id, err := c.Remember(ctx, in.Content, in.Tags, source)
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

type memoryOut struct {
	ID        uuid.UUID `json:"id"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	Score     float64   `json:"score"`
}

type rememberIn struct {
	Content string   `json:"content" jsonschema_description:"The memory, as one self-contained statement."`
	Tags    []string `json:"tags,omitempty" jsonschema_description:"Short lowercase tags, e.g. home, work, preference."`
}

type rememberOut struct {
	ID uuid.UUID `json:"id"`
}
