// Package testenv builds the full stack against a throwaway Postgres, with
// scripted fake models, for integration tests. It uses the same wiring as
// agentd (package app).
package testenv

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/app"
	"github.com/anas-domesticus/personal-agent/internal/config"
	"github.com/anas-domesticus/personal-agent/internal/llm"
	"github.com/anas-domesticus/personal-agent/internal/search"
	"github.com/anas-domesticus/personal-agent/internal/testdb"
	"github.com/anas-domesticus/personal-agent/internal/workspace"
)

// Responder produces a fake model's reply to a conversation.
type Responder func(msgs []*schema.Message) *schema.Message

// Env is a running stack. Models maps OpenRouter slugs to scripted replies;
// unknown slugs reply "ok".
type Env struct {
	*app.App

	mu     sync.Mutex
	models map[string]Responder
}

// SetModel scripts the replies for a model slug.
func (e *Env) SetModel(slug string, r Responder) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.models[slug] = r
}

// Options adjusts the stack.
type Options struct {
	// Models replaces the scripted fake models, e.g. with real ones.
	Models agent.ModelFactory
	// MCP configures MCP servers.
	MCP config.MCP
	// Search replaces the fake web search.
	Search search.Searcher
}

// New starts the stack with job workers running and scripted fake models.
// The admin agent is seeded with adminModel.
func New(t *testing.T, adminModel string) *Env {
	t.Helper()
	return NewWith(t, adminModel, Options{})
}

// NewWith is New with parts of the stack replaced.
func NewWith(t *testing.T, adminModel string, o Options) *Env {
	t.Helper()
	ctx := context.Background()
	st, _ := testdb.Open(t)
	e := &Env{models: map[string]Responder{}}

	models := o.Models
	if models == nil {
		models = func(_ context.Context, spec llm.Spec) (model.ToolCallingChatModel, error) {
			return &fakeModel{env: e, slug: spec.Model}, nil
		}
	}
	cfg := config.Config{EmbeddingModel: FakeEmbeddingModel, AdminModel: adminModel, MCP: o.MCP}
	ws, err := workspace.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	searcher := o.Search
	if searcher == nil {
		searcher = FakeSearch{}
	}
	a, err := app.New(ctx, cfg, app.Options{
		Store: st, Models: models, Embedder: FakeEmbedder{},
		Workspace: ws, Search: searcher, ModelCatalog: AnyModel{},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.App = a
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		a.Close(c)
	})
	return e
}

// The doubles below are hand-written rather than mockery mocks because they
// stand in for collaborators across the whole stack, called any number of
// times and in any order by agents, runs and jobs; a mock would need every
// call listed up front.
var (
	_ model.ToolCallingChatModel = (*fakeModel)(nil)
	_ embedding.Embedder         = FakeEmbedder{}
	_ search.Searcher            = FakeSearch{}
	_ agent.ModelCatalog         = AnyModel{}
)

// fakeModel plays back the Responder scripted for its slug; it isn't a mock
// because a conversation's replies depend on the messages so far.
type fakeModel struct {
	env  *Env
	slug string
}

func (f *fakeModel) Generate(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.env.mu.Lock()
	r := f.env.models[f.slug]
	f.env.mu.Unlock()
	if r == nil {
		return schema.AssistantMessage("ok", nil), nil
	}
	if m := r(msgs); m != nil {
		return m, nil
	}
	return nil, errors.New("fake model failure")
}

func (f *fakeModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m, err := f.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{m}), nil
}

func (f *fakeModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return f, nil }

// FakeEmbeddingModel is the embedding model name the fake embedder reports.
const FakeEmbeddingModel = "fake-embedding"

// FakeEmbedder returns the same 1536-dimension vector for every text. It
// isn't a mock because background embed jobs call it an unknown number of
// times.
type FakeEmbedder struct{}

func (FakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		v := make([]float64, 1536)
		v[0] = 1
		out[i] = v
	}
	return out, nil
}

// FakeSearch returns one result naming the query. It isn't a mock because
// one stateless double serves every integration test.
type FakeSearch struct{}

func (FakeSearch) Search(_ context.Context, q search.Query) (search.Response, error) {
	return search.Response{
		Answer:  "results for " + q.Text,
		Results: []search.Result{{Title: q.Text, URL: "https://example.com/" + url.PathEscape(q.Text), Snippet: "about " + q.Text}},
	}, nil
}

// AnyModel accepts every model. It isn't a mock because it's a stateless
// stand-in shared by every integration test.
type AnyModel struct{}

func (AnyModel) Check(context.Context, string) error { return nil }
