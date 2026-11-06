package catalog

import (
	"context"
	"log/slog"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/pgvector/pgvector-go"

	"github.com/anas-domesticus/personal-agent/internal/embed"
)

// Search embeds queries for hybrid search.
type Search struct {
	// Embedder is optional. Without it, or when it fails, search is
	// keyword-only.
	Embedder embedding.Embedder
	// Model is the embedding model; only vectors from it are compared.
	Model string
}

// vector embeds a query. It returns false, and a placeholder vector, when
// search should fall back to keywords.
func (s Search) vector(ctx context.Context, query string) (pgvector.Vector, bool) {
	placeholder := pgvector.NewVector(make([]float32, embed.Dimensions))
	if s.Embedder == nil {
		return placeholder, false
	}
	vec, err := embed.Text(ctx, s.Embedder, query)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("embedding a search query failed; using keyword search only", "err", err)
		}
		return placeholder, false
	}
	return vec, true
}
