// Package chat runs conversations with an agent and stores them.
//
// Eino's ADK runner is stateless: each turn takes the full message history.
// This package keeps that history in Postgres, one row per schema.Message.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/catalog"
	"github.com/anas-domesticus/personal-agent/internal/store"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

type Service struct {
	Store   *store.Store
	Builder *agent.Builder
}

// Start creates a conversation with the named agent.
func (s *Service) Start(ctx context.Context, agentName string) (uuid.UUID, error) {
	a, err := s.Store.GetAgentByName(ctx, agentName)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("agent %q: %w", agentName, catalog.ErrNotFound)
	}
	if err != nil {
		return uuid.Nil, err
	}
	c, err := s.Store.CreateConversation(ctx, db.CreateConversationParams{AgentID: a.ID})
	return c.ID, err
}

// History returns the conversation's messages in order.
func (s *Service) History(ctx context.Context, id uuid.UUID) ([]*schema.Message, error) {
	rows, err := s.Store.ListMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]*schema.Message, 0, len(rows))
	for _, r := range rows {
		var m schema.Message
		if err := json.Unmarshal(r.Message, &m); err != nil {
			return nil, fmt.Errorf("message %d: %w", r.ID, err)
		}
		out = append(out, &m)
	}
	return out, nil
}

// Send adds a user message and runs the agent's current version over the
// whole conversation. onMessage, if set, sees each new message as it
// completes. The user message is saved first; the agent's messages are saved
// together once the turn succeeds, so a failed turn never leaves a dangling
// tool call in the history.
func (s *Service) Send(ctx context.Context, id uuid.UUID, text string, onMessage func(*schema.Message)) (agent.Turn, error) {
	conv, err := s.Store.GetConversation(ctx, id)
	if err != nil {
		return agent.Turn{}, fmt.Errorf("conversation %s: %w", id, err)
	}
	history, err := s.History(ctx, id)
	if err != nil {
		return agent.Turn{}, err
	}
	built, err := s.Builder.Build(ctx, conv.AgentName)
	if err != nil {
		return agent.Turn{}, err
	}

	user := schema.UserMessage(text)
	if err := s.save(ctx, s.Store.Queries, id, user, nil); err != nil {
		return agent.Turn{}, err
	}
	if conv.Title == "" {
		if err := s.Store.SetConversationTitle(ctx, db.SetConversationTitleParams{ID: id, Title: title(text)}); err != nil {
			return agent.Turn{}, err
		}
	}

	turn, err := built.Run(ctx, append(history, user), onMessage)
	if err != nil {
		return turn, err
	}

	err = pgx.BeginFunc(ctx, s.Store.Pool, func(tx pgx.Tx) error {
		q := s.Store.WithTx(tx)
		for _, m := range turn.Messages {
			if err := s.save(ctx, q, id, m, &built.Version); err != nil {
				return err
			}
		}
		return q.TouchConversation(ctx, id)
	})
	return turn, err
}

func (s *Service) save(ctx context.Context, q *db.Queries, id uuid.UUID, m *schema.Message, version *int32) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = q.AddMessage(ctx, db.AddMessageParams{
		ConversationID: id, Role: string(m.Role), Content: m.Content, Message: raw, AgentVersion: version,
	})
	return err
}

// title is the first line of the first message, cut to 60 characters.
func title(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if utf8.RuneCountInString(line) <= 60 {
		return line
	}
	return string([]rune(line)[:57]) + "..."
}
