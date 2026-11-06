package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captured is what the test server saw of a request.
type captured struct {
	method, path, auth, contentType string
	body                            map[string]any
}

// server answers every request with status and body, and passes what it saw
// on the returned channel.
func server(t *testing.T, status int, body string) (*httptest.Server, <-chan captured) {
	t.Helper()
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := captured{method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization"), contentType: r.Header.Get("Content-Type")}
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&c.body), "decoding request")
		got <- c
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

const okBody = `{
  "choices": [{"message": {"role": "assistant", "content": " The answer. ",
    "annotations": [
      {"type": "url_citation", "url_citation": {"url": "https://a.example", "title": "A", "content": "about a", "start_index": 0, "end_index": 3}},
      {"type": "url_citation", "url_citation": {"url": "https://a.example", "title": "A again", "content": "dup"}},
      {"type": "other"},
      {"type": "url_citation", "url_citation": {"url": "https://b.example", "title": "B", "content": "about b"}},
      {"type": "url_citation", "url_citation": {"url": "https://c.example", "title": "C", "content": "about c"}}
    ]}}]
}`

var (
	resultA = Result{Title: "A", URL: "https://a.example", Snippet: "about a"}
	resultB = Result{Title: "B", URL: "https://b.example", Snippet: "about b"}
	resultC = Result{Title: "C", URL: "https://c.example", Snippet: "about c"}
)

func TestOpenRouterSearch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		engine      string
		query       Query
		wantPlugin  map[string]any
		wantResults []Result
	}{
		{
			name:        "engine and max results",
			engine:      "exa",
			query:       Query{Text: "go news", MaxResults: 2},
			wantPlugin:  map[string]any{"id": "web", "max_results": float64(2), "engine": "exa"},
			wantResults: []Result{resultA, resultB},
		},
		{
			name:        "defaults omit the engine",
			query:       Query{Text: "go news"},
			wantPlugin:  map[string]any{"id": "web", "max_results": float64(DefaultMaxResults)},
			wantResults: []Result{resultA, resultB, resultC},
		},
		{
			name:        "max results are clamped",
			query:       Query{Text: "go news", MaxResults: 50},
			wantPlugin:  map[string]any{"id": "web", "max_results": float64(MaxMaxResults)},
			wantResults: []Result{resultA, resultB, resultC},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, got := server(t, http.StatusOK, okBody)
			s := NewOpenRouter(OpenRouterConfig{APIKey: "key", Model: "m/x", BaseURL: srv.URL + "/", Engine: tt.engine})

			res, err := s.Search(context.Background(), tt.query)
			require.NoError(t, err)
			assert.Equal(t, Response{Answer: "The answer.", Results: tt.wantResults}, res)

			req := <-got
			assert.Equal(t, http.MethodPost, req.method)
			assert.Equal(t, "/chat/completions", req.path)
			assert.Equal(t, "Bearer key", req.auth)
			assert.Equal(t, "application/json", req.contentType)
			assert.Equal(t, map[string]any{
				"model": "m/x",
				"messages": []any{
					map[string]any{"role": "system", "content": systemPrompt},
					map[string]any{"role": "user", "content": "go news"},
				},
				"plugins": []any{tt.wantPlugin},
			}, req.body)
		})
	}
}

func TestOpenRouterErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "http error with message", status: 401, body: `{"error":{"code":401,"message":"No auth credentials found"}}`, wantErr: "No auth credentials found"},
		{name: "http error plain body", status: 502, body: `bad gateway`, wantErr: "bad gateway"},
		{name: "error in 200", status: 200, body: `{"error":{"code":"x","message":"provider failed"}}`, wantErr: "provider failed"},
		{name: "no choices", status: 200, body: `{"choices":[]}`, wantErr: "no choices"},
		{name: "bad json", status: 200, body: `nope`, wantErr: "decoding"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := server(t, tt.status, tt.body)
			s := NewOpenRouter(OpenRouterConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
			_, err := s.Search(context.Background(), Query{Text: "q"})
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestOpenRouterEmptyQuery(t *testing.T) {
	t.Parallel()
	s := NewOpenRouter(OpenRouterConfig{APIKey: "k", Model: "m", BaseURL: "http://127.0.0.1:0"})
	_, err := s.Search(context.Background(), Query{Text: "  "})
	require.ErrorContains(t, err, "search query is empty")
}

func TestUnavailable(t *testing.T) {
	t.Parallel()
	_, err := Unavailable{}.Search(context.Background(), Query{Text: "q"})
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestClampResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		n    int
		want int
	}{
		{name: "zero uses the default", n: 0, want: DefaultMaxResults},
		{name: "negative uses the default", n: -1, want: DefaultMaxResults},
		{name: "in range", n: 3, want: 3},
		{name: "over the maximum", n: 99, want: MaxMaxResults},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ClampResults(tt.n))
		})
	}
}
