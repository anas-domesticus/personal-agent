package server_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/anas-domesticus/personal-agent/internal/gen/agent/v1"
	"github.com/anas-domesticus/personal-agent/internal/testenv"
)

// The whole stack behind the API: real chat, agents and Postgres, scripted
// models that stream a word at a time.

func TestStackTurnIsStreamed(t *testing.T) {
	t.Parallel()
	e := testenv.New(t, "fake/admin")
	e.SetModel("fake/admin", func(msgs []*schema.Message) *schema.Message {
		if msgs[len(msgs)-1].Role == schema.User {
			return schema.AssistantMessage("", []schema.ToolCall{{
				ID: "call_1", Type: "function", Function: schema.FunctionCall{Name: "current_time", Arguments: "{}"},
			}})
		}
		return schema.AssistantMessage("It is now, more or less.", nil)
	})
	c := serve(t, e.Chat, e.Agents)
	ctx := context.Background()
	created, err := c.Conversations.CreateConversation(ctx, connect.NewRequest(&agentv1.CreateConversationRequest{}))
	require.NoError(t, err)
	id := created.Msg.Conversation.Id

	evs, err := receiveAll(c, id, "what time is it?")
	require.NoError(t, err)

	// started, then deltas and messages, then completed.
	require.GreaterOrEqual(t, len(evs), 3)
	started := evs[0].GetStarted()
	require.NotNil(t, started, "first event")
	assert.Equal(t, "admin", started.Agent)
	assert.Equal(t, "fake/admin", started.Model)
	var deltas strings.Builder
	var roles []agentv1.Role
	for _, ev := range evs[1 : len(evs)-1] {
		switch {
		case ev.GetDelta() != nil:
			deltas.WriteString(ev.GetDelta().Content)
		case ev.GetMessage() != nil:
			roles = append(roles, ev.GetMessage().Role)
		default:
			t.Fatalf("unexpected event mid-turn: %v", ev)
		}
	}
	assert.Equal(t, "It is now, more or less.", deltas.String())
	assert.Equal(t, []agentv1.Role{agentv1.Role_ROLE_ASSISTANT, agentv1.Role_ROLE_TOOL, agentv1.Role_ROLE_ASSISTANT}, roles)
	done := evs[len(evs)-1].GetCompleted()
	require.NotNil(t, done, "last event")
	assert.Equal(t, agentv1.TurnStatus_TURN_STATUS_COMPLETED, done.Status)
	assert.Equal(t, "what time is it?", done.Conversation.Title)

	got, err := c.Conversations.GetConversation(ctx, connect.NewRequest(&agentv1.GetConversationRequest{Id: id}))
	require.NoError(t, err)
	assert.Len(t, got.Msg.Messages, 4)
	assert.False(t, got.Msg.Busy)
}

func TestStackCancelInterruptsTheTurn(t *testing.T) {
	t.Parallel()
	e := testenv.New(t, "fake/admin")
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	e.SetModel("fake/admin", func([]*schema.Message) *schema.Message {
		started <- struct{}{}
		<-release
		return schema.AssistantMessage("too late", nil)
	})
	c := serve(t, e.Chat, e.Agents)
	ctx := context.Background()
	created, err := c.Conversations.CreateConversation(ctx, connect.NewRequest(&agentv1.CreateConversationRequest{}))
	require.NoError(t, err)
	id := created.Msg.Conversation.Id

	type result struct {
		evs []*agentv1.SendMessageResponse
		err error
	}
	results := make(chan result, 1)
	go func() {
		evs, err := receiveAll(c, id, "slow one")
		results <- result{evs, err}
	}()
	<-started

	_, err = receiveAll(c, id, "again")
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a second message while busy")

	res, err := c.Conversations.CancelTurn(ctx, connect.NewRequest(&agentv1.CancelTurnRequest{ConversationId: id}))
	require.NoError(t, err)
	assert.True(t, res.Msg.Cancelled)
	close(release)

	select {
	case r := <-results:
		require.NoError(t, r.err)
		done := r.evs[len(r.evs)-1].GetCompleted()
		require.NotNil(t, done)
		assert.Equal(t, agentv1.TurnStatus_TURN_STATUS_INTERRUPTED, done.Status)
	case <-time.After(10 * time.Second):
		t.Fatal("turn did not end after cancel")
	}
}
