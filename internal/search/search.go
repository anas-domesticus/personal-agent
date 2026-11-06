// Package search is a swappable web search backend for the agent's tools.
package search

import (
	"context"
	"errors"
)

// Searcher runs a web search.
type Searcher interface {
	Search(ctx context.Context, q Query) (Response, error)
}

// Query is one web search.
type Query struct {
	Text string
	// MaxResults caps the results; zero means DefaultMaxResults.
	MaxResults int
}

// Response is a search's answer and the sources it came from.
type Response struct {
	Answer  string   `json:"answer,omitempty"`
	Results []Result `json:"results"`
}

// Result is one source.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// ErrUnavailable is returned when no search backend is configured.
var ErrUnavailable = errors.New("web search is not configured")

const (
	// DefaultMaxResults is used when a query gives no MaxResults.
	DefaultMaxResults = 5
	// MaxMaxResults is the most results a query can ask for.
	MaxMaxResults = 10
)

// ClampResults clamps n to [1, MaxMaxResults], using DefaultMaxResults for
// zero or less.
func ClampResults(n int) int {
	if n <= 0 {
		return DefaultMaxResults
	}
	return min(n, MaxMaxResults)
}

// Unavailable is a Searcher for when search isn't configured.
type Unavailable struct{}

// Search always returns ErrUnavailable.
func (Unavailable) Search(context.Context, Query) (Response, error) {
	return Response{}, ErrUnavailable
}
