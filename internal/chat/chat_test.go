package chat_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/chat"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/testenv"
)

// These tests run whole turns on testenv's stack with scripted models. The
// logic of Send on its own is unit-tested in service_test.go. Each test is a
// sequence of turns over one conversation, so the steps run in order.

func TestConversation(t *testing.T) {
	e := testenv.New(t, "fake/admin")
	ctx := context.Background()

	// First turn: call current_time, then answer from its result.
	// Later turns: report how many messages the model was given.
	e.SetModel("fake/admin", func(msgs []*schema.Message) *schema.Message {
		last := msgs[len(msgs)-1]
		switch {
		case last.Role == schema.User && last.Content == "what agents exist?":
			return schema.AssistantMessage("", []schema.ToolCall{{
				ID: "call_1", Type: "function",
				Function: schema.FunctionCall{Name: "current_time", Arguments: "{}"},
			}})
		case last.Role == schema.Tool:
			return schema.AssistantMessage("Just me: admin.", nil)
		default:
			return schema.AssistantMessage("history has "+strconv.Itoa(len(msgs))+" messages", nil)
		}
	})

	conv, err := e.Chat.Start(ctx, testenv.DefaultAgent)
	require.NoError(t, err)
	id := conv.ID

	var seen []schema.RoleType
	turn, err := e.Chat.Send(ctx, id, "what agents exist?", agent.Observer{Message: func(m *schema.Message) { seen = append(seen, m.Role) }})
	require.NoError(t, err)
	assert.Equal(t, "Just me: admin.", turn.Final)
	// assistant tool call, tool result, assistant answer
	assert.Equal(t, []schema.RoleType{schema.Assistant, schema.Tool, schema.Assistant}, seen)

	history, err := e.Chat.History(ctx, id)
	require.NoError(t, err)
	require.Len(t, history, 4)
	require.Len(t, history[1].ToolCalls, 1, "tool call did not round-trip")
	assert.Equal(t, "current_time", history[1].ToolCalls[0].Function.Name)
	assert.Equal(t, schema.Tool, history[2].Role, "tool result did not round-trip")
	assert.Equal(t, "call_1", history[2].ToolCallID, "tool result did not round-trip")

	// The next turn is given the system prompt, the 4 stored messages and the
	// new one.
	turn, err = e.Chat.Send(ctx, id, "and now?", agent.Observer{})
	require.NoError(t, err)
	assert.Equal(t, "history has 6 messages", turn.Final)

	conv, err = e.Chat.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "what agents exist?", conv.Title)
}

func TestFailedTurnLeavesNoTrace(t *testing.T) {
	e := testenv.New(t, "fake/admin")
	ctx := context.Background()
	e.SetModel("fake/admin", func(msgs []*schema.Message) *schema.Message {
		if msgs[len(msgs)-1].Content == "break" {
			return nil // the fake model turns nil into an error
		}
		return schema.AssistantMessage("fine", nil)
	})

	conv, err := e.Chat.Start(ctx, testenv.DefaultAgent)
	require.NoError(t, err)
	_, err = e.Chat.Send(ctx, conv.ID, "break", agent.Observer{})
	require.Error(t, err, "expected the turn to fail")
	h, err := e.Chat.History(ctx, conv.ID)
	require.NoError(t, err)
	assert.Empty(t, h, "failed turn left messages")

	// A turn whose caller goes away after the model replied is still saved.
	cctx, cancel := context.WithCancel(ctx)
	_, err = e.Chat.Send(cctx, conv.ID, "hello", agent.Observer{Message: func(*schema.Message) { cancel() }})
	require.NoError(t, err)
	h, err = e.Chat.History(ctx, conv.ID)
	require.NoError(t, err)
	assert.Len(t, h, 2)
}

func TestOneTurnAtATime(t *testing.T) {
	e := testenv.New(t, "fake/admin")
	ctx := context.Background()
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	e.SetModel("fake/admin", func([]*schema.Message) *schema.Message {
		started <- struct{}{}
		<-release
		return schema.AssistantMessage("done", nil)
	})

	conv, err := e.Chat.Start(ctx, testenv.DefaultAgent)
	require.NoError(t, err)
	done := make(chan error)
	go func() {
		_, err := e.Chat.Send(ctx, conv.ID, "first", agent.Observer{})
		done <- err
	}()
	<-started
	_, err = e.Chat.Send(ctx, conv.ID, "second", agent.Observer{})
	require.ErrorIs(t, err, domain.ErrBusy, "concurrent send")
	close(release)
	require.NoError(t, <-done)

	// Another process appending first makes the slower turn conflict
	// rather than interleave.
	store := chat.NewPGStore(e.Store)
	_, last, err := store.History(ctx, conv.ID)
	require.NoError(t, err)
	require.NoError(t, store.AppendTurn(ctx, conv.ID, last, []*schema.Message{schema.UserMessage("a")}, 1, ""))
	err = store.AppendTurn(ctx, conv.ID, last, []*schema.Message{schema.UserMessage("b")}, 1, "")
	require.ErrorIs(t, err, domain.ErrConflict, "stale append")
}

func TestFailedTurnAfterToolsIsKept(t *testing.T) {
	e := testenv.New(t, "fake/admin")
	ctx := context.Background()
	// Call a tool, then fail.
	e.SetModel("fake/admin", func(msgs []*schema.Message) *schema.Message {
		if msgs[len(msgs)-1].Role == schema.Tool {
			return nil
		}
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "current_time", Arguments: `{}`},
		}})
	})
	conv, err := e.Chat.Start(ctx, testenv.DefaultAgent)
	require.NoError(t, err)
	_, err = e.Chat.Send(ctx, conv.ID, "what time is it?", agent.Observer{})
	require.Error(t, err, "expected the turn to fail")
	h, err := e.Chat.History(ctx, conv.ID)
	require.NoError(t, err)
	// user, tool call, tool result: the successful tool call stays in history.
	require.Len(t, h, 3)
	assert.Equal(t, schema.Tool, h[2].Role)
}
