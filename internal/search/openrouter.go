package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultOpenRouterBaseURL is OpenRouter's API root.
const DefaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"

const systemPrompt = "Search the web to answer the user's query. Answer concisely and factually, citing your sources."

// OpenRouterConfig configures OpenRouter search.
type OpenRouterConfig struct {
	APIKey string
	// Model answers the query, e.g. "openai/gpt-4o-mini".
	Model string
	// BaseURL defaults to DefaultOpenRouterBaseURL.
	BaseURL string
	// HTTPClient defaults to a client with a 60s timeout.
	HTTPClient *http.Client
	// Engine picks the search engine: native, exa, firecrawl, parallel or
	// perplexity. Empty lets OpenRouter choose (native if the provider
	// supports it, else exa).
	Engine string
}

// OpenRouter searches with OpenRouter's web search plugin.
type OpenRouter struct {
	cfg OpenRouterConfig
}

// NewOpenRouter returns an OpenRouter searcher.
func NewOpenRouter(cfg OpenRouterConfig) *OpenRouter {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultOpenRouterBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &OpenRouter{cfg: cfg}
}

type orMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type orPlugin struct {
	ID         string `json:"id"`
	MaxResults int    `json:"max_results"`
	Engine     string `json:"engine,omitempty"`
}

type orRequest struct {
	Model    string      `json:"model"`
	Messages []orMessage `json:"messages"`
	Plugins  []orPlugin  `json:"plugins"`
}

type orError struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
}

type orResponse struct {
	Error   *orError `json:"error"`
	Choices []struct {
		Message struct {
			Content     string `json:"content"`
			Annotations []struct {
				Type        string `json:"type"`
				URLCitation struct {
					URL     string `json:"url"`
					Title   string `json:"title"`
					Content string `json:"content"`
				} `json:"url_citation"`
			} `json:"annotations"`
		} `json:"message"`
	} `json:"choices"`
}

// Search runs q through the model with web search enabled.
func (o *OpenRouter) Search(ctx context.Context, q Query) (Response, error) {
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return Response{}, errors.New("search query is empty")
	}
	n := ClampResults(q.MaxResults)
	body, err := json.Marshal(orRequest{
		Model: o.cfg.Model,
		Messages: []orMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: text},
		},
		Plugins: []orPlugin{{ID: "web", MaxResults: n, Engine: o.cfg.Engine}},
	})
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+o.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.cfg.HTTPClient.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("web search: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return Response{}, fmt.Errorf("web search: reading response: %w", err)
	}

	var out orResponse
	decErr := json.Unmarshal(raw, &out)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(raw))
		if decErr == nil && out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		return Response{}, fmt.Errorf("web search: openrouter returned %s: %s", resp.Status, msg)
	}
	if decErr != nil {
		return Response{}, fmt.Errorf("web search: decoding response: %w", decErr)
	}
	if out.Error != nil {
		return Response{}, fmt.Errorf("web search: openrouter error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return Response{}, errors.New("web search: openrouter returned no choices")
	}

	msg := out.Choices[0].Message
	res := Response{Answer: strings.TrimSpace(msg.Content), Results: []Result{}}
	seen := map[string]bool{}
	for _, a := range msg.Annotations {
		if len(res.Results) >= n {
			break
		}
		c := a.URLCitation
		if a.Type != "url_citation" || c.URL == "" || seen[c.URL] {
			continue
		}
		seen[c.URL] = true
		res.Results = append(res.Results, Result{Title: c.Title, URL: c.URL, Snippet: strings.TrimSpace(c.Content)})
	}
	return res, nil
}
