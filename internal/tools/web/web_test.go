package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/search"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

func specs(t *testing.T, s Searcher, c *http.Client) map[string]kit.Spec {
	t.Helper()
	ss, err := Specs(s, c)
	require.NoError(t, err)
	out := map[string]kit.Spec{}
	for _, sp := range ss {
		out[sp.Name] = sp
	}
	require.Len(t, out, 2)
	require.Contains(t, out, "web_search")
	require.Contains(t, out, "web_fetch")
	return out
}

func build(t *testing.T, sp kit.Spec, settings string) tool.InvokableTool {
	t.Helper()
	v, err := sp.DecodeSettings(json.RawMessage(settings))
	require.NoError(t, err)
	tl, err := sp.New(v)
	require.NoError(t, err)
	require.Equal(t, sp.Name, kit.Name(tl))
	return tl
}

type result[D any] struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Data  D      `json:"data"`
}

func run[D any](t *testing.T, tl tool.InvokableTool, args any) result[D] {
	t.Helper()
	b, err := json.Marshal(args)
	require.NoError(t, err)
	out, err := tl.InvokableRun(context.Background(), string(b))
	require.NoError(t, err)
	var r result[D]
	require.NoError(t, json.Unmarshal([]byte(out), &r), "decoding %q", out)
	return r
}

func TestSearchDescriptionHasTheYear(t *testing.T) {
	t.Parallel()
	tl := build(t, specs(t, NewMockSearcher(t), nil)["web_search"], "true")
	info, err := tl.Info(context.Background())
	require.NoError(t, err)
	assert.Contains(t, info.Desc, fmt.Sprintf("The current year is %d.", time.Now().Year()))
}

func TestSearch(t *testing.T) {
	t.Parallel()
	three := []search.Result{
		{Title: "A", URL: "https://a", Snippet: "a"},
		{Title: "B", URL: "https://b", Snippet: "b"},
		{Title: "C", URL: "https://c", Snippet: "c"},
	}
	tests := []struct {
		name      string
		settings  string
		args      map[string]any
		wantQuery *search.Query // nil means the searcher isn't called
		resp      search.Response
		err       error
		want      search.Response
		wantErr   string
	}{
		{
			name:      "trims the query and uses the default count",
			settings:  "true",
			args:      map[string]any{"query": " news "},
			wantQuery: &search.Query{Text: "news", MaxResults: search.DefaultMaxResults},
			resp:      search.Response{Answer: "yes", Results: three},
			want:      search.Response{Answer: "yes", Results: three},
		},
		{
			name:      "clamps max_results",
			settings:  "true",
			args:      map[string]any{"query": "q", "max_results": 99},
			wantQuery: &search.Query{Text: "q", MaxResults: search.MaxMaxResults},
			want:      search.Response{Results: []search.Result{}},
		},
		{
			name:      "settings cap the count and the results",
			settings:  `{"max_results": 2}`,
			args:      map[string]any{"query": "q", "max_results": 8},
			wantQuery: &search.Query{Text: "q", MaxResults: 2},
			resp:      search.Response{Results: three},
			want:      search.Response{Results: three[:2]},
		},
		{
			name:     "empty query",
			settings: "true",
			args:     map[string]any{"query": " "},
			wantErr:  "query is required",
		},
		{
			name:      "searcher error",
			settings:  "true",
			args:      map[string]any{"query": "q"},
			wantQuery: &search.Query{Text: "q", MaxResults: search.DefaultMaxResults},
			err:       errors.New("boom"),
			wantErr:   "boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := NewMockSearcher(t)
			if tt.wantQuery != nil {
				s.EXPECT().Search(mock.Anything, *tt.wantQuery).Return(tt.resp, tt.err).Once()
			}
			tl := build(t, specs(t, s, nil)["web_search"], tt.settings)

			r := run[search.Response](t, tl, tt.args)
			if tt.wantErr != "" {
				assert.False(t, r.OK)
				assert.Equal(t, tt.wantErr, r.Error)
				return
			}
			require.True(t, r.OK, r.Error)
			assert.Equal(t, tt.want, r.Data)
		})
	}
}

func TestSearchUnavailable(t *testing.T) {
	t.Parallel()
	for _, s := range []Searcher{search.Unavailable{}, nil} {
		tl := build(t, specs(t, s, nil)["web_search"], "true")
		r := run[search.Response](t, tl, map[string]any{"query": "q"})
		assert.False(t, r.OK)
		assert.Equal(t, search.ErrUnavailable.Error(), r.Error)
	}
}

