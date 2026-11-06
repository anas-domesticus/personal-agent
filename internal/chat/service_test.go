package chat_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/chat"
	"github.com/anas-domesticus/personal-agent/internal/domain"
)

// fakeRunnable replays one scripted turn and keeps the history it was given.
// It is a fake because agent.MockRunnable lives in package agent's test files
// and can't be imported here, and the tests assert on the recorded history.
type fakeRunnable struct {
	version int32
	turn    agent.Turn
	err     error

	mu      sync.Mutex
	history []*schema.Message
}

var _ agent.Runnable = (*fakeRunnable)(nil)

func (r *fakeRunnable) Info() agent.Info { return agent.Info{Name: "fake", Version: r.version} }

func (r *fakeRunnable) Run(_ context.Context, history []*schema.Message, onMessage func(*schema.Message)) (agent.Turn, error) {
	r.mu.Lock()
	r.history = history
	r.mu.Unlock()
	if onMessage != nil {
		for _, m := range r.turn.Messages {
			onMessage(m)
		}
	}
	return r.turn, r.err
}

func toolCall(id string) *schema.Message {
	return schema.AssistantMessage("", []schema.ToolCall{{ID: id, Type: "function", Function: schema.FunctionCall{Name: "x", Arguments: "{}"}}})
}

// msgsLike matches a message slice by role and content, since the service
// builds the user message itself.
func msgsLike(want ...*schema.Message) any {
	return mock.MatchedBy(func(got []*schema.Message) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i].Role != want[i].Role || got[i].Content != want[i].Content || len(got[i].ToolCalls) != len(want[i].ToolCalls) {
				return false
			}
		}
		return true
	})
}

