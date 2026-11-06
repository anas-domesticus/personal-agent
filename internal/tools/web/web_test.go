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

	"github.com/anas-domesticus/personal-agent/internal/search"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

type fakeSearcher struct {
	got  []search.Query
	resp search.Response
	err  error
}

func (f *fakeSearcher) Search(_ context.Context, q search.Query) (search.Response, error) {
	f.got = append(f.got, q)
	return f.resp, f.err
}

func specs(t *testing.T, s search.Searcher, c *http.Client) map[string]kit.Spec {
	t.Helper()
	ss, err := Specs(s, c)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]kit.Spec{}
	for _, sp := range ss {
		out[sp.Name] = sp
	}
	if len(out) != 2 || out["web_search"].Name == "" || out["web_fetch"].Name == "" {
		t.Fatalf("specs = %v", out)
	}
	return out
}

func build(t *testing.T, sp kit.Spec, settings string) tool.InvokableTool {
	t.Helper()
	v, err := sp.DecodeSettings(json.RawMessage(settings))
	if err != nil {
		t.Fatal(err)
	}
	tl, err := sp.New(v)
	if err != nil {
		t.Fatal(err)
	}
	if kit.Name(tl) != sp.Name {
		t.Fatalf("tool name %q, spec %q", kit.Name(tl), sp.Name)
	}
	return tl
}

type result[D any] struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Data  D      `json:"data"`
}

func run[D any](t *testing.T, tl tool.InvokableTool, args any) result[D] {
	t.Helper()
	b, _ := json.Marshal(args)
	out, err := tl.InvokableRun(context.Background(), string(b))
	if err != nil {
		t.Fatal(err)
	}
	var r result[D]
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("decoding %q: %v", out, err)
	}
	return r
}

func TestSearch(t *testing.T) {
	f := &fakeSearcher{resp: search.Response{Answer: "yes", Results: []search.Result{
		{Title: "A", URL: "https://a", Snippet: "a"},
		{Title: "B", URL: "https://b", Snippet: "b"},
		{Title: "C", URL: "https://c", Snippet: "c"},
	}}}
	sp := specs(t, f, nil)["web_search"]
	tl := build(t, sp, "true")

	info, _ := tl.Info(context.Background())
	if want := fmt.Sprintf("The current year is %d.", time.Now().Year()); !strings.Contains(info.Desc, want) {
		t.Errorf("description missing %q: %s", want, info.Desc)
	}

	r := run[search.Response](t, tl, map[string]any{"query": " news "})
	if !r.OK || r.Data.Answer != "yes" || len(r.Data.Results) != 3 {
		t.Fatalf("result = %+v", r)
	}
	if f.got[0] != (search.Query{Text: "news", MaxResults: search.DefaultMaxResults}) {
		t.Errorf("query = %+v", f.got[0])
	}

	run[search.Response](t, tl, map[string]any{"query": "q", "max_results": 99})
	if f.got[1].MaxResults != search.MaxMaxResults {
		t.Errorf("max_results = %d", f.got[1].MaxResults)
	}

	if r := run[search.Response](t, tl, map[string]any{"query": " "}); r.OK || r.Error == "" {
		t.Errorf("empty query: %+v", r)
	}
}

func TestSearchSettingsCap(t *testing.T) {
	f := &fakeSearcher{resp: search.Response{Results: []search.Result{{URL: "1"}, {URL: "2"}, {URL: "3"}}}}
	sp := specs(t, f, nil)["web_search"]
	tl := build(t, sp, `{"max_results": 2}`)
	r := run[search.Response](t, tl, map[string]any{"query": "q", "max_results": 8})
	if f.got[0].MaxResults != 2 || len(r.Data.Results) != 2 {
		t.Errorf("query = %+v, results = %+v", f.got[0], r.Data.Results)
	}

	if _, err := sp.DecodeSettings(json.RawMessage(`{"max_results": 11}`)); err == nil {
		t.Error("want error for max_results 11")
	}
	if _, err := sp.DecodeSettings(json.RawMessage(`{"bogus": 1}`)); err == nil {
		t.Error("want error for unknown field")
	}
}

