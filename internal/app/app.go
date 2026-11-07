// Package app wires the orchestration system together. The MCP binary and
// integration tests build an App rather than repeating the wiring.
//
// Construction creates the stores and model clients, then agent definitions,
// workflows and runs, and finally the workers that execute them.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/chat"
	"github.com/anas-domesticus/personal-agent/internal/config"
	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/llm"
	"github.com/anas-domesticus/personal-agent/internal/runs"
	"github.com/anas-domesticus/personal-agent/internal/search"
	"github.com/anas-domesticus/personal-agent/internal/store"
	"github.com/anas-domesticus/personal-agent/internal/tools"
	"github.com/anas-domesticus/personal-agent/internal/tools/clock"
	"github.com/anas-domesticus/personal-agent/internal/tools/files"
	"github.com/anas-domesticus/personal-agent/internal/tools/kit"
	"github.com/anas-domesticus/personal-agent/internal/tools/mcp"
	"github.com/anas-domesticus/personal-agent/internal/tools/web"
	"github.com/anas-domesticus/personal-agent/internal/workspace"
)

// MCPCheckInterval is how often MCP servers are pinged and reconnected.
const MCPCheckInterval = time.Minute

// Options overrides parts of the system, for tests.
type Options struct {
	// Store is used instead of opening Config.DatabaseURL.
	Store *store.Store
	// Models replaces OpenRouter chat models.
	Models agent.ModelFactory
	// Embedder replaces the OpenRouter embedder.
	Embedder embedding.Embedder
	// Workspace replaces the configured workspace.
	Workspace workspace.Workspace
	// Search replaces OpenRouter web search.
	Search search.Searcher
	// ModelCatalog replaces the check of subagent models against OpenRouter.
	ModelCatalog agent.ModelCatalog
}

type App struct {
	Config    config.Config
	Store     *store.Store
	MCP       *mcp.Set
	Workspace workspace.Workspace
	Search    search.Searcher
	Agents    *catalog.Agents
	Workflows *catalog.Workflows
	Runs      *runs.Service
	Tools     *tools.Toolset
	Builder   *agent.Builder

	// Jobs queues jobs; it never works them.
	Jobs *river.Client[pgx.Tx]
	// worker works the queues this process can serve; nil if none.
	worker *river.Client[pgx.Tx]

	life   context.Context
	cancel context.CancelFunc
	ownsDB bool
}