func TestSearchSettings(t *testing.T) {
	t.Parallel()
	sp := specs(t, NewMockSearcher(t), nil)["web_search"]
	tests := []struct {
		name    string
		raw     string
		want    any
		wantErr string
	}{
		{name: "true", raw: "true", want: &SearchSettings{}},
		{name: "in range", raw: `{"max_results": 3}`, want: &SearchSettings{MaxResults: 3}},
		{name: "upper bound", raw: `{"max_results": 10}`, want: &SearchSettings{MaxResults: 10}},
		{name: "over the maximum", raw: `{"max_results": 11}`, wantErr: "max_results must be between 0 and 10"},
		{name: "negative", raw: `{"max_results": -1}`, wantErr: "max_results must be between 0 and 10"},
		{name: "unknown field", raw: `{"bogus": 1}`, wantErr: "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := sp.DecodeSettings(json.RawMessage(tt.raw))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

const page = `<!doctype html><html><head><meta charset="utf-8"><title>T</title>
<style>body{color:red}</style><script>var x = 1;</script></head>
<body><h1>Hello</h1><p>Some <b>bold</b> text and a <a href="/next">link</a>.</p>
<noscript>enable js</noscript></body></html>`

// write sends a test response body, failing the test if it can't.
func write(t *testing.T, w http.ResponseWriter, b []byte) {
	t.Helper()
	_, err := w.Write(b)
	assert.NoError(t, err)
}

func fetchServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, UserAgent, r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		write(t, w, []byte(page))
	})
	mux.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		write(t, w, []byte(`{"a":"<b>1</b>"}`))
	})
	mux.HandleFunc("/image", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		write(t, w, []byte{0x89, 'P', 'N', 'G'})
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		write(t, w, []byte(strings.Repeat("x", MaxFetchBytes+1)))
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/page", http.StatusFound)
	})
	mux.HandleFunc("/latin1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=iso-8859-1")
		write(t, w, []byte{'c', 'a', 'f', 0xe9})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetch(t *testing.T) {
	t.Parallel()
	srv := fetchServer(t)
	tl := build(t, specs(t, NewMockSearcher(t), srv.Client())["web_fetch"], "true")

	tests := []struct {
		name            string
		args            map[string]any
		wantURL         string   // empty means don't check
		wantContentType string   // prefix; empty means don't check
		wantContent     string   // exact; empty means don't check
		wantContains    []string // substrings of the content
		wantNotContains []string
		wantErr         string
	}{
		{
			name:            "markdown by default",
			args:            map[string]any{"url": srv.URL + "/page"},
			wantURL:         srv.URL + "/page",
			wantContentType: "text/html",
			wantContains:    []string{"# Hello", "**bold**", "[link](" + srv.URL + "/next)"},
			wantNotContains: []string{"var x", "color:red"},
		},
		{
			name:        "text",
			args:        map[string]any{"url": srv.URL + "/page", "format": "text"},
			wantContent: "Hello\n\nSome bold text and a link.",
		},
		{
			name:        "html",
			args:        map[string]any{"url": srv.URL + "/page", "format": "html"},
			wantContent: page,
		},
		{
			name:        "json as-is",
			args:        map[string]any{"url": srv.URL + "/json"},
			wantContent: `{"a":"<b>1</b>"}`,
		},
		{
			name:    "redirect reports the final url",
			args:    map[string]any{"url": srv.URL + "/redirect"},
			wantURL: srv.URL + "/page",
		},
		{
			name:        "charset is decoded",
			args:        map[string]any{"url": srv.URL + "/latin1"},
			wantContent: "café",
		},
		{name: "binary", args: map[string]any{"url": srv.URL + "/image"}, wantErr: "binary"},
		{name: "too large", args: map[string]any{"url": srv.URL + "/big"}, wantErr: "too large"},
		{name: "status", args: map[string]any{"url": srv.URL + "/missing"}, wantErr: "404"},
		{name: "scheme", args: map[string]any{"url": "file:///etc/passwd"}, wantErr: "http"},
		{name: "no host", args: map[string]any{"url": "http://"}, wantErr: "no host"},
		{name: "format", args: map[string]any{"url": srv.URL + "/page", "format": "pdf"}, wantErr: "format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := run[fetchOut](t, tl, tt.args)
			if tt.wantErr != "" {
				assert.False(t, r.OK)
				assert.Contains(t, r.Error, tt.wantErr)
				return
			}
			require.True(t, r.OK, r.Error)
			if tt.wantURL != "" {
				assert.Equal(t, tt.wantURL, r.Data.URL)
			}
			if tt.wantContentType != "" {
				assert.True(t, strings.HasPrefix(r.Data.ContentType, tt.wantContentType), r.Data.ContentType)
			}
			if tt.wantContent != "" {
				assert.Equal(t, tt.wantContent, r.Data.Content)
			}
			for _, s := range tt.wantContains {
				assert.Contains(t, r.Data.Content, s)
			}
			for _, s := range tt.wantNotContains {
				assert.NotContains(t, r.Data.Content, s)
			}
		})
	}
}

func TestFetchTimeout(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := fetch(ctx, srv.Client(), fetchIn{URL: srv.URL})
	require.Error(t, err)
}
