package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentv1 "github.com/anas-domesticus/personal-agent/internal/gen/agent/v1"
	"github.com/anas-domesticus/personal-agent/internal/tui/styles"
)

func env() *Env { return NewEnv(styles.New(true)) }

func TestStableCut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string // the stable prefix
	}{
		{name: "no paragraph break", in: "one line", want: ""},
		{name: "after the last blank line", in: "para one\n\npara two\n\nhalf", want: "para one\n\npara two\n\n"},
		{name: "not inside a code block", in: "intro\n\n```\ncode\n\nmore", want: "intro\n\n"},
		{name: "after a closed code block", in: "```\ncode\n```\n\ntail", want: "```\ncode\n```\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.in[:stableCut(tt.in)])
		})
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     string
		wantMain string
		wantRest []string
	}{
		{name: "main parameter first", args: `{"timeout":5,"command":"ls  -la"}`, wantMain: "ls -la", wantRest: []string{"timeout=5"}},
		{name: "no main parameter", args: `{"b":2,"a":"x"}`, wantMain: "a=x", wantRest: []string{"b=2"}},
		{name: "no arguments", args: `{}`},
		{name: "not JSON", args: "raw  text", wantMain: "raw text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			main, rest := summarize(tt.args)
			assert.Equal(t, tt.wantMain, main)
			assert.Equal(t, tt.wantRest, rest)
		})
	}
}

func TestToolResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		content    string
		wantState  ToolState
		wantOutput string
	}{
		{name: "data is unwrapped", content: `{"ok":true,"data":{"n":1}}`, wantState: ToolDone, wantOutput: `{"n":1}`},
		{name: "ok with no data", content: `{"ok":true}`, wantState: ToolDone},
		{name: "failure keeps the error", content: `{"ok":false,"error":"denied"}`, wantState: ToolFailed, wantOutput: "denied"},
		{name: "anything else as is", content: "plain", wantState: ToolDone, wantOutput: "plain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tool := NewTool(env(), "c1", "shell", `{}`)

			tool.SetResult(tt.content)

			assert.Equal(t, tt.wantState, tool.state)
			assert.Equal(t, tt.wantOutput, tool.output)
		})
	}
}

func TestToolOutputCollapses(t *testing.T) {
	t.Parallel()
	lines := make([]string, 25)
	for i := range lines {
		lines[i] = "row"
	}
	tool := NewTool(env(), "c1", "shell", `{"command":"x"}`)
	tool.SetResult(strings.Join(lines, "\n"))

	collapsed := ansi.Strip(tool.Render(80))
	require.True(t, tool.ToggleExpanded())
	expanded := ansi.Strip(tool.Render(80))

	assert.Contains(t, collapsed, "(15 lines hidden)")
	assert.Equal(t, 10, strings.Count(collapsed, "row"))
	assert.Equal(t, 25, strings.Count(expanded, "row"))
}

func TestTranscript(t *testing.T) {
	t.Parallel()
	call := &agentv1.Message{Role: agentv1.Role_ROLE_ASSISTANT, ToolCalls: []*agentv1.ToolCall{{Id: "c1", Name: "shell", Arguments: `{}`}}}
	result := &agentv1.Message{Role: agentv1.Role_ROLE_TOOL, ToolCallId: "c1", Content: `{"ok":true}`}

	tests := []struct {
		name     string
		play     func(tr *Transcript)
		wantKind []string
	}{
		{
			name: "streamed reply replaces its fragments",
			play: func(tr *Transcript) {
				tr.Pending(time.Now())
				tr.Delta(&agentv1.MessageDelta{Content: "Hel"})
				tr.Message(&agentv1.Message{Role: agentv1.Role_ROLE_ASSISTANT, Content: "Hello"})
				tr.Settle(false)
			},
			wantKind: []string{"*chat.Assistant"},
		},
		{
			name: "a tool-only message drops the placeholder",
			play: func(tr *Transcript) {
				tr.Pending(time.Now())
				tr.Message(call)
				tr.Message(result)
			},
			wantKind: []string{"*chat.Tool"},
		},
		{
			name: "an empty placeholder goes when the turn ends",
			play: func(tr *Transcript) {
				tr.User("hi")
				tr.Pending(time.Now())
				tr.Settle(true)
			},
			wantKind: []string{"*chat.User"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tr := NewTranscript(env())

			tt.play(tr)

			var kinds []string
			for _, it := range tr.Items() {
				kinds = append(kinds, typeName(it))
			}
			assert.Equal(t, tt.wantKind, kinds)
		})
	}
}

func TestCancelMarksRunningTools(t *testing.T) {
	t.Parallel()
	tr := NewTranscript(env())
	tr.Message(&agentv1.Message{Role: agentv1.Role_ROLE_ASSISTANT, ToolCalls: []*agentv1.ToolCall{{Id: "c1", Name: "shell"}}})

	tr.Settle(true)

	tool, ok := tr.Items()[0].(*Tool)
	require.True(t, ok)
	assert.Equal(t, ToolCanceled, tool.state)
	assert.Contains(t, ansi.Strip(tool.Render(80)), "Interrupted.")
}

func TestFocusBar(t *testing.T) {
	t.Parallel()
	a := NewAssistant(env(), "hello", "")

	blurred := ansi.Strip(a.Render(80))
	a.SetFocused(true)
	focused := ansi.Strip(a.Render(80))

	assert.False(t, strings.HasPrefix(blurred, styles.FocusBar))
	assert.True(t, strings.HasPrefix(focused, styles.FocusBar), "focused: %q", focused)
}

func typeName(v any) string {
	switch v.(type) {
	case *Assistant:
		return "*chat.Assistant"
	case *Tool:
		return "*chat.Tool"
	case *User:
		return "*chat.User"
	}
	return "other"
}
