package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/store"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

// Memories stores long-term memories.
type Memories struct {
	st     *store.Store
	jobs   JobInserter
	search Search
}

func NewMemories(st *store.Store, jobs JobInserter, search Search) *Memories {
	return &Memories{st: st, jobs: jobs, search: search}
}

// Remember saves a memory and queues its embedding.
func (m *Memories) Remember(ctx context.Context, content string, tags []string, source string) (uuid.UUID, error) {
	if strings.TrimSpace(content) == "" {
		return uuid.Nil, errors.New("content is required")
	}
	if tags == nil {
		tags = []string{}
	}
	var id uuid.UUID
	err := m.st.InTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		row, err := q.CreateMemory(ctx, db.CreateMemoryParams{
			Content: content, Tags: tags, Source: source, Metadata: json.RawMessage(`{}`),
		})
		if err != nil {
			return err
		}
		id = row.ID
		if _, err := m.jobs.InsertTx(ctx, tx, embed.Args{Target: embed.TargetMemory, ID: row.ID}, nil); err != nil {
			return fmt.Errorf("queue embed job: %w", err)
		}
		return nil
	})
	return id, err
}

// Search finds memories by meaning and keywords. Memories must have every
// given tag.
func (m *Memories) Search(ctx context.Context, query string, tags []string, limit int32) ([]domain.MemoryMatch, error) {
	vec, semantic := m.search.vector(ctx, query)
	if tags == nil {
		tags = []string{}
	}
	rows, err := m.st.SearchMemories(ctx, db.SearchMemoriesParams{
		UseSemantic: semantic, Embedding: vec, Model: m.search.Model, Query: query, Tags: tags,
		Candidates: limit * 4, Lim: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]domain.MemoryMatch, len(rows))
	for i, r := range rows {
		out[i] = domain.MemoryMatch{
			Memory: domain.Memory{ID: r.ID, Content: r.Content, Tags: r.Tags, Source: r.Source, Metadata: r.Metadata, CreatedAt: r.CreatedAt},
			Score:  r.Score,
		}
	}
	return out, nil
}
