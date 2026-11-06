// Package testenv builds the full stack against a throwaway Postgres, with
// scripted fake models, for integration tests.
package testenv

import (
	"context"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/chat"
	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/runner"
	"github.com/anas-domesticus/personal-agent/internal/store"
	"github.com/anas-domesticus/personal-agent/internal/testdb"
	"github.com/anas-domesticus/personal-agent/internal/tools"
)

// Responder produces a fake model's reply to a conversation.
type Responder func(msgs []*schema.Message) *schema.Message

// Env is a running stack. Models maps OpenRouter slugs to scripted replies;
// unknown slugs reply "ok".
type Env struct {
	Store    *store.Store
	Jobs     *river.Client[pgx.Tx]
	Catalog  *catalog.Catalog
	Registry *tools.Registry
	Builder  *agent.Builder
	Chat     *chat.Service

	mu     sync.Mutex
	models map[string]Responder
}

// SetModel scripts the replies for a model slug.
func (e *Env) SetModel(slug string, r Responder) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.models[slug] = r
}

// New starts the stack with the run worker active and scripted fake models.
// The admin agent is seeded with adminModel.
func New(t *testing.T, adminModel string) *Env {
	return NewWithModels(t, adminModel, nil)
}

// NewWithModels is New with a real model factory; nil means fake models.
func NewWithModels(t *testing.T, adminModel string, models agent.ModelFactory) *Env {
	t.Helper()
	ctx := context.Background()
	st, _ := testdb.Open(t)
	e := &Env{Store: st, models: map[string]Responder{}}

	cfg := &river.Config{}
	worker := &runner.Worker{Queries: st.Queries}
	runner.Register(cfg, worker)
	embed.Register(cfg, st.Queries, fakeEmbedder{}, "fake-embedding")
	jobs, err := river.NewClient[pgx.Tx](riverpgxv5.New(st.Pool), cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.Jobs = jobs

	e.Catalog = catalog.New(catalog.Options{Store: st, Jobs: jobs})
	if e.Registry, err = tools.New(tools.Deps{Catalog: e.Catalog}); err != nil {
		t.Fatal(err)
	}
	e.Catalog.SetAssignable(e.Registry.Assignable)
	if err := agent.EnsureAdmin(ctx, e.Catalog, adminModel); err != nil {
		t.Fatal(err)
	}

	if models == nil {
		models = func(_ context.Context, slug string) (model.ToolCallingChatModel, error) {
			return &fakeModel{env: e, slug: slug}, nil
		}
	}
	e.Builder = &agent.Builder{Catalog: e.Catalog, Tools: e.Registry, Models: models}
	worker.Builder = e.Builder
	e.Chat = &chat.Service{Store: st, Builder: e.Builder}

	if err := jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = jobs.Stop(context.Background()) })
	return e
}

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
	return r(msgs), nil
}

func (f *fakeModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m, err := f.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{m}), nil
}

func (f *fakeModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return f, nil }

// fakeEmbedder returns the same 1536-dimension vector for every text.
type fakeEmbedder struct{}

func (fakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		v := make([]float64, 1536)
		v[0] = 1
		out[i] = v
	}
	return out, nil
}
