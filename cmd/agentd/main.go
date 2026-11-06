// Command agentd is the long-running agent daemon.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/app"
	"github.com/anas-domesticus/personal-agent/internal/config"
)

// drainTimeout is how long running jobs get to finish on shutdown before
// they are cancelled.
const drainTimeout = 30 * time.Second

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
	a, err := app.New(ctx, cfg, app.Options{})
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		a.Close(closeCtx)
	}()

	// Building the admin agent catches a broken setup early. It isn't fatal:
	// the daemon still works runs, and a broken admin version can be fixed.
	if admin, err := a.Builder.Build(ctx, agent.AdminName); err != nil {
		slog.Error("admin agent can't be built", "err", err)
	} else {
		info := admin.Info()
		slog.Info("admin agent ready", "version", info.Version, "tools", len(info.Tools))
	}

	if err := a.Start(); err != nil {
		return err
	}
	slog.Info("agentd ready")
	<-ctx.Done()
	slog.Info("shutting down; letting running jobs finish", "timeout", drainTimeout)
	return nil
}
