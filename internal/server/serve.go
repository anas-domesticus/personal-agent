package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Server is the API listening on an address.
type Server struct {
	// URL is where clients reach it, e.g. http://127.0.0.1:53211.
	URL string
	srv *http.Server
	ln  net.Listener
}

// Listen binds addr (port 0 picks a free one). Serve starts answering.
func Listen(ctx context.Context, addr string, h http.Handler) (*Server, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	// HTTP/1.1 for Connect clients and curl, plus HTTP/2 without TLS so gRPC
	// clients (grpcurl, other languages) work too.
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	return &Server{
		URL: "http://" + ln.Addr().String(),
		srv: &http.Server{Handler: h, Protocols: &protocols, ReadHeaderTimeout: 10 * time.Second},
		ln:  ln,
	}, nil
}

// Serve answers requests until Shutdown.
func (s *Server) Serve() error {
	if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// Shutdown stops accepting requests and waits for open ones until ctx ends.
// Turns keep running regardless; chat.Service.Close deals with those.
func (s *Server) Shutdown(ctx context.Context) {
	if err := s.srv.Shutdown(ctx); err != nil {
		slog.Warn("server did not shut down cleanly", "err", err)
		_ = s.srv.Close()
	}
}
