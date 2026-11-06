package server_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/client"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	agentv1 "github.com/anas-domesticus/personal-agent/internal/gen/agent/v1"
	"github.com/anas-domesticus/personal-agent/internal/server"
)

const token = "secret"

// serve starts the API over the given services and returns a client for it.
func serve(t *testing.T, chat server.Chat, agents server.Agents) *client.Client {
	t.Helper()
	srv := httptest.NewServer(server.New(server.Deps{Chat: chat, Agents: agents, Token: token}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL, token)
}

func TestToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		token    string
		wantCode connect.Code // 0 means success
	}{
		{name: "right token", token: token},
		{name: "wrong token", token: "guess", wantCode: connect.CodeUnauthenticated},
		{name: "no token", token: "", wantCode: connect.CodeUnauthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			chat := server.NewMockChat(t)
			if tt.wantCode == 0 {
				chat.EXPECT().List(mock.Anything, int32(50)).Return(nil, nil).Once()
			}
			c := client.New(serve(t, chat, server.NewMockAgents(t)).URL, tt.token)

			_, err := c.Conversations.ListConversations(context.Background(), connect.NewRequest(&agentv1.ListConversationsRequest{}))

			if tt.wantCode == 0 {
				require.NoError(t, err)
				return
			}
			assert.Equal(t, tt.wantCode, connect.CodeOf(err))
		})
	}
}

func TestErrorCodes(t *testing.T) {
	t.Parallel()
	id := uuid.New()

	tests := []struct {
		name     string
		id       string
		setup    func(m *server.MockChat)
		wantCode connect.Code
	}{
		{name: "malformed id", id: "nope", wantCode: connect.CodeInvalidArgument},
		{name: "not found", id: id.String(), wantCode: connect.CodeNotFound,
			setup: func(m *server.MockChat) {
				m.EXPECT().Get(mock.Anything, id).Return(domain.Conversation{}, fmt.Errorf("conversation: %w", domain.ErrNotFound)).Once()
			}},
		{name: "conflict", id: id.String(), wantCode: connect.CodeAborted,
			setup: func(m *server.MockChat) {
				m.EXPECT().Get(mock.Anything, id).Return(domain.Conversation{}, domain.ErrConflict).Once()
			}},
		{name: "shutting down", id: id.String(), wantCode: connect.CodeUnavailable,
			setup: func(m *server.MockChat) {
				m.EXPECT().Get(mock.Anything, id).Return(domain.Conversation{}, fmt.Errorf("chat: %w", domain.ErrUnavailable)).Once()
			}},
		{name: "anything else is internal", id: id.String(), wantCode: connect.CodeInternal,
			setup: func(m *server.MockChat) {
				m.EXPECT().Get(mock.Anything, id).Return(domain.Conversation{}, errors.New("database on fire")).Once()
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			chat := server.NewMockChat(t)
			if tt.setup != nil {
				tt.setup(chat)
			}
			c := serve(t, chat, server.NewMockAgents(t))

			_, err := c.Conversations.GetConversation(context.Background(), connect.NewRequest(&agentv1.GetConversationRequest{Id: tt.id}))

			assert.Equal(t, tt.wantCode, connect.CodeOf(err))
		})
	}
}

func TestSendMessageBeforeTheTurnStarts(t *testing.T) {
	t.Parallel()
	id := uuid.New()

	tests := []struct {
		name     string
		text     string
		setup    func(m *server.MockChat)
		wantCode connect.Code
	}{
		{name: "empty text", text: "  ", wantCode: connect.CodeInvalidArgument},
		{name: "busy conversation", text: "hi", wantCode: connect.CodeFailedPrecondition,
			setup: func(m *server.MockChat) {
				m.EXPECT().Send(mock.Anything, id, "hi", mock.Anything).Return(agent.Turn{}, fmt.Errorf("conversation: %w", domain.ErrBusy)).Once()
			}},
		{name: "agent can't be built", text: "hi", wantCode: connect.CodeInternal,
			setup: func(m *server.MockChat) {
				m.EXPECT().Send(mock.Anything, id, "hi", mock.Anything).Return(agent.Turn{}, errors.New("no model access")).Once()
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			chat := server.NewMockChat(t)
			if tt.setup != nil {
				tt.setup(chat)
			}
			c := serve(t, chat, server.NewMockAgents(t))

			_, err := receiveAll(c, id.String(), tt.text)

			assert.Equal(t, tt.wantCode, connect.CodeOf(err))
		})
	}
}

