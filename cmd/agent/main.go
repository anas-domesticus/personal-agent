// Command agent is the personal agent: a server that owns the agents, their
// conversations and jobs, and a terminal client that talks to it.
//
//	agent                 start a server in this process and open the TUI on it
//	agent serve           run only the server, for clients elsewhere
//	agent attach [URL]    open the TUI on a server that's already running
//
// The TUI only ever talks to the server over its API, so the first form is
// the third with the server started for you.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/app"
	"github.com/anas-domesticus/personal-agent/internal/client"
	"github.com/anas-domesticus/personal-agent/internal/config"
	"github.com/anas-domesticus/personal-agent/internal/server"
	"github.com/anas-domesticus/personal-agent/internal/tui"
)

// drainTimeout is how long running turns and jobs get to finish on shutdown
// before they are cancelled.
const drainTimeout = 30 * time.Second

const usage = `Usage:
  agent [flags]              start a local server and open the TUI on it
  agent serve                run the server only (AGENT_LISTEN, AGENT_TOKEN)
  agent attach [flags] [URL] open the TUI on a running server (AGENT_URL, AGENT_TOKEN)

TUI flags:
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agent:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := ""
	if len(args) > 0 && (args[0] == "serve" || args[0] == "attach" || args[0] == "help") {
		cmd, args = args[0], args[1:]
	}
	// After the first signal, restore the default handling, so a second one
	// kills the process instead of waiting out the drain.
	go func() {
		<-ctx.Done()
		stop()
	}()

	switch cmd {
	case "serve":
		return serve(ctx)
	case "help":
		fmt.Fprint(os.Stderr, usage)
		tuiFlags("agent").PrintDefaults()
		return nil
	}

	fs := tuiFlags("agent " + cmd)
	var token string
	if cmd == "attach" {
		fs.StringVar(&token, "token", os.Getenv("AGENT_TOKEN"), "the server's token")
	}
	opts := &tui.Options{Out: os.Stderr}
	fs.StringVar(&opts.Agent, "agent", "admin", "the agent new conversations talk to")
	fs.StringVar(&opts.ConversationID, "conversation", "", "open this conversation")
	fs.BoolVar(&opts.Continue, "c", false, "continue the most recent conversation")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if cmd == "attach" {
		url := fs.Arg(0)
		if url == "" {
			url = os.Getenv("AGENT_URL")
		}
		if url == "" {
			return errors.New("attach needs the server's URL, as an argument or AGENT_URL")
		}
		opts.Remote = url
		opts.Resume = func(id string) string { return "agent attach -conversation " + id + " " + url }
		return tui.Run(ctx, client.New(url, token), *opts)
	}
	opts.Resume = func(id string) string { return "agent -conversation " + id }
	return local(ctx, *opts)
}

func tuiFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		fs.PrintDefaults()
	}
	return fs
}

// local starts the server on a private loopback port and runs the TUI
// against it. Logs go to a file, since the terminal belongs to the TUI.
func local(ctx context.Context, opts tui.Options) error {
	logPath, closeLog, err := logToFile()
	if err != nil {
		return err
	}
	defer closeLog()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a, srv, err := start(ctx, cancel, cfg, "127.0.0.1:0", token)
	if err != nil {
		return fmt.Errorf("%w (log: %s)", err, logPath)
	}
	defer shutdown(a, srv)

	return tui.Run(ctx, client.New(srv.URL, token), opts)
}

// serve runs the server until interrupted.
func serve(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	token := cfg.Token
	if token == "" {
		if !loopback(cfg.ListenAddr) {
			return fmt.Errorf("AGENT_TOKEN is required to listen on %s", cfg.ListenAddr)
		}
		if token, err = randomToken(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "AGENT_TOKEN is not set; this run's token is %s\n", token)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a, srv, err := start(ctx, cancel, cfg, cfg.ListenAddr, token)
	if err != nil {
		return err
	}
	defer shutdown(a, srv)
	slog.Info("agent server ready", "url", srv.URL)
	<-ctx.Done()
	slog.Info("shutting down; letting running turns and jobs finish", "timeout", drainTimeout)
	return nil
}

// start builds the app, starts its jobs and serves the API on addr. If the
// server stops serving, it calls stop, since the process is no use without it.
func start(ctx context.Context, stop func(), cfg config.Config, addr, token string) (*app.App, *server.Server, error) {
	a, err := app.New(ctx, cfg, app.Options{})
	if err != nil {
		return nil, nil, err
	}
	// Building the admin agent catches a broken setup early. It isn't fatal:
	// the server still works runs, and a broken admin version can be fixed.
	if admin, err := a.Builder.Build(ctx, agent.AdminName); err != nil {
		slog.Error("admin agent can't be built", "err", err)
	} else {
		info := admin.Info()
		slog.Info("admin agent ready", "version", info.Version, "tools", len(info.Tools))
	}
	if err := a.Start(); err != nil {
		a.Close(context.Background())
		return nil, nil, err
	}
	srv, err := server.Listen(ctx, addr, server.New(server.Deps{Chat: a.Chat, Agents: a.Agents, Token: token}))
	if err != nil {
		a.Close(context.Background())
		return nil, nil, err
	}
	go func() {
		if err := srv.Serve(); err != nil {
			slog.Error("server stopped", "err", err)
			stop()
		}
	}()
	return a, srv, nil
}

// shutdown interrupts running turns (they save what they must), then stops
// serving, then drains jobs and disconnects.
func shutdown(a *app.App, srv *server.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	a.Chat.Close(ctx)
	srv.Shutdown(ctx)
	a.Close(ctx)
}

func logToFile() (string, func(), error) {
	path := os.Getenv("AGENT_LOG")
	if path == "" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return "", nil, fmt.Errorf("find a directory for the log (set AGENT_LOG): %w", err)
		}
		path = filepath.Join(dir, "personal-agent", "agent.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", nil, fmt.Errorf("create log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", nil, fmt.Errorf("open log: %w", err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(f, nil)))
	return path, func() { _ = f.Close() }, nil
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
