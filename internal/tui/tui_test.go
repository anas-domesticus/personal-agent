package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/agent"
	"github.com/anas-domesticus/personal-agent/internal/client"
	"github.com/anas-domesticus/personal-agent/internal/domain"
	agentv1 "github.com/anas-domesticus/personal-agent/internal/gen/agent/v1"
	"github.com/anas-domesticus/personal-agent/internal/server"
	"github.com/anas-domesticus/personal-agent/internal/tui/chat"
	"github.com/anas-domesticus/personal-agent/internal/tui/dialog"
)

// fakeChat stands in for chat.Service behind the real API server. It's a
// fake, not a mock: conversations are stateful (later calls read what earlier
// turns saved) and turns are scripted playback through Observer callbacks.
type fakeChat struct {
	mu      sync.Mutex
	convs   map[uuid.UUID]*domain.Conversation
	history map[uuid.UUID][]*schema.Message
	// turn plays a turn; it returns what to save (nil saves nothing).
	turn     func(text string, obs agent.Observer) ([]*schema.Message, error)
	cancels  atomic.Int32
	notIdle  bool // Cancel reports that a turn was running
	creation time.Time
}

var (
	_ server.Chat   = (*fakeChat)(nil)
	_ server.Agents = fakeAgents{}
)

func newFakeChat() *fakeChat {
	return &fakeChat{convs: map[uuid.UUID]*domain.Conversation{}, history: map[uuid.UUID][]*schema.Message{}, notIdle: true}
}

func (f *fakeChat) Start(_ context.Context, name string) (domain.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Later conversations are newer, so lists sort predictably.
	f.creation = f.creation.Add(time.Minute)
	c := &domain.Conversation{ID: uuid.New(), Agent: name, CreatedAt: f.creation, UpdatedAt: f.creation}
	f.convs[c.ID] = c
	return *c, nil
}

func (f *fakeChat) Get(_ context.Context, id uuid.UUID) (domain.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.convs[id]
	if !ok {
		return domain.Conversation{}, domain.ErrNotFound
	}
	return *c, nil
}

func (f *fakeChat) List(context.Context, int32) ([]domain.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Conversation
	for _, c := range f.convs {
		out = append(out, *c)
	}
	return out, nil
}

func (f *fakeChat) History(_ context.Context, id uuid.UUID) ([]*schema.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.history[id], nil
}

func (f *fakeChat) Send(_ context.Context, id uuid.UUID, text string, obs agent.Observer) (agent.Turn, error) {
	obs.Started(agent.Info{Name: "admin", Version: 2, Model: "fake/model"})
	saved, err := f.turn(text, obs)
	f.mu.Lock()
	defer f.mu.Unlock()
	if saved != nil {
		f.history[id] = append(f.history[id], append([]*schema.Message{schema.UserMessage(text)}, saved...)...)
		if c := f.convs[id]; c.Title == "" {
			c.Title = text
		}
	}
	return agent.Turn{Messages: saved, PromptTokens: 1200, CompletionTokens: 34}, err
}

func (f *fakeChat) Busy(uuid.UUID) bool { return false }

func (f *fakeChat) Cancel(uuid.UUID) bool {
	f.cancels.Add(1)
	return f.notIdle
}

type fakeAgents struct{}

func (fakeAgents) Get(_ context.Context, name string) (domain.AgentDetail, error) {
	return domain.AgentDetail{
		Agent:   domain.Agent{Name: name, Description: "The " + name + " agent.", Admin: name == "admin"},
		Current: &domain.AgentVersion{Version: 2, Model: "fake/model"},
	}, nil
}

func (fakeAgents) List(context.Context) ([]domain.Agent, error) {
	v := int32(2)
	return []domain.Agent{
		{Name: "admin", Description: "Builds agents.", Admin: true, CurrentVersion: &v},
		{Name: "researcher", Description: "Reads the web.", CurrentVersion: &v},
	}, nil
}

