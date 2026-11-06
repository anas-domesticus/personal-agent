// Package llm builds Eino model components backed by OpenRouter.
//
// Models use OpenRouter slugs, e.g. "openai/text-embedding-3-small" or
// "anthropic/claude-sonnet-4.5".
package llm

import (
	"context"
	"fmt"
	"time"

	embedopenai "github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/cloudwego/eino-ext/components/model/openrouter"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
)

const DefaultBaseURL = "https://openrouter.ai/api/v1"

type Config struct {
	APIKey  string
	BaseURL string // defaults to DefaultBaseURL
}

func (c Config) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

// NewChatModel returns a tool-calling chat model for the given slug.
func NewChatModel(ctx context.Context, cfg Config, slug string) (model.ToolCallingChatModel, error) {
	m, err := openrouter.NewChatModel(ctx, &openrouter.Config{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.baseURL(),
		Model:   slug,
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("chat model %s: %w", slug, err)
	}
	return m, nil
}

// NewEmbedder returns an embedder for the given slug. OpenRouter's embeddings
// endpoint is OpenAI-compatible, so Eino's OpenAI embedder works with a
// different base URL.
func NewEmbedder(ctx context.Context, cfg Config, slug string) (embedding.Embedder, error) {
	e, err := embedopenai.NewEmbedder(ctx, &embedopenai.EmbeddingConfig{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.baseURL(),
		Model:   slug,
		Timeout: time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("embedder %s: %w", slug, err)
	}
	return e, nil
}

// Float32s converts Eino's float64 embeddings to the float32 vectors pgvector stores.
func Float32s(in []float64) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v)
	}
	return out
}
