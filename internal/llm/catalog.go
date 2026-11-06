package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"
)

// Catalog checks model slugs against OpenRouter's model list, which is
// public and cached for an hour.
type Catalog struct {
	BaseURL string // defaults to DefaultBaseURL
	HTTP    *http.Client

	mu      sync.Mutex
	fetched time.Time
	tools   map[string]bool // slug -> supports tool calling
}

const catalogTTL = time.Hour

// Check returns an error unless the model exists and supports tool calling.
// If the list can't be fetched, models are allowed: the call itself will
// fail clearly if the slug is wrong.
func (c *Catalog) Check(ctx context.Context, slug string) error {
	models, err := c.models(ctx)
	if err != nil {
		slog.Warn("can't fetch OpenRouter's model list; not checking the model", "model", slug, "err", err)
		return nil
	}
	supportsTools, ok := models[slug]
	switch {
	case !ok:
		return fmt.Errorf("model %q isn't on OpenRouter; use a slug like deepseek/deepseek-v4.1-flash", slug)
	case !supportsTools:
		return fmt.Errorf("model %q doesn't support tool calling, which agents need", slug)
	}
	return nil
}

func (c *Catalog) models(ctx context.Context) (map[string]bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tools != nil && time.Since(c.fetched) < catalogTTL {
		return c.tools, nil
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("models list: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("models list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models list: %s", resp.Status)
	}
	var body struct {
		Data []struct {
			ID                  string   `json:"id"`
			SupportedParameters []string `json:"supported_parameters"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("models list: %w", err)
	}
	out := make(map[string]bool, len(body.Data))
	for _, m := range body.Data {
		out[m.ID] = slices.Contains(m.SupportedParameters, "tools")
	}
	c.tools, c.fetched = out, time.Now()
	return out, nil
}