func setupSized(t *testing.T, fc *fakeChat, w, h int) *model {
	t.Helper()
	srv := httptest.NewServer(server.New(server.Deps{Chat: fc, Agents: fakeAgents{}, Token: "t"}))
	t.Cleanup(srv.Close)
	m := newModel(context.Background(), client.New(srv.URL, "t"), Options{Agent: "admin"})
	m.tick = func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	run(t, m, m.fetchAgent())
	return m
}

func setup(t *testing.T, fc *fakeChat) *model {
	t.Helper()
	return setupSized(t, fc, 100, 40)
}

// run executes cmd and every command that follows from it, feeding the
// messages to the model, until nothing is left.
func run(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 1000 {
			t.Fatal("commands did not settle")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
			continue
		case nil:
			continue
		}
		_, next := m.Update(msg)
		queue = append(queue, next)
	}
}

// press sends keys, running whatever they start.
func press(t *testing.T, m *model, keys ...tea.KeyPressMsg) {
	t.Helper()
	for _, k := range keys {
		_, cmd := m.Update(k)
		run(t, m, cmd)
	}
}

func typeText(t *testing.T, m *model, s string) {
	t.Helper()
	for _, r := range s {
		press(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

var (
	enter     = tea.KeyPressMsg{Code: tea.KeyEnter}
	esc       = tea.KeyPressMsg{Code: tea.KeyEscape}
	tab       = tea.KeyPressMsg{Code: tea.KeyTab}
	space     = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	shiftUp   = tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}
	up        = tea.KeyPressMsg{Code: tea.KeyUp}
	down      = tea.KeyPressMsg{Code: tea.KeyDown}
	ctrl      = func(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }
	plainChar = func(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }
)

func typeAndSend(t *testing.T, m *model, text string) {
	t.Helper()
	m.input.SetValue(text)
	press(t, m, enter)
}

func screen(m *model) string { return ansi.Strip(m.View().Content) }

// toolTurn runs a shell call with the given output, then answers.
func toolTurn(output string) func(string, agent.Observer) ([]*schema.Message, error) {
	return func(_ string, obs agent.Observer) ([]*schema.Message, error) {
		call := schema.AssistantMessage("", []schema.ToolCall{{ID: "c1", Function: schema.FunctionCall{Name: "shell", Arguments: `{"command":"ls -la"}`}}})
		result := schema.ToolMessage(output, "c1", schema.WithToolName("shell"))
		obs.Message(call)
		obs.Message(result)
		obs.Delta(&schema.Message{Content: "Two "})
		obs.Delta(&schema.Message{Content: "files."})
		answer := schema.AssistantMessage("Two **files**.", nil)
		obs.Message(answer)
		return []*schema.Message{call, result, answer}, nil
	}
}

func TestStreamedTurn(t *testing.T) {
	t.Parallel()
	fc := newFakeChat()
	fc.turn = toolTurn(`{"ok":true,"data":"a.txt\nb.txt"}`)
	m := setup(t, fc)
	assert.Contains(t, screen(m), "The admin agent.", "landing screen")

	typeAndSend(t, m, "what's here?")

	assert.False(t, m.busy(), "busy after the turn")
	require.NotNil(t, m.conv)
	assert.Equal(t, "what's here?", m.conv.Title)
	out := screen(m)
	for _, want := range []string{"what's here?", "✓ Shell ls -la", "a.txt", "Two files.", "◇ fake/model in", "↑1.2k ↓34"} {
		assert.Contains(t, out, want)
	}
	assert.Empty(t, m.input.Value(), "input after sending")
}

func TestToolFailureIsShown(t *testing.T) {
	t.Parallel()
	fc := newFakeChat()
	fc.turn = toolTurn(`{"ok":false,"error":"permission denied"}`)
	m := setup(t, fc)

	typeAndSend(t, m, "list")

	out := screen(m)
	assert.Contains(t, out, "× Shell ls -la")
	assert.Contains(t, out, "ERROR")
	assert.Contains(t, out, "permission denied")
}

func TestInterruptedTurnRestoresInput(t *testing.T) {
	t.Parallel()
	fc := newFakeChat()
	fc.turn = func(_ string, obs agent.Observer) ([]*schema.Message, error) {
		obs.Delta(&schema.Message{Content: "Let me th"})
		return nil, context.Canceled // nothing saved, as when no tool ran
	}
	m := setup(t, fc)

	typeAndSend(t, m, "write me a poem")

	out := screen(m)
	assert.Contains(t, out, "Interrupted.")
	assert.Contains(t, out, "back in the input")
	assert.NotContains(t, out, "Let me th", "an unsaved partial reply")
	assert.Equal(t, "write me a poem", m.input.Value())
}

func TestConversationsAndCommands(t *testing.T) {
	t.Parallel()
	fc := newFakeChat()
	fc.turn = func(string, agent.Observer) ([]*schema.Message, error) {
		return []*schema.Message{schema.AssistantMessage("ok", nil)}, nil
	}
	m := setup(t, fc)
	typeAndSend(t, m, "first chat")
	first := m.conv.Id

	// The palette opens on / in an empty editor; "clear" is an alias of New.
	press(t, m, plainChar('/'))
	require.True(t, m.overlay.Has(commandsID))
	typeText(t, m, "clear")
	press(t, m, enter)
	assert.False(t, m.overlay.Any(), "palette after choosing")
	assert.Nil(t, m.conv, "New Conversation kept the conversation")
	assert.Zero(t, m.tr.Len())

	typeAndSend(t, m, "second chat")
	require.NotNil(t, m.conv)
	assert.NotEqual(t, first, m.conv.Id, "second chat reused the first conversation")

	press(t, m, ctrl('s'))
	require.True(t, m.overlay.Has(sessionsID))
	picker, ok := m.overlay.Front().(*dialog.Picker)
	require.True(t, ok)
	assert.Equal(t, 2, picker.Len())
	typeText(t, m, "first")
	assert.Equal(t, 1, picker.Len(), "after filtering")
	press(t, m, enter)
	assert.False(t, m.overlay.Any(), "picker after choosing")
	assert.Equal(t, first, m.conv.Id)
	assert.Contains(t, screen(m), "first chat", "history of the chosen conversation")

	// The history of the opened conversation is browsable.
	press(t, m, up)
	assert.Equal(t, "first chat", m.input.Value())
}

func TestSwitchAgent(t *testing.T) {
	t.Parallel()
	m := setup(t, newFakeChat())

	press(t, m, ctrl('l'))
	require.True(t, m.overlay.Has(agentsID))
	typeText(t, m, "web") // matches the description
	press(t, m, enter)

	assert.Equal(t, "researcher", m.opts.Agent)
	assert.Equal(t, "researcher", m.agent.Name)
	assert.Contains(t, screen(m), "The researcher agent.")
}

func TestChatNavigation(t *testing.T) {
	t.Parallel()
	long := make([]string, 30)
	for i := range long {
		long[i] = fmt.Sprintf("line %02d", i)
	}
	out, err := json.Marshal(strings.Join(long, "\n"))
	require.NoError(t, err)
	fc := newFakeChat()
	fc.turn = toolTurn(`{"ok":true,"data":` + string(out) + `}`)
	m := setupSized(t, fc, 100, 80)
	typeAndSend(t, m, "go")

	press(t, m, tab)
	require.Equal(t, focusChat, m.focus)
	assert.IsType(t, (*chat.Info)(nil), m.view.selected(), "the last item is selected")

	// Up to the tool call: assistant reply, then the tool.
	press(t, m, shiftUp, shiftUp)
	tool, ok := m.view.selected().(*chat.Tool)
	require.True(t, ok, "selected %T", m.view.selected())
	assert.Contains(t, screen(m), "lines hidden")
	assert.NotContains(t, screen(m), "line 29")

	press(t, m, space)
	assert.Contains(t, screen(m), "line 29", "expanded")
	assert.NotContains(t, screen(m), "lines hidden")

	press(t, m, plainChar('c'))
	assert.Contains(t, screen(m), "Copied to clipboard.")
	assert.Contains(t, tool.Text(), "line 29")

	// Typing goes back to the editor.
	press(t, m, plainChar('h'))
	assert.Equal(t, focusEditor, m.focus)
	assert.Equal(t, "h", m.input.Value())
}

func TestEscTwiceInterrupts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		running     bool // Cancel finds a running turn
		keys        []tea.KeyPressMsg
		wantCancels int32
		want        string
	}{
		{name: "one press only arms", running: true, keys: []tea.KeyPressMsg{esc}, want: "Press esc again to interrupt."},
		{name: "two presses interrupt", running: true, keys: []tea.KeyPressMsg{esc, esc}, wantCancels: 1},
		{name: "nothing was running", keys: []tea.KeyPressMsg{esc, esc}, wantCancels: 1, want: "Nothing to interrupt yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeChat()
			fc.notIdle = tt.running
			m := setup(t, fc)
			m.conv = &agentv1.Conversation{Id: uuid.NewString()}
			m.serverBusy = true

			press(t, m, tt.keys...)

			assert.Equal(t, tt.wantCancels, fc.cancels.Load())
			if tt.want != "" {
				assert.Contains(t, screen(m), tt.want)
			}
		})
	}
}

func TestQuitAsksFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		answer   tea.KeyPressMsg
		wantQuit bool
	}{
		{name: "yes", answer: plainChar('y'), wantQuit: true},
		{name: "no", answer: plainChar('n')},
		{name: "esc", answer: esc},
		{name: "ctrl+c again", answer: ctrl('c'), wantQuit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := setup(t, newFakeChat())

			press(t, m, ctrl('c'))
			require.Contains(t, screen(m), "Are you sure you want to quit?")
			_, cmd := m.Update(tt.answer)

			quit := cmd != nil && isQuit(cmd())
			assert.Equal(t, tt.wantQuit, quit)
			if !tt.wantQuit {
				assert.False(t, m.overlay.Any(), "dialog after answering no")
			}
		})
	}
}

func isQuit(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if c != nil && isQuit(c()) {
				return true
			}
		}
	}
	return false
}

func TestLayout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		width, height int
		keys          []tea.KeyPressMsg
		want, notWant string
	}{
		{name: "wide has a sidebar", width: 130, height: 40, want: "Session ─", notWant: "ctrl+d open"},
		{name: "narrow has a header", width: 100, height: 40, want: "ctrl+d open", notWant: "Session ─"},
		{name: "ctrl+b hides the sidebar", width: 130, height: 40, keys: []tea.KeyPressMsg{ctrl('b')}, want: "ctrl+d open", notWant: "Session ─"},
		{name: "ctrl+d opens details", width: 100, height: 40, keys: []tea.KeyPressMsg{ctrl('d')}, want: "Session ─"},
		{name: "ctrl+g shows full help", width: 130, height: 40, keys: []tea.KeyPressMsg{ctrl('g')}, want: "open editor"},
		{name: "full help follows focus", width: 130, height: 40, keys: []tea.KeyPressMsg{tab, ctrl('g')}, want: "half page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fc := newFakeChat()
			fc.turn = toolTurn(`{"ok":true}`)
			m := setupSized(t, fc, tt.width, tt.height)
			typeAndSend(t, m, "hello")

			press(t, m, tt.keys...)

			out := screen(m)
			assert.Contains(t, out, tt.want)
			if tt.notWant != "" {
				assert.NotContains(t, out, tt.notWant)
			}
		})
	}
}