func TestSend(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	answer := schema.AssistantMessage("hi", nil)
	call, result := toolCall("c1"), schema.ToolMessage("r", "c1")
	stored := []*schema.Message{schema.UserMessage("earlier"), schema.AssistantMessage("reply", nil)}
	errModel := errors.New("model failed")
	errSave := errors.New("database gone")
	long := strings.Repeat("a", 70)

	tests := []struct {
		name  string
		text  string
		conv  domain.Conversation
		agent *fakeRunnable
		setup func(s *chat.MockStore, a *chat.MockAgents, r *fakeRunnable)
		// wantHistory is what the agent is sent.
		wantHistory []*schema.Message
		wantFinal   string
		wantErrs    []error // each must be in the chain; nil means success
	}{
		{
			name:  "first turn is saved with a title",
			text:  "hello\nmore detail",
			conv:  domain.Conversation{ID: id, Agent: "admin"},
			agent: &fakeRunnable{version: 2, turn: agent.Turn{Final: "hi", Messages: []*schema.Message{answer}}},
			setup: func(s *chat.MockStore, a *chat.MockAgents, r *fakeRunnable) {
				s.EXPECT().History(mock.Anything, id).Return(nil, int64(0), nil).Once()
				a.EXPECT().Build(mock.Anything, "admin").Return(r, nil).Once()
				s.EXPECT().AppendTurn(mock.Anything, id, int64(0),
					msgsLike(schema.UserMessage("hello\nmore detail"), answer), int32(2), "hello").Return(nil).Once()
			},
			wantHistory: []*schema.Message{schema.UserMessage("hello\nmore detail")},
			wantFinal:   "hi",
		},
		{
			name:  "later turn keeps the title and sends the history",
			text:  "again",
			conv:  domain.Conversation{ID: id, Agent: "admin", Title: "set"},
			agent: &fakeRunnable{version: 1, turn: agent.Turn{Final: "hi", Messages: []*schema.Message{answer}}},
			setup: func(s *chat.MockStore, a *chat.MockAgents, r *fakeRunnable) {
				s.EXPECT().History(mock.Anything, id).Return(stored, int64(7), nil).Once()
				a.EXPECT().Build(mock.Anything, "admin").Return(r, nil).Once()
				s.EXPECT().AppendTurn(mock.Anything, id, int64(7),
					msgsLike(schema.UserMessage("again"), answer), int32(1), "").Return(nil).Once()
			},
			wantHistory: []*schema.Message{stored[0], stored[1], schema.UserMessage("again")},
			wantFinal:   "hi",
		},
		{
			name:  "long first message is cut for the title",
			text:  long,
			conv:  domain.Conversation{ID: id, Agent: "admin"},
			agent: &fakeRunnable{turn: agent.Turn{Messages: []*schema.Message{answer}}},
			setup: func(s *chat.MockStore, a *chat.MockAgents, r *fakeRunnable) {
				s.EXPECT().History(mock.Anything, id).Return(nil, int64(0), nil).Once()
				a.EXPECT().Build(mock.Anything, "admin").Return(r, nil).Once()
				s.EXPECT().AppendTurn(mock.Anything, id, int64(0), mock.Anything, int32(0), long[:57]+"...").Return(nil).Once()
			},
			wantHistory: []*schema.Message{schema.UserMessage(long)},
		},
		{
			name:  "subagent conversation builds its spec at depth 1",
			text:  "steer",
			conv:  domain.Conversation{ID: id, Title: "t", Subagent: &domain.SubagentSpec{Model: "fake/worker"}},
			agent: &fakeRunnable{turn: agent.Turn{Messages: []*schema.Message{answer}}},
			setup: func(s *chat.MockStore, a *chat.MockAgents, r *fakeRunnable) {
				s.EXPECT().History(mock.Anything, id).Return(nil, int64(0), nil).Once()
				a.EXPECT().BuildSpec(mock.Anything, domain.SubagentSpec{Model: "fake/worker"}, 1).Return(r, nil).Once()
				s.EXPECT().AppendTurn(mock.Anything, id, int64(0), mock.Anything, int32(0), "").Return(nil).Once()
			},
			wantHistory: []*schema.Message{schema.UserMessage("steer")},
		},
		{
			name: "agent that can't be built",
			text: "hello",
			conv: domain.Conversation{ID: id, Agent: "ghost"},
			setup: func(s *chat.MockStore, a *chat.MockAgents, _ *fakeRunnable) {
				s.EXPECT().History(mock.Anything, id).Return(nil, int64(0), nil).Once()
				a.EXPECT().Build(mock.Anything, "ghost").Return(nil, domain.ErrNotFound).Once()
			},
			wantErrs: []error{domain.ErrNotFound},
		},
		{
			name:  "failure before any tool ran saves nothing",
			text:  "hello",
			conv:  domain.Conversation{ID: id, Agent: "admin"},
			agent: &fakeRunnable{turn: agent.Turn{Messages: []*schema.Message{call}}, err: errModel},
			setup: func(s *chat.MockStore, a *chat.MockAgents, r *fakeRunnable) {
				s.EXPECT().History(mock.Anything, id).Return(nil, int64(0), nil).Once()
				a.EXPECT().Build(mock.Anything, "admin").Return(r, nil).Once()
			},
			wantHistory: []*schema.Message{schema.UserMessage("hello")},
			wantErrs:    []error{errModel},
		},
		{
			name:  "failure after a tool ran saves the settled part",
			text:  "hello",
			conv:  domain.Conversation{ID: id, Agent: "admin"},
			agent: &fakeRunnable{turn: agent.Turn{Messages: []*schema.Message{call, result, toolCall("c2")}}, err: errModel},
			setup: func(s *chat.MockStore, a *chat.MockAgents, r *fakeRunnable) {
				s.EXPECT().History(mock.Anything, id).Return(nil, int64(0), nil).Once()
				a.EXPECT().Build(mock.Anything, "admin").Return(r, nil).Once()
				s.EXPECT().AppendTurn(mock.Anything, id, int64(0),
					msgsLike(schema.UserMessage("hello"), call, result), int32(0), "hello").Return(nil).Once()
			},
			wantHistory: []*schema.Message{schema.UserMessage("hello")},
			wantErrs:    []error{errModel},
		},
		{
			name:  "failing to save a partial turn reports both errors",
			text:  "hello",
			conv:  domain.Conversation{ID: id, Agent: "admin"},
			agent: &fakeRunnable{turn: agent.Turn{Messages: []*schema.Message{call, result}}, err: errModel},
			setup: func(s *chat.MockStore, a *chat.MockAgents, r *fakeRunnable) {
				s.EXPECT().History(mock.Anything, id).Return(nil, int64(0), nil).Once()
				a.EXPECT().Build(mock.Anything, "admin").Return(r, nil).Once()
				s.EXPECT().AppendTurn(mock.Anything, id, int64(0), mock.Anything, int32(0), "hello").Return(errSave).Once()
			},
			wantHistory: []*schema.Message{schema.UserMessage("hello")},
			wantErrs:    []error{errModel, errSave},
		},
		{
			name:  "a turn that lost the race conflicts",
			text:  "hello",
			conv:  domain.Conversation{ID: id, Agent: "admin", Title: "t"},
			agent: &fakeRunnable{turn: agent.Turn{Final: "hi", Messages: []*schema.Message{answer}}},
			setup: func(s *chat.MockStore, a *chat.MockAgents, r *fakeRunnable) {
				s.EXPECT().History(mock.Anything, id).Return(nil, int64(0), nil).Once()
				a.EXPECT().Build(mock.Anything, "admin").Return(r, nil).Once()
				s.EXPECT().AppendTurn(mock.Anything, id, int64(0), mock.Anything, int32(0), "").Return(domain.ErrConflict).Once()
			},
			wantHistory: []*schema.Message{schema.UserMessage("hello")},
			wantFinal:   "hi",
			wantErrs:    []error{domain.ErrConflict},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := chat.NewMockStore(t)
			agents := chat.NewMockAgents(t)
			store.EXPECT().Get(mock.Anything, id).Return(tt.conv, nil).Once()
			tt.setup(store, agents, tt.agent)

			var streamed int
			turn, err := chat.New(store, agents).Send(context.Background(), id, tt.text, func(*schema.Message) { streamed++ })
			if tt.agent != nil {
				assert.Equal(t, len(tt.agent.turn.Messages), streamed, "messages streamed")
				assert.Equal(t, tt.wantHistory, tt.agent.history)
			}
			assert.Equal(t, tt.wantFinal, turn.Final)
			if tt.wantErrs != nil {
				require.Error(t, err)
				for _, want := range tt.wantErrs {
					require.ErrorIs(t, err, want)
				}
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestSendStoreErrors(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	tests := []struct {
		name    string
		setup   func(s *chat.MockStore)
		wantErr error
	}{
		{
			name: "unknown conversation",
			setup: func(s *chat.MockStore) {
				s.EXPECT().Get(mock.Anything, id).Return(domain.Conversation{}, domain.ErrNotFound).Once()
			},
			wantErr: domain.ErrNotFound,
		},
		{
			name: "history can't be read",
			setup: func(s *chat.MockStore) {
				s.EXPECT().Get(mock.Anything, id).Return(domain.Conversation{ID: id, Agent: "admin"}, nil).Once()
				s.EXPECT().History(mock.Anything, id).Return(nil, int64(0), domain.ErrNotFound).Once()
			},
			wantErr: domain.ErrNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := chat.NewMockStore(t)
			tt.setup(store)
			_, err := chat.New(store, chat.NewMockAgents(t)).Send(context.Background(), id, "hello", nil)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestSendWindowsHistory(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	old := []*schema.Message{
		schema.UserMessage(strings.Repeat("o", 100)), schema.AssistantMessage(strings.Repeat("o", 100), nil),
		schema.UserMessage("recent"), schema.AssistantMessage("ok", nil),
	}
	r := &fakeRunnable{turn: agent.Turn{Messages: []*schema.Message{schema.AssistantMessage("hi", nil)}}}
	store := chat.NewMockStore(t)
	agents := chat.NewMockAgents(t)
	store.EXPECT().Get(mock.Anything, id).Return(domain.Conversation{ID: id, Agent: "admin", Title: "t"}, nil).Once()
	store.EXPECT().History(mock.Anything, id).Return(old, int64(4), nil).Once()
	agents.EXPECT().Build(mock.Anything, "admin").Return(r, nil).Once()
	store.EXPECT().AppendTurn(mock.Anything, id, int64(4), mock.Anything, int32(0), "").Return(nil).Once()

	s := chat.New(store, agents)
	s.HistoryChars = 50
	_, err := s.Send(context.Background(), id, "now", nil)
	require.NoError(t, err)
	// Older messages stay stored but aren't sent.
	assert.Equal(t, []*schema.Message{old[2], old[3], schema.UserMessage("now")}, r.history)
}

func TestPassThrough(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	id := uuid.New()
	conv := domain.Conversation{ID: id, Agent: "admin", Title: "t"}
	msgs := []*schema.Message{schema.UserMessage("a")}

	store := chat.NewMockStore(t)
	store.EXPECT().Create(mock.Anything, "admin").Return(conv, nil).Once()
	store.EXPECT().Get(mock.Anything, id).Return(conv, nil).Once()
	store.EXPECT().List(mock.Anything, int32(5)).Return([]domain.Conversation{conv}, nil).Once()
	store.EXPECT().History(mock.Anything, id).Return(msgs, int64(1), nil).Once()
	s := chat.New(store, chat.NewMockAgents(t))

	got, err := s.Start(ctx, "admin")
	require.NoError(t, err)
	assert.Equal(t, conv, got)
	got, err = s.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, conv, got)
	list, err := s.List(ctx, 5)
	require.NoError(t, err)
	assert.Equal(t, []domain.Conversation{conv}, list)
	h, err := s.History(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, msgs, h)
}
