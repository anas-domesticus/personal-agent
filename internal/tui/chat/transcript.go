package chat

import (
	"strings"
	"time"

	agentv1 "github.com/anas-domesticus/personal-agent/internal/gen/agent/v1"
)

// Transcript is the conversation as the TUI shows it: what the server
// stored, plus what is streaming and the TUI's own notices.
type Transcript struct {
	env   *Env
	items []Item
	// calls finds a tool call by id, to attach its result.
	calls map[string]*Tool
}

// NewTranscript returns an empty transcript.
func NewTranscript(env *Env) *Transcript {
	return &Transcript{env: env, calls: map[string]*Tool{}}
}

// FromMessages rebuilds a transcript from stored history.
func FromMessages(env *Env, msgs []*agentv1.Message) *Transcript {
	t := NewTranscript(env)
	for _, m := range msgs {
		t.Message(m)
	}
	return t
}

// Items returns the items in order.
func (t *Transcript) Items() []Item { return t.items }

// Len is the number of items.
func (t *Transcript) Len() int { return len(t.items) }

// Add appends an item.
func (t *Transcript) Add(it Item) Item {
	t.items = append(t.items, it)
	return it
}

// User appends a user message.
func (t *Transcript) User(text string) { t.Add(NewUser(t.env, text)) }

// Notice appends a notice.
func (t *Transcript) Notice(text string) { t.Add(NewNotice(t.env, text)) }

// Error appends an error banner.
func (t *Transcript) Error(title, detail string) { t.Add(NewError(t.env, title, detail)) }

// Info appends a turn footer.
func (t *Transcript) Info(model string, d time.Duration) { t.Add(NewInfo(t.env, model, d)) }

// streaming returns the assistant message being written, if it is last.
func (t *Transcript) streaming() *Assistant {
	if n := len(t.items); n > 0 {
		if a, ok := t.items[n-1].(*Assistant); ok && a.Streaming() {
			return a
		}
	}
	return nil
}

// Pending shows that the agent is working on a reply: a streaming
// assistant message with a spinner, unless one is already last.
func (t *Transcript) Pending(turnStart time.Time) {
	if t.streaming() == nil {
		t.Add(NewStreaming(t.env, turnStart))
	}
}

// Delta appends a streamed fragment of the reply.
func (t *Transcript) Delta(d *agentv1.MessageDelta) {
	a := t.streaming()
	if a == nil {
		a = NewStreaming(t.env, time.Now())
		t.Add(a)
	}
	a.Append(d.Content, d.Reasoning)
}

// Message records a complete message. A complete assistant message replaces
// the fragments streamed for it; a tool result is attached to its call.
func (t *Transcript) Message(m *agentv1.Message) {
	switch m.Role {
	case agentv1.Role_ROLE_USER:
		t.User(m.Content)
	case agentv1.Role_ROLE_ASSISTANT:
		hasText := strings.TrimSpace(m.Content) != "" || strings.TrimSpace(m.Reasoning) != ""
		a := t.streaming()
		switch {
		case a != nil && hasText:
			a.Finish(m.Content, m.Reasoning)
		case a != nil:
			t.items = t.items[:len(t.items)-1] // only tool calls: drop the placeholder
		case hasText:
			t.Add(NewAssistant(t.env, m.Content, m.Reasoning))
		}
		for _, tc := range m.ToolCalls {
			tool := NewTool(t.env, tc.Id, tc.Name, tc.Arguments)
			t.calls[tc.Id] = tool
			t.Add(tool)
		}
	case agentv1.Role_ROLE_TOOL:
		if tool, ok := t.calls[m.ToolCallId]; ok {
			tool.SetResult(m.Content)
			return
		}
		// A result without its call, e.g. history cut by the window.
		tool := NewTool(t.env, m.ToolCallId, m.ToolName, "")
		tool.SetResult(m.Content)
		t.Add(tool)
	case agentv1.Role_ROLE_SYSTEM, agentv1.Role_ROLE_UNSPECIFIED:
	}
}

// Settle ends a turn: the streaming message is finished (or dropped if it
// is empty, or marked interrupted when canceled), and tools still running
// are marked interrupted.
func (t *Transcript) Settle(canceled bool) {
	if a := t.streaming(); a != nil {
		switch {
		case a.Empty():
			t.items = t.items[:len(t.items)-1]
		case canceled:
			a.Cancel()
		default:
			a.Settle()
		}
	}
	// A call still running when its turn ends never gets a result.
	for _, tool := range t.calls {
		tool.Cancel()
	}
}

// DropLastUser removes the last item if it is a user message with text.
func (t *Transcript) DropLastUser(text string) {
	if n := len(t.items); n > 0 {
		if u, ok := t.items[n-1].(*User); ok && u.Content() == text {
			t.items = t.items[:n-1]
		}
	}
}

// Restyle redraws every item after a theme change.
func (t *Transcript) Restyle() {
	for _, it := range t.items {
		if r, ok := it.(interface{ Restyle() }); ok {
			r.Restyle()
		}
	}
}
