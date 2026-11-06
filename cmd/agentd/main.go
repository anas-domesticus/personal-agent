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

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/anas-domesticus/personal-agent/internal/config"
	"github.com/anas-domesticus/personal-agent/internal/embed"
	"github.com/anas-domesticus/personal-agent/internal/llm"
	"github.com/anas-domesticus/personal-agent/internal/store"
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

	riverCfg := &river.Config{}
	if cfg.OpenRouterAPIKey == "" {
		// Without a key, this process can still queue embedding jobs but
		// won't work them; they wait until a process with a key runs.
		slog.Warn("OPENROUTER_API_KEY is not set; embeddings will not be computed")
	} else {
		embedder, err := llm.NewEmbedder(ctx, llm.Config{APIKey: cfg.OpenRouterAPIKey}, cfg.EmbeddingModel)
		if err != nil {
			return err
		}
		embed.Register(riverCfg, st.Queries, embedder, cfg.EmbeddingModel)
	}

	jobs, err := river.NewClient[pgx.Tx](riverpgxv5.New(st.Pool), riverCfg)
	if err != nil {
		return fmt.Errorf("river client: %w", err)
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
