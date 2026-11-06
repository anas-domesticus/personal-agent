package llm_test

import (
	"context"
	"os"
	"testing"

	"github.com/anas-domesticus/personal-agent/internal/llm"
)

// Calls the real OpenRouter API, so it only runs when OPENROUTER_API_KEY is set.
func TestEmbedOpenRouter(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	ctx := context.Background()

	e, err := llm.NewEmbedder(ctx, llm.Config{APIKey: key}, "openai/text-embedding-3-small")
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.EmbedStrings(ctx, []string{"turn on the hall light", "triage my inbox"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d embeddings, want 2", len(got))
	}
	for i, v := range got {
		if len(v) != 1536 {
			t.Fatalf("embedding %d has %d dimensions, want 1536", i, len(v))
		}
	}
}