func TestSearchErrors(t *testing.T) {
	tl := build(t, specs(t, search.Unavailable{}, nil)["web_search"], "true")
	r := run[search.Response](t, tl, map[string]any{"query": "q"})
	if r.OK || r.Error != search.ErrUnavailable.Error() {
		t.Errorf("result = %+v", r)
	}

	f := &fakeSearcher{err: errors.New("boom")}
	tl = build(t, specs(t, f, nil)["web_search"], "true")
	if r := run[search.Response](t, tl, map[string]any{"query": "q"}); r.OK || r.Error != "boom" {
		t.Errorf("result = %+v", r)
	}
}

const page = `<!doctype html><html><head><meta charset="utf-8"><title>T</title>
<style>body{color:red}</style><script>var x = 1;</script></head>
<body><h1>Hello</h1><p>Some <b>bold</b> text and a <a href="/next">link</a>.</p>
<noscript>enable js</noscript></body></html>`

func fetchServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != UserAgent {
			t.Errorf("user agent = %q", ua)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, page)
	})
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"a":"<b>1</b>"}`)
	})
	mux.HandleFunc("/image", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 'P', 'N', 'G'})
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(strings.Repeat("x", MaxFetchBytes+1)))
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/page", http.StatusFound)
	})
	mux.HandleFunc("/latin1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=iso-8859-1")
		w.Write([]byte{'c', 'a', 'f', 0xe9})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetch(t *testing.T) {
	srv := fetchServer(t)
	tl := build(t, specs(t, &fakeSearcher{}, srv.Client())["web_fetch"], "true")

	t.Run("markdown", func(t *testing.T) {
		r := run[fetchOut](t, tl, map[string]any{"url": srv.URL + "/page"})
		if !r.OK {
			t.Fatal(r.Error)
		}
		c := r.Data.Content
		for _, want := range []string{"# Hello", "**bold**", "[link](" + srv.URL + "/next)"} {
			if !strings.Contains(c, want) {
				t.Errorf("markdown missing %q:\n%s", want, c)
			}
		}
		for _, bad := range []string{"var x", "color:red"} {
			if strings.Contains(c, bad) {
				t.Errorf("markdown contains %q:\n%s", bad, c)
			}
		}
		if r.Data.URL != srv.URL+"/page" || !strings.HasPrefix(r.Data.ContentType, "text/html") {
			t.Errorf("out = %+v", r.Data)
		}
	})

	t.Run("text", func(t *testing.T) {
		r := run[fetchOut](t, tl, map[string]any{"url": srv.URL + "/page", "format": "text"})
		if !r.OK {
			t.Fatal(r.Error)
		}
		if want := "Hello\n\nSome bold text and a link."; r.Data.Content != want {
			t.Errorf("text = %q, want %q", r.Data.Content, want)
		}
	})

	t.Run("html", func(t *testing.T) {
		r := run[fetchOut](t, tl, map[string]any{"url": srv.URL + "/page", "format": "html"})
		if !r.OK || r.Data.Content != page {
			t.Errorf("result = %+v", r)
		}
	})

	t.Run("json as-is", func(t *testing.T) {
		r := run[fetchOut](t, tl, map[string]any{"url": srv.URL + "/json"})
		if !r.OK || r.Data.Content != `{"a":"<b>1</b>"}` {
			t.Errorf("result = %+v", r)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		r := run[fetchOut](t, tl, map[string]any{"url": srv.URL + "/redirect"})
		if !r.OK || r.Data.URL != srv.URL+"/page" {
			t.Errorf("result = %+v", r)
		}
	})

	t.Run("charset", func(t *testing.T) {
		r := run[fetchOut](t, tl, map[string]any{"url": srv.URL + "/latin1"})
		if !r.OK || r.Data.Content != "café" {
			t.Errorf("result = %+v", r)
		}
	})

	errCases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"binary", map[string]any{"url": srv.URL + "/image"}, "binary"},
		{"too large", map[string]any{"url": srv.URL + "/big"}, "too large"},
		{"status", map[string]any{"url": srv.URL + "/missing"}, "404"},
		{"scheme", map[string]any{"url": "file:///etc/passwd"}, "http"},
		{"format", map[string]any{"url": srv.URL + "/page", "format": "pdf"}, "format"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			r := run[fetchOut](t, tl, tc.args)
			if r.OK || !strings.Contains(r.Error, tc.want) {
				t.Errorf("result = %+v, want error containing %q", r, tc.want)
			}
		})
	}
}

func TestFetchTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := fetch(ctx, srv.Client(), fetchIn{URL: srv.URL})
	if err == nil {
		t.Fatal("want timeout error")
	}
}