func TestTurnRunningElsewhere(t *testing.T) {
	t.Parallel()
	errDown := errors.New("connection refused")
	conv := &agentv1.Conversation{Id: uuid.NewString(), Title: "t"}

	tests := []struct {
		name         string
		pollFailures int
		wantBusy     bool
		wantError    bool
	}{
		{name: "a few failed checks keep waiting", pollFailures: pollAttempts - 1, wantBusy: true},
		{name: "too many failed checks stop waiting", pollFailures: pollAttempts, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := setup(t, newFakeChat())
			m.Update(openedMsg{res: &agentv1.GetConversationResponse{Conversation: conv, Busy: true}})
			require.True(t, m.busy(), "a turn running on the server shows as busy")

			for range tt.pollFailures {
				m.Update(openedMsg{poll: true, err: errDown})
			}

			assert.Equal(t, tt.wantBusy, m.busy())
			if tt.wantError {
				assert.Contains(t, screen(m), "Lost track of the running turn")
			}
		})
	}
}

func TestInterruptFeedback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  tea.Msg
		want string
	}{
		{name: "nothing was running", msg: cancelMsg{cancelled: false}, want: "Nothing to interrupt yet"},
		{name: "the call failed", msg: cancelMsg{err: errors.New("boom")}, want: "Can't interrupt: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := setup(t, newFakeChat())

			m.Update(tt.msg)

			assert.Contains(t, screen(m), tt.want)
		})
	}
}

func TestEditorKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		keys []tea.KeyPressMsg
		want string
	}{
		{name: "trailing backslash is a newline", text: `one\`, keys: []tea.KeyPressMsg{enter}, want: "one\n"},
		{name: "shift+enter is a newline", text: "one", keys: []tea.KeyPressMsg{{Code: tea.KeyEnter, Mod: tea.ModShift}}, want: "one\n"},
		{name: "down past the newest entry restores the draft", text: "draft", keys: []tea.KeyPressMsg{ctrl('a'), up, down}, want: "draft"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := setup(t, newFakeChat())
			m.history.reset([]string{"old"})
			m.input.SetValue(tt.text)

			press(t, m, tt.keys...)

			assert.Equal(t, tt.want, m.input.Value())
		})
	}
}

func TestBusyPollLeavesTheScreenAlone(t *testing.T) {
	t.Parallel()
	conv := &agentv1.Conversation{Id: uuid.NewString(), Title: "t"}
	busy := &agentv1.GetConversationResponse{Conversation: conv, Busy: true, Messages: []*agentv1.Message{
		{Role: agentv1.Role_ROLE_USER, Content: "go"},
		{Role: agentv1.Role_ROLE_ASSISTANT, ToolCalls: []*agentv1.ToolCall{{Id: "c1", Name: "shell"}}},
	}}
	m := setup(t, newFakeChat())
	m.Update(openedMsg{res: busy})
	before := append([]chat.Item(nil), m.tr.Items()...)
	assert.True(t, m.view.spinning(), "the server's tool call runs")

	m.Update(openedMsg{poll: true, res: busy})
	m.Update(openedMsg{poll: true, res: busy})

	assert.Equal(t, before, m.tr.Items(), "items rebuilt while the turn runs")
	assert.True(t, m.busy())

	done := &agentv1.GetConversationResponse{Conversation: conv, Messages: append(busy.Messages,
		&agentv1.Message{Role: agentv1.Role_ROLE_TOOL, ToolCallId: "c1", Content: `{"ok":true}`},
		&agentv1.Message{Role: agentv1.Role_ROLE_ASSISTANT, Content: "Finished."})}
	m.Update(openedMsg{poll: true, res: done})

	assert.False(t, m.busy())
	assert.False(t, m.view.spinning())
	assert.Contains(t, screen(m), "Finished.")
}

func TestUnansweredToolCallDoesNotSpin(t *testing.T) {
	t.Parallel()
	m := setup(t, newFakeChat())

	m.Update(openedMsg{res: &agentv1.GetConversationResponse{
		Conversation: &agentv1.Conversation{Id: uuid.NewString()},
		Messages: []*agentv1.Message{
			{Role: agentv1.Role_ROLE_USER, Content: "go"},
			{Role: agentv1.Role_ROLE_ASSISTANT, ToolCalls: []*agentv1.ToolCall{{Id: "c1", Name: "shell"}}},
		},
	}})

	assert.False(t, m.view.spinning())
	assert.Contains(t, screen(m), "Interrupted.")
}

func TestStreamEndingEarly(t *testing.T) {
	t.Parallel()
	fc := newFakeChat()
	m := setup(t, fc)
	conv, err := fc.Start(context.Background(), "admin")
	require.NoError(t, err)
	m.conv = &agentv1.Conversation{Id: conv.ID.String()}
	m.turn = &turn{text: "hello", started: time.Now(), events: make(chan tea.Msg)}
	m.tr.User("hello")
	m.tr.Pending(m.turn.started)

	_, cmd := m.Update(streamEndMsg{})
	run(t, m, cmd)

	assert.False(t, m.busy())
	assert.False(t, m.view.spinning())
	assert.Contains(t, screen(m), "The stream ended before the turn did")
	assert.Equal(t, "hello", m.input.Value(), "the unsaved message is back in the input")
}

func TestFailedCreate(t *testing.T) {
	t.Parallel()
	m := setup(t, newFakeChat())
	m.input.SetValue("hello")
	m.Update(enter) // the create call isn't run
	require.True(t, m.busy())

	m.Update(createdMsg{err: errors.New("database down")})

	assert.False(t, m.busy())
	assert.Equal(t, "hello", m.input.Value())
	assert.Equal(t, 1, strings.Count(screen(m), "hello"), "the message shows only in the input")
	assert.Contains(t, screen(m), "Can't start a conversation")
}

func TestClicks(t *testing.T) {
	t.Parallel()
	long := make([]string, 30)
	for i := range long {
		long[i] = fmt.Sprintf("line %02d", i)
	}
	out, err := json.Marshal(strings.Join(long, "\n"))
	require.NoError(t, err)
	fc := newFakeChat()
	fc.turn = toolTurn(`{"ok":true,"data":` + string(out) + `}`)
	m := setupSized(t, fc, 100, 80)
	typeAndSend(t, m, "go")

	// Find the row of the tool's header.
	row := -1
	for i, l := range strings.Split(screen(m), "\n") {
		if strings.Contains(l, "Shell ls -la") {
			row = i
		}
	}
	require.GreaterOrEqual(t, row, 0)
	click := tea.MouseClickMsg{X: 5, Y: row, Button: tea.MouseLeft}

	m.Update(click)
	require.Equal(t, focusChat, m.focus)
	_, ok := m.view.selected().(*chat.Tool)
	require.True(t, ok, "selected %T", m.view.selected())
	assert.NotContains(t, screen(m), "line 29")

	m.Update(click)
	assert.Contains(t, screen(m), "line 29", "a second click expands")

	m.Update(tea.MouseClickMsg{X: 5, Y: m.lay.editor.Min.Y + 1, Button: tea.MouseLeft})
	assert.Equal(t, focusEditor, m.focus)
}

func TestEscWithASelectionWhileBusy(t *testing.T) {
	t.Parallel()
	fc := newFakeChat()
	fc.turn = toolTurn(`{"ok":true}`)
	m := setup(t, fc)
	typeAndSend(t, m, "go")
	press(t, m, tab)
	m.serverBusy = true

	press(t, m, esc)
	assert.Equal(t, focusEditor, m.focus, "the first esc leaves the transcript")
	assert.Zero(t, fc.cancels.Load())

	press(t, m, esc, esc)
	assert.Equal(t, int32(1), fc.cancels.Load())
}

func TestLateListDoesNotCoverADialog(t *testing.T) {
	t.Parallel()
	m := setup(t, newFakeChat())
	press(t, m, ctrl('c'))

	m.Update(sessionsMsg{list: []*agentv1.Conversation{{Id: "x", Title: "late"}}})

	front := m.overlay.Front()
	require.NotNil(t, front)
	assert.Equal(t, dialog.QuitID, front.ID())
}
