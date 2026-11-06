// Package web provides the web_search and web_fetch tools.
package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"

	"github.com/anas-domesticus/personal-agent/internal/search"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
)

// SearchSettings are web_search's per-agent settings.
type SearchSettings struct {
	// MaxResults caps results per search; 0 means the default.
	MaxResults int `json:"max_results"`
}

// Validate checks the settings.
func (s *SearchSettings) Validate() error {
	if s.MaxResults < 0 || s.MaxResults > search.MaxMaxResults {
		return fmt.Errorf("max_results must be between 0 and %d", search.MaxMaxResults)
	}
	return nil
}

// Specs returns the web_search and web_fetch tool specs. s runs searches
// (use search.Unavailable when none is configured); client fetches pages and
// defaults to http.DefaultClient's transport with no overall timeout (each
// fetch has its own).
func Specs(s search.Searcher, client *http.Client) ([]kit.Spec, error) {
	if s == nil {
		s = search.Unavailable{}
	}
	if client == nil {
		client = &http.Client{}
	}
	fetch, err := newFetchTool(client)
	if err != nil {
		return nil, err
	}
	// Check web_search builds now so a bad schema fails at startup.
	if _, err := newSearchTool(s, nil); err != nil {
		return nil, err
	}
	return []kit.Spec{
		{
			Name:         "web_search",
			Summary:      "Search the web for current information; returns an answer with cited sources.",
			Settings:     func() any { return &SearchSettings{} },
			SettingsHelp: fmt.Sprintf(`{"max_results": int} caps results per search (1-%d; 0 = default %d).`, search.MaxMaxResults, search.DefaultMaxResults),
			New: func(settings any) (tool.InvokableTool, error) {
				ss, _ := settings.(*SearchSettings)
				return newSearchTool(s, ss)
			},
		},
		kit.Static("Fetch a URL and return its content as markdown, text or HTML.", fetch),
	}, nil
}

type searchIn struct {
	Query      string `json:"query" jsonschema_description:"What to search for."`
	MaxResults int    `json:"max_results,omitempty" jsonschema_description:"Maximum sources to return, default 5, at most 10."`
}

func searchDescription() string {
	return fmt.Sprintf(`Search the web. Use this for current information, recent events, or anything that may have changed since your training. Returns a short answer and the sources it cites; use web_fetch to read a source in full.

The current year is %d. Use this year when searching for recent information.`, time.Now().Year())
}

func newSearchTool(s search.Searcher, settings *SearchSettings) (tool.InvokableTool, error) {
	limit := search.MaxMaxResults
	if settings != nil && settings.MaxResults > 0 {
		limit = settings.MaxResults
	}
	return kit.New("web_search", searchDescription(),
		func(ctx context.Context, in searchIn) (search.Response, error) {
			q := strings.TrimSpace(in.Query)
			if q == "" {
				return search.Response{}, errors.New("query is required")
			}
			n := min(search.ClampResults(in.MaxResults), limit)
			res, err := s.Search(ctx, search.Query{Text: q, MaxResults: n})
			if err != nil {
				return search.Response{}, err
			}
			if res.Results == nil {
				res.Results = []search.Result{}
			}
			if len(res.Results) > n {
				res.Results = res.Results[:n]
			}
			return res, nil
		})
}
