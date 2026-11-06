package agent_test

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"

	"github.com/anas-domesticus/personal-agent/internal/agent"
)

func TestWindow(t *testing.T) {
	t.Parallel()

	msg := func(role schema.RoleType, n int) *schema.Message {
		return &schema.Message{Role: role, Content: strings.Repeat("x", n)}
	}
	msgs := []*schema.Message{
		msg(schema.User, 10), msg(schema.Assistant, 10),
		msg(schema.User, 10), msg(schema.Assistant, 10), msg(schema.Tool, 10), msg(schema.Assistant, 10),
		msg(schema.User, 10),
	}

	tests := []struct {
		name      string
		maxChars  int
		wantStart int // the window is msgs[wantStart:]
	}{
		{name: "no limit", maxChars: 0, wantStart: 0},
		// 45 characters reaches back into the tool result, which must not
		// lead, so the window starts at the next user message.
		{name: "never starts at a tool result", maxChars: 45, wantStart: 6},
		{name: "starts at a user message", maxChars: 50, wantStart: 2},
		{name: "the last user message is always sent", maxChars: 1, wantStart: 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := agent.Window(msgs, tt.maxChars)
			assert.Len(t, got, len(msgs)-tt.wantStart)
			assert.Same(t, msgs[tt.wantStart], got[0])
		})
	}
}

func TestSettled(t *testing.T) {
	t.Parallel()

	call := func(id string) *schema.Message {
		return schema.AssistantMessage("", []schema.ToolCall{{ID: id, Function: schema.FunctionCall{Name: "x"}}})
	}
	result := func(id string) *schema.Message { return schema.ToolMessage("r", id) }

	tests := []struct {
		name    string
		msgs    []*schema.Message
		wantLen int // Settled returns msgs[:wantLen]
	}{
		{name: "empty", msgs: nil, wantLen: 0},
		{name: "trailing unanswered call is dropped",
			msgs: []*schema.Message{call("1"), result("1"), call("2"), result("2"), call("3")}, wantLen: 4},
		{name: "only an unanswered call", msgs: []*schema.Message{call("1")}, wantLen: 0},
		{name: "user message before an unanswered call",
			msgs: []*schema.Message{schema.UserMessage("a"), call("1")}, wantLen: 1},
		{name: "everything answered",
			msgs: []*schema.Message{schema.UserMessage("a"), call("1"), result("1"), schema.AssistantMessage("done", nil)}, wantLen: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := agent.Settled(tt.msgs)
			assert.Len(t, got, tt.wantLen)
			if tt.wantLen > 0 {
				assert.Same(t, tt.msgs[tt.wantLen-1], got[len(got)-1])
			}
		})
	}
}
