package llm_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-domesticus/personal-agent/internal/llm"
)

func TestCatalog(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"data": [
			{"id": "a/tools", "supported_parameters": ["tools", "temperature"]},
			{"id": "b/plain", "supported_parameters": ["temperature"]}]}`))
	}))
	defer srv.Close()
	c := &llm.Catalog{BaseURL: srv.URL}
	ctx := context.Background()

	if err := c.Check(ctx, "a/tools"); err != nil {
		t.Fatal(err)
	}
	if err := c.Check(ctx, "b/plain"); err == nil || !strings.Contains(err.Error(), "tool calling") {
		t.Fatalf("plain model: %v", err)
	}
	if err := c.Check(ctx, "c/missing"); err == nil || !strings.Contains(err.Error(), "isn't on OpenRouter") {
		t.Fatalf("missing model: %v", err)
	}
	if calls != 1 {
		t.Fatalf("list fetched %d times, want 1 (cached)", calls)
	}
}
