package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/store"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

// PGStore keeps conversations in Postgres, one row per message.
//
// Each row's `message` column is an Eino schema.Message as JSON, so history
// round-trips exactly, tool calls included. That ties stored history to
// Eino's message format: when upgrading Eino, check that old rows still
// decode (History fails loudly if they don't).
type PGStore struct {
	st *store.Store
}

func NewPGStore(st *store.Store) *PGStore { return &PGStore{st: st} }

func (p *PGStore) Create(ctx context.Context, agentName string) (domain.Conversation, error) {
	a, err := p.st.GetAgentByName(ctx, agentName)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Conversation{}, fmt.Errorf("agent %q: %w", agentName, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Conversation{}, err
	}
	c, err := p.st.CreateConversation(ctx, db.CreateConversationParams{AgentID: &a.ID})
	if err != nil {
		return domain.Conversation{}, err
	}
	return p.Get(ctx, c.ID)
}

// CreateSubagent starts a subagent's conversation.
func (p *PGStore) CreateSubagent(ctx context.Context, parent *uuid.UUID, spec domain.SubagentSpec, title string) (domain.Conversation, error) {
	var agentID *uuid.UUID
	if spec.Agent != "" {
		a, err := p.st.GetAgentByName(ctx, spec.Agent)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Conversation{}, fmt.Errorf("agent %q: %w", spec.Agent, domain.ErrNotFound)
		}
		if err != nil {
			return domain.Conversation{}, err
		}
		agentID = &a.ID
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return domain.Conversation{}, fmt.Errorf("encode subagent spec: %w", err)
	}
	c, err := p.st.CreateConversation(ctx, db.CreateConversationParams{AgentID: agentID, Title: title, ParentID: parent, Subagent: raw})
	if err != nil {
		return domain.Conversation{}, err
	}
	return p.Get(ctx, c.ID)
}

func toConversation(id uuid.UUID, agentID *uuid.UUID, agentName *string, parent *uuid.UUID, subagent json.RawMessage, title string, created, updated time.Time) (domain.Conversation, error) {
	c := domain.Conversation{ID: id, AgentID: agentID, ParentID: parent, Title: title, CreatedAt: created, UpdatedAt: updated}
	if agentName != nil {
		c.Agent = *agentName
	}
	if len(subagent) > 0 {
		c.Subagent = &domain.SubagentSpec{}
		if err := json.Unmarshal(subagent, c.Subagent); err != nil {
			return c, fmt.Errorf("conversation %s: subagent spec: %w", id, err)
		}
	}
	return c, nil
}

func (p *PGStore) Get(ctx context.Context, id uuid.UUID) (domain.Conversation, error) {
	c, err := p.st.GetConversation(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Conversation{}, fmt.Errorf("conversation %s: %w", id, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Conversation{}, err
	}
	return toConversation(c.ID, c.AgentID, c.AgentName, c.ParentID, c.Subagent, c.Title, c.CreatedAt, c.UpdatedAt)
}

func (p *PGStore) List(ctx context.Context, limit int32) ([]domain.Conversation, error) {
	rows, err := p.st.ListConversations(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Conversation, len(rows))
	for i, c := range rows {
		if out[i], err = toConversation(c.ID, c.AgentID, c.AgentName, c.ParentID, c.Subagent, c.Title, c.CreatedAt, c.UpdatedAt); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (p *PGStore) History(ctx context.Context, id uuid.UUID) ([]*schema.Message, int64, error) {
	rows, err := p.st.ListMessages(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	out := make([]*schema.Message, 0, len(rows))
	var last int64
	for _, r := range rows {
		var m schema.Message
		if err := json.Unmarshal(r.Message, &m); err != nil {
			return nil, 0, fmt.Errorf("message %d: %w", r.ID, err)
		}
		out = append(out, &m)
		last = r.ID
	}
	return out, last, nil
}

func (p *PGStore) AppendTurn(ctx context.Context, id uuid.UUID, lastID int64, msgs []*schema.Message, agentVersion int32, title string) error {
	return p.st.InTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		// Locking the conversation serialises turns across processes.
		current, err := q.LockConversation(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("conversation %s: %w", id, domain.ErrNotFound)
		}
		if err != nil {
			return err
		}
		if current != lastID {
			return fmt.Errorf("conversation %s changed while the agent was replying: %w", id, domain.ErrConflict)
		}
		for _, m := range msgs {
			raw, err := json.Marshal(m)
			if err != nil {
				return fmt.Errorf("encode message: %w", err)
			}
			var version *int32
			if m.Role != schema.User && agentVersion > 0 {
				version = &agentVersion
			}
			if _, err := q.AddMessage(ctx, db.AddMessageParams{
				ConversationID: id, Role: string(m.Role), Content: m.Content, Message: raw, AgentVersion: version,
			}); err != nil {
				return err
			}
		}
		if title != "" {
			if err := q.SetConversationTitle(ctx, db.SetConversationTitleParams{ID: id, Title: title}); err != nil {
				return err
			}
		}
		return q.TouchConversation(ctx, id)
	})
}
