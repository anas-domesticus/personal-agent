// Command orchestrator runs the unauthenticated MCP server and background workers.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anas-domesticus/personal-agent/internal/app"
	"github.com/anas-domesticus/personal-agent/internal/config"
	"github.com/anas-domesticus/personal-agent/internal/mcpserver"
)

const drainTimeout = 30 * time.Second

var _ mcpserver.Knowledge = (*app.Knowledge)(nil)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "orchestrator:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	a, err := app.New(ctx, cfg, app.Options{})
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), drainTimeout)
		defer closeCancel()
		a.Close(closeCtx)
	}()
	if err := a.Start(); err != nil {
		return err
	}

	_, handler := mcpserver.New(mcpserver.Deps{
		Agents: a.Agents, Workflows: a.Workflows, Runs: a.Runs, Knowledge: &a.Knowledge, Tools: a.Tools,
	})
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddr, err)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			slog.Error("MCP server stopped", "err", err)
			cancel()
		}
	}()
	slog.Info("MCP server ready", "url", "http://"+ln.Addr().String()+"/mcp")
	<-ctx.Done()

	shutdown, release := context.WithTimeout(context.Background(), drainTimeout)
	defer release()
	if err := srv.Shutdown(shutdown); err != nil {
		_ = srv.Close()
	}
	return nil
}
