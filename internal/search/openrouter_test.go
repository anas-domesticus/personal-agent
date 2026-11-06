package search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func server(t *testing.T, status int, body string, check func(*http.Request, map[string]any)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		if check != nil {
			check(r, req)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
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

func TestOpenRouterRequestAndParse(t *testing.T) {
	srv := server(t, 200, okBody, func(r *http.Request, req map[string]any) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer key" {
			t.Errorf("auth = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content-type = %q", got)
		}
		if req["model"] != "m/x" {
			t.Errorf("model = %v", req["model"])
		}
		msgs := req["messages"].([]any)
		if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" ||
			msgs[1].(map[string]any)["content"] != "go news" {
			t.Errorf("messages = %v", msgs)
		}
		plugins := req["plugins"].([]any)
		p := plugins[0].(map[string]any)
		if p["id"] != "web" || p["max_results"] != float64(2) || p["engine"] != "exa" {
			t.Errorf("plugin = %v", p)
		}
	})
	s := NewOpenRouter(OpenRouterConfig{APIKey: "key", Model: "m/x", BaseURL: srv.URL + "/", Engine: "exa"})
	res, err := s.Search(context.Background(), Query{Text: "go news", MaxResults: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != "The answer." {
		t.Errorf("answer = %q", res.Answer)
	}
	want := []Result{
		{Title: "A", URL: "https://a.example", Snippet: "about a"},
		{Title: "B", URL: "https://b.example", Snippet: "about b"},
	}
	if len(res.Results) != len(want) {
		t.Fatalf("results = %+v", res.Results)
	}
	for i := range want {
		if res.Results[i] != want[i] {
			t.Errorf("result %d = %+v, want %+v", i, res.Results[i], want[i])
		}
	}
}

func TestOpenRouterDefaults(t *testing.T) {
	srv := server(t, 200, okBody, func(_ *http.Request, req map[string]any) {
		p := req["plugins"].([]any)[0].(map[string]any)
		if p["max_results"] != float64(DefaultMaxResults) {
			t.Errorf("max_results = %v", p["max_results"])
		}
		if _, ok := p["engine"]; ok {
			t.Errorf("engine should be omitted: %v", p)
		}
	})
	s := NewOpenRouter(OpenRouterConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
	res, err := s.Search(context.Background(), Query{Text: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 3 {
		t.Errorf("results = %+v", res.Results)
	}
}

func TestOpenRouterClamp(t *testing.T) {
	srv := server(t, 200, okBody, func(_ *http.Request, req map[string]any) {
		p := req["plugins"].([]any)[0].(map[string]any)
		if p["max_results"] != float64(MaxMaxResults) {
			t.Errorf("max_results = %v", p["max_results"])
		}
	})
	s := NewOpenRouter(OpenRouterConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
	if _, err := s.Search(context.Background(), Query{Text: "q", MaxResults: 50}); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRouterErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"http error with message", 401, `{"error":{"code":401,"message":"No auth credentials found"}}`, "No auth credentials found"},
		{"http error plain body", 502, `bad gateway`, "bad gateway"},
		{"error in 200", 200, `{"error":{"code":"x","message":"provider failed"}}`, "provider failed"},
		{"no choices", 200, `{"choices":[]}`, "no choices"},
		{"bad json", 200, `nope`, "decoding"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := server(t, tc.status, tc.body, nil)
			s := NewOpenRouter(OpenRouterConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
			_, err := s.Search(context.Background(), Query{Text: "q"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestOpenRouterEmptyQuery(t *testing.T) {
	s := NewOpenRouter(OpenRouterConfig{APIKey: "k", Model: "m", BaseURL: "http://127.0.0.1:0"})
	if _, err := s.Search(context.Background(), Query{Text: "  "}); err == nil {
		t.Fatal("want error")
	}
}

func TestUnavailable(t *testing.T) {
	_, err := Unavailable{}.Search(context.Background(), Query{Text: "q"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
