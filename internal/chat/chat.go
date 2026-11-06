// Package chat runs conversations with an agent and stores them.
//
// Eino's ADK runner is stateless: each turn takes the message history. This
// package keeps that history and decides how much of it each turn sees.
//
// The user's message and what the agent produced are saved together at the
// end of a turn, so the history never holds a tool call without its result.
// A turn that fails before any tool ran leaves no trace. One that fails after
// tools ran (they may have changed things) saves the part that completed, so
// the agent can see what was done.
package chat

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/domain"
)

// Store keeps conversations. *PGStore implements it.
type Store interface {
	Create(ctx context.Context, agentName string) (domain.Conversation, error)
	Get(ctx context.Context, id uuid.UUID) (domain.Conversation, error)
	List(ctx context.Context, limit int32) ([]domain.Conversation, error)
	// History returns the messages in order and the id of the last one
	// (0 if there are none).
	History(ctx context.Context, id uuid.UUID) ([]*schema.Message, int64, error)
	// AppendTurn saves a turn's messages, and the title if one is given. It
	// returns domain.ErrConflict if messages were added after lastID.
	AppendTurn(ctx context.Context, id uuid.UUID, lastID int64, msgs []*schema.Message, agentVersion int32, title string) error
}

// Agents builds agents. *agent.Builder implements it.
type Agents interface {
	Build(ctx context.Context, name string) (agent.Runnable, error)
	BuildSpec(ctx context.Context, spec domain.SubagentSpec, depth int) (agent.Runnable, error)
}

type Service struct {
	store  Store
	agents Agents
	// HistoryChars bounds the history sent each turn. Older messages stay
	// stored but aren't sent.
	HistoryChars int

	mu     sync.Mutex
	active map[uuid.UUID]bool
}

func New(store Store, agents Agents) *Service {
	return &Service{store: store, agents: agents, HistoryChars: agent.DefaultHistoryChars, active: map[uuid.UUID]bool{}}
}

// Start creates a conversation with the named agent.
func (s *Service) Start(ctx context.Context, agentName string) (domain.Conversation, error) {
	return s.store.Create(ctx, agentName)
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (domain.Conversation, error) {
	return s.store.Get(ctx, id)
}

// List returns recent conversations, most recently active first.
func (s *Service) List(ctx context.Context, limit int32) ([]domain.Conversation, error) {
	return s.store.List(ctx, limit)
}

// History returns the conversation's messages in order.
func (s *Service) History(ctx context.Context, id uuid.UUID) ([]*schema.Message, error) {
	msgs, _, err := s.store.History(ctx, id)
	return msgs, err
}

// Send adds a user message and runs the agent's current version over the
// conversation. onMessage, if set, sees each new message as it completes.
// A conversation handles one turn at a time; a second Send while one is in
// progress returns domain.ErrBusy.
func (s *Service) Send(ctx context.Context, id uuid.UUID, text string, onMessage func(*schema.Message)) (agent.Turn, error) {
	if !s.lock(id) {
		return agent.Turn{}, fmt.Errorf("conversation %s is handling another message: %w", id, domain.ErrBusy)
	}
	defer s.unlock(id)

	conv, err := s.store.Get(ctx, id)
	if err != nil {
		return agent.Turn{}, err
	}
	history, lastID, err := s.store.History(ctx, id)
	if err != nil {
		return agent.Turn{}, err
	}
	var a agent.Runnable
	if conv.Subagent != nil {
		// Talking to a subagent directly, e.g. to inspect or steer it.
		a, err = s.agents.BuildSpec(ctx, *conv.Subagent, 1)
	} else {
		a, err = s.agents.Build(ctx, conv.Agent)
	}
	if err != nil {
		return agent.Turn{}, err
	}

	user := schema.UserMessage(text)
	ctx = domain.WithConversation(ctx, id)
	turn, runErr := a.Run(ctx, agent.Window(append(history, user), s.HistoryChars), onMessage)
	produced := turn.Messages
	if runErr != nil {
		produced = agent.Settled(produced)
		if !ranTools(produced) {
			return turn, runErr
		}
	}

	var newTitle string
	if conv.Title == "" {
		newTitle = title(text)
	}
	// What happened has been paid for and its tools have had their effects;
	// save it even if the caller has gone away.
	msgs := append([]*schema.Message{user}, produced...)
	if err := s.store.AppendTurn(context.WithoutCancel(ctx), id, lastID, msgs, a.Info().Version, newTitle); err != nil {
		if runErr != nil {
			return turn, fmt.Errorf("%w (and saving the partial turn failed: %w)", runErr, err)
		}
		return turn, err
	}
	return turn, runErr
}

func ranTools(msgs []*schema.Message) bool {
	for _, m := range msgs {
		if m.Role == schema.Tool {
			return true
		}
	}
	return false
}

func (s *Service) lock(id uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[id] {
		return false
	}
	s.active[id] = true
	return true
}

func (s *Service) unlock(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, id)
}

// title is the first line of the first message, cut to 60 characters.
func title(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if utf8.RuneCountInString(line) <= 60 {
		return line
	}
	return string([]rune(line)[:57]) + "..."
}
