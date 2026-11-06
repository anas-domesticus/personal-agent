package chat_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/anas-domesticus/personal-agent/internal/agent"
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

	id, err := e.Chat.Start(ctx, agent.AdminName)
	if err != nil {
		t.Fatal(err)
	}

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

	conv, err := e.Store.GetConversation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if conv.Title != "what agents exist?" {
		t.Fatalf("title = %q", conv.Title)
	}
}