func TestSendMessageTurnOutlivesTheClient(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	chat := server.NewMockChat(t)
	sent := make(chan struct{})
	finished := make(chan context.Context, 1)
	chat.EXPECT().Send(mock.Anything, id, "go", mock.Anything).
		RunAndReturn(func(ctx context.Context, _ uuid.UUID, _ string, obs agent.Observer) (agent.Turn, error) {
			obs.Started(agent.Info{Name: "admin"})
			<-sent // the client has given up by now
			finished <- ctx
			return agent.Turn{}, nil
		}).Once()
	chat.EXPECT().Get(mock.Anything, id).Return(domain.Conversation{ID: id}, nil).Maybe()
	c := serve(t, chat, server.NewMockAgents(t))

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := c.Conversations.SendMessage(ctx, connect.NewRequest(&agentv1.SendMessageRequest{ConversationId: id.String(), Text: "go"}))
	require.NoError(t, err)
	require.True(t, stream.Receive(), "want the started event")
	cancel()
	close(sent)

	select {
	case turnCtx := <-finished:
		require.NoError(t, turnCtx.Err(), "the turn's context must not end with the request")
	case <-time.After(5 * time.Second):
		t.Fatal("turn never finished")
	}
}

func TestCreateConversationDefaultsToAdmin(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	chat := server.NewMockChat(t)
	chat.EXPECT().Start(mock.Anything, "admin").Return(domain.Conversation{ID: id, Agent: "admin"}, nil).Once()
	c := serve(t, chat, server.NewMockAgents(t))

	res, err := c.Conversations.CreateConversation(context.Background(), connect.NewRequest(&agentv1.CreateConversationRequest{}))

	require.NoError(t, err)
	assert.Equal(t, id.String(), res.Msg.Conversation.Id)
	assert.Equal(t, "admin", res.Msg.Conversation.Agent)
}

func TestGetConversationConvertsMessages(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	chat := server.NewMockChat(t)
	chat.EXPECT().Get(mock.Anything, id).Return(domain.Conversation{ID: id, Agent: "admin", Title: "hi"}, nil).Once()
	chat.EXPECT().History(mock.Anything, id).Return([]*schema.Message{
		schema.UserMessage("hi"),
		{Role: schema.Assistant, ReasoningContent: "hmm", ToolCalls: []schema.ToolCall{{ID: "c1", Function: schema.FunctionCall{Name: "shell", Arguments: `{"command":"ls"}`}}}},
		schema.ToolMessage("a.txt", "c1", schema.WithToolName("shell")),
	}, nil).Once()
	chat.EXPECT().Busy(id).Return(true).Once()
	c := serve(t, chat, server.NewMockAgents(t))

	res, err := c.Conversations.GetConversation(context.Background(), connect.NewRequest(&agentv1.GetConversationRequest{Id: id.String()}))

	require.NoError(t, err)
	assert.True(t, res.Msg.Busy)
	want := []*agentv1.Message{
		{Role: agentv1.Role_ROLE_USER, Content: "hi"},
		{Role: agentv1.Role_ROLE_ASSISTANT, Reasoning: "hmm", ToolCalls: []*agentv1.ToolCall{{Id: "c1", Name: "shell", Arguments: `{"command":"ls"}`}}},
		{Role: agentv1.Role_ROLE_TOOL, Content: "a.txt", ToolCallId: "c1", ToolName: "shell"},
	}
	require.Len(t, res.Msg.Messages, len(want))
	for i := range want {
		assert.Equal(t, want[i].String(), res.Msg.Messages[i].String(), "message %d", i)
	}
}

func TestGetAgent(t *testing.T) {
	t.Parallel()
	agents := server.NewMockAgents(t)
	agents.EXPECT().Get(mock.Anything, "admin").Return(domain.AgentDetail{
		Agent:   domain.Agent{Name: "admin", Description: "The main agent.", Admin: true},
		Current: &domain.AgentVersion{Version: 3, Model: "m", Config: domain.AgentConfig{Tools: domain.On("shell", "read")}},
	}, nil).Once()
	c := serve(t, server.NewMockChat(t), agents)

	res, err := c.Agents.GetAgent(context.Background(), connect.NewRequest(&agentv1.GetAgentRequest{Name: "admin"}))

	require.NoError(t, err)
	got := res.Msg.Agent
	assert.Equal(t, "admin", got.Name)
	assert.True(t, got.Admin)
	assert.Equal(t, int32(3), got.Version)
	assert.Equal(t, "m", got.Model)
	assert.Equal(t, []string{"read", "shell"}, got.Tools)
}

// receiveAll sends a message and collects the turn's events.
func receiveAll(c *client.Client, id, text string) ([]*agentv1.SendMessageResponse, error) {
	stream, err := c.Conversations.SendMessage(context.Background(), connect.NewRequest(&agentv1.SendMessageRequest{ConversationId: id, Text: text}))
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var evs []*agentv1.SendMessageResponse
	for stream.Receive() {
		evs = append(evs, stream.Msg())
	}
	return evs, stream.Err() //nolint:wrapcheck // the test checks the Connect code
}
