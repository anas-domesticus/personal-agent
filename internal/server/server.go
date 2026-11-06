// Package server serves the agent API (proto/agent/v1) over Connect, which
// also speaks gRPC and gRPC-Web. Clients such as the TUI use only this API,
// so they work the same against a local server or a remote one.
package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/gen/agent/v1/agentv1connect"
)

// Chat runs conversations. *chat.Service implements it.
type Chat interface {
	Start(ctx context.Context, agentName string) (domain.Conversation, error)
	Get(ctx context.Context, id uuid.UUID) (domain.Conversation, error)
	List(ctx context.Context, limit int32) ([]domain.Conversation, error)
	History(ctx context.Context, id uuid.UUID) ([]*schema.Message, error)
	Send(ctx context.Context, id uuid.UUID, text string, obs agent.Observer) (agent.Turn, error)
	Busy(id uuid.UUID) bool
	Cancel(id uuid.UUID) bool
}

// Agents reads agents. *catalog.Agents implements it.
type Agents interface {
	Get(ctx context.Context, name string) (domain.AgentDetail, error)
	List(ctx context.Context) ([]domain.Agent, error)
}

// Deps is what the API serves.
type Deps struct {
	Chat   Chat
	Agents Agents
	// Token, if set, must be sent by every client as a bearer token.
	Token string
}

// maxRequestBytes bounds a request. Messages are typed by people; 1 MiB is
// generous.
const maxRequestBytes = 1 << 20

// New returns the API's HTTP handler.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	limit := connect.WithReadMaxBytes(maxRequestBytes)
	mux.Handle(agentv1connect.NewAgentServiceHandler(&agents{d.Agents}, limit))
	mux.Handle(agentv1connect.NewConversationServiceHandler(&conversations{chat: d.Chat}, limit))
	if d.Token == "" {
		return mux
	}
	return requireToken(d.Token, mux)
}

func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			// A Connect-shaped error, so every client reports it the same way.
			_ = connect.NewErrorWriter().Write(w, r, connect.NewError(connect.CodeUnauthenticated, errors.New("missing or wrong token")))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// toConnect maps domain errors to Connect codes. Anything unrecognised is
// an internal error.
func toConnect(err error) error {
	if err == nil {
		return nil
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		return err
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, domain.ErrBusy):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, domain.ErrConflict):
		return connect.NewError(connect.CodeAborted, err)
	case errors.Is(err, domain.ErrExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, domain.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, err)
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	}
	return connect.NewError(connect.CodeInternal, err)
}

func parseID(field, s string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New(field+" must be a UUID"))
	}
	return id, nil
}
