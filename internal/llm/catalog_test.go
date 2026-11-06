package llm_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/llm"
)

const modelList = `{"data": [
	{"id": "a/tools", "supported_parameters": ["tools", "temperature"]},
	{"id": "b/plain", "supported_parameters": ["temperature"]}]}`

// modelServer serves body with status at /models and counts the requests.
func modelServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/models", r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestCatalogCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		body    string
		slug    string
		wantErr string
	}{
		{name: "supports tools", status: http.StatusOK, body: modelList, slug: "a/tools"},
		{name: "no tool calling", status: http.StatusOK, body: modelList, slug: "b/plain", wantErr: "doesn't support tool calling"},
		{name: "unknown model", status: http.StatusOK, body: modelList, slug: "c/missing", wantErr: "isn't on OpenRouter"},
		{name: "list unavailable allows any model", status: http.StatusInternalServerError, body: "down", slug: "c/missing"},
		{name: "bad list allows any model", status: http.StatusOK, body: "nope", slug: "c/missing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := modelServer(t, tt.status, tt.body)
			c := &llm.Catalog{BaseURL: srv.URL}
			err := c.Check(context.Background(), tt.slug)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCatalogCachesTheList(t *testing.T) {
	t.Parallel()
	srv, calls := modelServer(t, http.StatusOK, modelList)
	c := &llm.Catalog{BaseURL: srv.URL}
	ctx := context.Background()

	require.NoError(t, c.Check(ctx, "a/tools"))
	require.Error(t, c.Check(ctx, "b/plain"))
	require.Error(t, c.Check(ctx, "c/missing"))
	assert.Equal(t, int32(1), calls.Load(), "list should be fetched once and cached")
}
