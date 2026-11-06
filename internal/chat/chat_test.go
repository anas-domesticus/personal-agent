package chat_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/chat"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/testenv"
)

func TestConversation(t *testing.T) {
	e := testenv.New(t, "fake/admin")
	ctx := context.Background()

	// First turn: call list_agents, then answer from its result.
	// Later turns: report how many messages the model was given.
	e.SetModel("fake/admin", func(msgs []*schema.Message) *schema.Message {
		last := msgs[len(msgs)-1]
		switch {
		case last.Role == schema.User && last.Content == "what agents exist?":
			return schema.AssistantMessage("", []schema.ToolCall{{
				ID: "call_1", Type: "function",
				Function: schema.FunctionCall{Name: "list_agents", Arguments: "{}"},
			}})
		case last.Role == schema.Tool:
			if strings.Contains(last.Content, `"name":"admin"`) {
				return schema.AssistantMessage("Just me: admin.", nil)
			}
			return schema.AssistantMessage("couldn't see agents", nil)
		default:
			return schema.AssistantMessage("history has "+strconv.Itoa(len(msgs))+" messages", nil)
		}
	})

	conv, err := e.Chat.Start(ctx, agent.AdminName)
	if err != nil {
		t.Fatal(err)
	}
	id := conv.ID

	var seen []schema.RoleType
	turn, err := e.Chat.Send(ctx, id, "what agents exist?", func(m *schema.Message) { seen = append(seen, m.Role) })
	if err != nil {
		t.Fatal(err)
	}
	if turn.Final != "Just me: admin." {
		t.Fatalf("final = %q", turn.Final)
	}
	if len(seen) != 3 { // assistant tool call, tool result, assistant answer
		t.Fatalf("streamed %v", seen)
	}

	history, err := e.Chat.History(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 {
		t.Fatalf("history = %d messages, want 4", len(history))
	}
	if tc := history[1].ToolCalls; len(tc) != 1 || tc[0].Function.Name != "list_agents" {
		t.Fatalf("tool call did not round-trip: %+v", history[1])
	}
	if history[2].Role != schema.Tool || history[2].ToolCallID != "call_1" {
		t.Fatalf("tool result did not round-trip: %+v", history[2])
	}

	// The next turn is given the system prompt, the 4 stored messages and the
	// new one.
	turn, err = e.Chat.Send(ctx, id, "and now?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Final != "history has 6 messages" {
		t.Fatalf("final = %q", turn.Final)
	}

	conv, err = e.Chat.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if conv.Title != "what agents exist?" {
		t.Fatalf("title = %q", conv.Title)
	}
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

	conv, err := e.Chat.Start(ctx, agent.AdminName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Chat.Send(ctx, conv.ID, "break", nil); err == nil {
		t.Fatal("expected the turn to fail")
	}
	if h, _ := e.Chat.History(ctx, conv.ID); len(h) != 0 {
		t.Fatalf("failed turn left %d messages", len(h))
	}

	// A turn whose caller goes away after the model replied is still saved.
	cctx, cancel := context.WithCancel(ctx)
	if _, err := e.Chat.Send(cctx, conv.ID, "hello", func(*schema.Message) { cancel() }); err != nil {
		t.Fatal(err)
	}
	if h, _ := e.Chat.History(ctx, conv.ID); len(h) != 2 {
		t.Fatalf("history = %d messages, want 2", len(h))
	}
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

	conv, err := e.Chat.Start(ctx, agent.AdminName)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() {
		_, err := e.Chat.Send(ctx, conv.ID, "first", nil)
		done <- err
	}()
	<-started
	if _, err := e.Chat.Send(ctx, conv.ID, "second", nil); !errors.Is(err, domain.ErrBusy) {
		t.Fatalf("concurrent send: got %v, want ErrBusy", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// Another process appending first makes the slower turn conflict
	// rather than interleave.
	store := chat.NewPGStore(e.Store)
	_, last, err := store.History(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTurn(ctx, conv.ID, last, []*schema.Message{schema.UserMessage("a")}, 1, ""); err != nil {
		t.Fatal(err)
	}
	err = store.AppendTurn(ctx, conv.ID, last, []*schema.Message{schema.UserMessage("b")}, 1, "")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale append: got %v, want ErrConflict", err)
	}
}

func TestWindow(t *testing.T) {
	msg := func(role schema.RoleType, n int) *schema.Message {
		return &schema.Message{Role: role, Content: strings.Repeat("x", n)}
	}
	msgs := []*schema.Message{
		msg(schema.User, 10), msg(schema.Assistant, 10),
		msg(schema.User, 10), msg(schema.Assistant, 10), msg(schema.Tool, 10), msg(schema.Assistant, 10),
		msg(schema.User, 10),
	}
	if got := agent.Window(msgs, 0); len(got) != 7 {
		t.Fatalf("no limit: %d", len(got))
	}
	// 45 characters reaches back into the tool result, which must not lead,
	// so the window starts at the next user message.
	if got := agent.Window(msgs, 45); len(got) != 1 || got[0] != msgs[6] {
		t.Fatalf("window(45) = %d messages", len(got))
	}
	if got := agent.Window(msgs, 50); len(got) != 5 || got[0] != msgs[2] {
		t.Fatalf("window(50) = %d messages", len(got))
	}
	// The last user message is always sent.
	if got := agent.Window(msgs, 1); len(got) != 1 || got[0] != msgs[6] {
		t.Fatalf("window(1) = %d messages", len(got))
	}
}

func TestSettled(t *testing.T) {
	call := func(id string) *schema.Message {
		return schema.AssistantMessage("", []schema.ToolCall{{ID: id, Function: schema.FunctionCall{Name: "x"}}})
	}
	result := func(id string) *schema.Message { return schema.ToolMessage("r", id) }
	msgs := []*schema.Message{call("1"), result("1"), call("2"), result("2"), call("3")}
	if got := agent.Settled(msgs); len(got) != 4 {
		t.Fatalf("settled = %d messages, want 4", len(got))
	}
	if got := agent.Settled(msgs[:1]); len(got) != 0 {
		t.Fatalf("an unanswered call should not be kept: %d", len(got))
	}
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
			ID: "c1", Type: "function", Function: schema.FunctionCall{Name: "remember", Arguments: `{"content":"likes tea"}`},
		}})
	})
	conv, err := e.Chat.Start(ctx, agent.AdminName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Chat.Send(ctx, conv.ID, "remember I like tea", nil); err == nil {
		t.Fatal("expected the turn to fail")
	}
	h, err := e.Chat.History(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	// user, tool call, tool result: the memory was saved, so the history says so.
	if len(h) != 3 || h[2].Role != schema.Tool {
		t.Fatalf("history = %d messages", len(h))
	}
}
