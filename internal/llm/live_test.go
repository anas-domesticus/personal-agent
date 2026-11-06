//go:build live

package llm_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/llm"
)

// Calls the real OpenRouter API, so it only runs when OPENROUTER_API_KEY is set.
func TestLiveEmbed(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	ctx := context.Background()

	e, err := llm.NewEmbedder(ctx, llm.Config{APIKey: key}, "openai/text-embedding-3-small")
	require.NoError(t, err)
	got, err := e.EmbedStrings(ctx, []string{"turn on the hall light", "triage my inbox"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	for i, v := range got {
		assert.Len(t, v, 1536, "embedding %d", i)
	}
}