// New builds the system. Nothing runs in the background until Start.
func New(ctx context.Context, cfg config.Config, opts Options) (_ *App, err error) {
	life, cancel := context.WithCancel(context.Background())
	a := &App{Config: cfg, life: life, cancel: cancel, Store: opts.Store}
	defer func() {
		if err != nil {
			a.Close(context.Background())
		}
	}()

	if a.Store == nil {
		if a.Store, err = store.Open(ctx, cfg.DatabaseURL); err != nil {
			return nil, err
		}
		a.ownsDB = true
	}
	if a.Jobs, err = river.NewClient(riverpgxv5.New(a.Store.Pool), &river.Config{Logger: slog.Default()}); err != nil {
		return nil, fmt.Errorf("river client: %w", err)
	}

	llmCfg := llm.Config{APIKey: cfg.OpenRouterAPIKey}
	hasKey := cfg.OpenRouterAPIKey != ""
	embedder, models := opts.Embedder, opts.Models
	if embedder == nil && hasKey {
		if embedder, err = llm.NewEmbedder(ctx, llmCfg, cfg.EmbeddingModel); err != nil {
			return nil, err
		}
	}
	if models == nil && hasKey {
		models = func(ctx context.Context, spec llm.Spec) (model.ToolCallingChatModel, error) {
			return llm.NewChatModel(ctx, llmCfg, spec)
		}
	}
	if embedder != nil {
		// A model with the wrong vector size would fail every embed job, and
		// the sweep would keep paying to retry them. Check once and stop.
		if _, err := embed.Text(ctx, embedder, "dimension check"); err != nil {
			return nil, fmt.Errorf("embedding model %s: %w", cfg.EmbeddingModel, err)
		}
	}
	hasModels := models != nil
	if !hasModels {
		slog.Warn("OPENROUTER_API_KEY is not set; models, embeddings and runs are unavailable")
		models = func(context.Context, llm.Spec) (model.ToolCallingChatModel, error) {
			return nil, errors.New("no model access: OPENROUTER_API_KEY is not set")
		}
	}

	a.MCP = mcp.Connect(life, cfg.MCP)
	if a.Workspace, err = newWorkspace(cfg, opts.Workspace); err != nil {
		return nil, err
	}
	a.Search = opts.Search
	if a.Search == nil {
		a.Search = search.Unavailable{}
		if hasKey {
			a.Search = search.NewOpenRouter(search.OpenRouterConfig{APIKey: cfg.OpenRouterAPIKey, Model: cfg.SearchModel})
		}
	}
	modelCatalog := opts.ModelCatalog
	if modelCatalog == nil {
		modelCatalog = &llm.Catalog{}
	}

	semantic := catalog.Search{Embedder: embedder, Model: cfg.EmbeddingModel}

	general, err := generalTools(a)
	if err != nil {
		return nil, err
	}
	registry, err := tools.New(general, a.MCP)
	if err != nil {
		return nil, err
	}
	a.Agents = catalog.NewAgents(a.Store, registry)
	a.Workflows = catalog.NewWorkflows(a.Store, a.Jobs, semantic)
	a.Runs = runs.NewService(a.Store, a.Jobs)

	a.Tools = registry.NewToolset()

	conversations := chat.NewPGStore(a.Store)
	a.Builder = &agent.Builder{Agents: a.Agents, Tools: a.Tools, Models: models, Catalog: modelCatalog, Subagents: conversations}

	// Each queue is worked only where it can be: runs need models, and
	// embeddings need an embedder. Jobs for an unworked queue wait for a
	// process that can work it.
	rc := &river.Config{Logger: slog.Default()}
	if embedder != nil {
		embed.Register(rc, a.Store.Queries, embedder, cfg.EmbeddingModel)
	}
	if hasModels {
		runs.Register(rc, &runs.Worker{Store: a.Runs, Agents: a.Builder})
	}
	if len(rc.Queues) > 0 {
		if a.worker, err = river.NewClient(riverpgxv5.New(a.Store.Pool), rc); err != nil {
			return nil, fmt.Errorf("river worker: %w", err)
		}
	}
	return a, nil
}

// Start begins working jobs and maintaining MCP connections.
func (a *App) Start() error {
	go a.MCP.Maintain(a.life, MCPCheckInterval)
	if a.worker == nil {
		return nil
	}
	// Started with the app's lifetime rather than a request context:
	// cancelling River's start context hard-stops running jobs, and Close
	// wants to drain them first.
	if err := a.worker.Start(a.life); err != nil {
		return fmt.Errorf("start river: %w", err)
	}
	return nil
}

// Close stops working jobs, letting running ones finish until ctx ends and
// cancelling the rest, then disconnects everything.
func (a *App) Close(ctx context.Context) {
	if a.worker != nil {
		if err := a.worker.Stop(ctx); err != nil {
			slog.Warn("jobs did not finish in time; cancelling them", "err", err)
			hard, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = a.worker.StopAndCancel(hard)
			cancel()
		}
	}
	a.cancel()
	a.MCP.Close()
	if a.ownsDB && a.Store != nil {
		a.Store.Close()
	}
}

// generalTools collects every tool versions can turn on.
func generalTools(a *App) ([]kit.Spec, error) {
	var all []kit.Spec
	for _, set := range []func() ([]kit.Spec, error){
		func() ([]kit.Spec, error) { return kit.Specs(clock.Tools()) },
		func() ([]kit.Spec, error) { return web.Specs(a.Search, nil) },
		func() ([]kit.Spec, error) { return files.Specs(a.Workspace) },
		func() ([]kit.Spec, error) { return []kit.Spec{agent.SubagentSpec()}, nil },
	} {
		specs, err := set()
		if err != nil {
			return nil, err
		}
		all = append(all, specs...)
	}
	return all, nil
}

func newWorkspace(cfg config.Config, override workspace.Workspace) (workspace.Workspace, error) {
	if override != nil {
		return override, nil
	}
	switch cfg.Workspace {
	case config.WorkspaceSandbox:
		return workspace.Sandbox{}, nil
	default:
		ws, err := workspace.NewLocal(cfg.WorkspaceDir)
		if err != nil {
			return nil, fmt.Errorf("workspace: %w", err)
		}
		return ws, nil
	}
}
