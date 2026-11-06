// Command agentd is the long-running agent daemon.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/config"
	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/llm"
	"github.com/anas-domesticus/personal-agent/internal/runner"
	"github.com/anas-domesticus/personal-agent/internal/store"
	"github.com/anas-domesticus/personal-agent/internal/tools"
	"github.com/anas-domesticus/personal-agent/internal/tools/mcp"
)

func main() {
	if err := run(); err != nil {
		slog.Error("agentd failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	llmCfg := llm.Config{APIKey: cfg.OpenRouterAPIKey}
	hasKey := cfg.OpenRouterAPIKey != ""
	var embedder embedding.Embedder
	riverCfg := &river.Config{}

	// The run worker needs the agent builder, which needs the catalog, which
	// needs the River client; so the worker is registered now and given the
	// builder once it exists, before River starts.
	runWorker := &runner.Worker{Queries: st.Queries}
	if !hasKey {
		// Without a key, jobs can still be queued; they wait until a process
		// with a key runs.
		slog.Warn("OPENROUTER_API_KEY is not set; models, embeddings and runs are unavailable")
	} else {
		if embedder, err = llm.NewEmbedder(ctx, llmCfg, cfg.EmbeddingModel); err != nil {
			return err
		}
		embed.Register(riverCfg, st.Queries, embedder, cfg.EmbeddingModel)
		runner.Register(riverCfg, runWorker)
	}

	jobs, err := river.NewClient[pgx.Tx](riverpgxv5.New(st.Pool), riverCfg)
	if err != nil {
		return fmt.Errorf("river client: %w", err)
	}

	cat := catalog.New(catalog.Options{Store: st, Jobs: jobs, Embedder: embedder})

	mcpServers := mcp.Connect(ctx, cfg.MCP)
	defer mcpServers.Close()

	registry, err := tools.New(tools.Deps{Catalog: cat, MCP: mcpServers})
	if err != nil {
		return err
	}
	cat.SetAssignable(registry.Assignable)

	if err := agent.EnsureAdmin(ctx, cat, cfg.AdminModel); err != nil {
		return fmt.Errorf("ensure admin agent: %w", err)
	}

	builder := &agent.Builder{
		Catalog: cat,
		Tools:   registry,
		Models: func(ctx context.Context, slug string) (model.ToolCallingChatModel, error) {
			return llm.NewChatModel(ctx, llmCfg, slug)
		},
	}
	runWorker.Builder = builder

	if hasKey {
		// Build once at startup to catch a broken admin version early.
		admin, err := builder.Build(ctx, agent.AdminName)
		if err != nil {
			return fmt.Errorf("build admin agent: %w", err)
		}
		slog.Info("admin agent ready", "version", admin.Version, "tools", len(admin.Tools))
	}

	if len(riverCfg.Queues) > 0 {
		if err := jobs.Start(ctx); err != nil {
			return fmt.Errorf("start river: %w", err)
		}
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := jobs.Stop(stopCtx); err != nil {
				slog.Warn("river did not stop cleanly", "err", err)
			}
		}()
	}

	slog.Info("agentd ready")
	<-ctx.Done()
	slog.Info("shutting down")
	return nil
}
