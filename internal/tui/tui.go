// Package tui is the terminal client. It talks to the agent server only
// through the generated API client, so it works the same against a server
// started in-process and a remote one.
//
// The navigation and look follow Crush (github.com/charmbracelet/crush):
// one Bubble Tea model owns everything, components are plain structs it
// calls into, the screen is drawn region by region onto a cell buffer, and
// dialogs are drawn over it. Focus moves between the editor and the
// transcript with tab; in the transcript, items can be selected, expanded
// and copied.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect"

	"github.com/anas-domesticus/personal-agent/internal/client"
	agentv1 "github.com/anas-domesticus/personal-agent/internal/gen/agent/v1"
	"github.com/anas-domesticus/personal-agent/internal/tui/chat"
	"github.com/anas-domesticus/personal-agent/internal/tui/dialog"
	"github.com/anas-domesticus/personal-agent/internal/tui/styles"
)

// Options configures a session.
type Options struct {
	// Agent is the agent new conversations talk to; "admin" if empty.
	Agent string
	// ConversationID opens that conversation.
	ConversationID string
	// Continue opens the most recent conversation.
	Continue bool
	// Remote, if set, is the server's address: the server isn't local.
	Remote string
	// Out, if set, gets a line on exit saying how to resume.
	Out io.Writer
	// Resume is the command that reopens a conversation, for that line.
	Resume func(conversationID string) string
}

// Run runs the TUI until the user quits or ctx ends.
func Run(ctx context.Context, c *client.Client, o Options) error {
	if o.Agent == "" {
		o.Agent = "admin"
	}
	// Everything the TUI starts (calls, the stream reader) ends with it.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := newModel(ctx, c, o)
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	switch {
	case err == nil:
	case errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil:
		// stopped by a signal: a normal exit, not a failure
	default:
		return fmt.Errorf("run tui: %w", err)
	}
	if o.Out != nil && o.Resume != nil && m.conv != nil {
		fmt.Fprintf(o.Out, "Resume this conversation with: %s\n", o.Resume(m.conv.Id))
	}
	return nil
}

const (
	// pollInterval is how often a conversation busy on the server (a turn
	// this client isn't streaming) is checked.
	pollInterval = 2 * time.Second
	// pollAttempts is how many checks in a row may fail before the TUI stops
	// waiting for that turn.
	pollAttempts = 5
	// callTimeout bounds every call except the turn stream, which lasts as
	// long as the turn.
	callTimeout = 30 * time.Second
	// toastTTL is how long a status message stays.
	toastTTL = 5 * time.Second
	// cancelWindow is how long after the first esc a second one interrupts.
	cancelWindow = 2 * time.Second
	// Layout breakpoints, after Crush's: below these the sidebar gives way
	// to a one-line header.
	wideWidth, wideHeight = 120, 30
	sidebarWidth          = 32
	editorMinLines        = 1
	editorMaxLines        = 12
)

type focus int

const (
	focusEditor focus = iota
	focusChat
)

type model struct {
	ctx  context.Context
	c    *client.Client
	opts Options
	keys keyMap

	width, height int
	sty           *styles.Styles
	env           *chat.Env

	agent *agentv1.Agent        // what new turns will run, as last reported
	conv  *agentv1.Conversation // nil until the first message
	tr    *chat.Transcript
	view  *chatView

	input   textarea.Model
	history history
	focus   focus
	overlay dialog.Overlay
	help    help.Model

	turn       *turn  // the turn this client is streaming
	serverBusy bool   // a turn is running that this client isn't streaming
	pollFails  int    // checks of that turn that failed in a row
	pending    string // text to send once the conversation exists
	lastTokens [2]int64
	reload     *openedMsg // reload the conversation when the stream ends

	animating   bool // the animation clock is running
	cancelArmed int  // >0 while a second esc interrupts; the toast's seq
	toast       toast
	toastSeq    int

	fullHelp     bool
	compactForce bool // ctrl+b: no sidebar even when there is room
	details      bool // ctrl+d: the details panel in compact mode
	noMouse      bool // mouse reporting off, for the terminal's own selection
	lay          regions
	termFocused  bool

	// tick schedules a timer: tea.Tick, replaced in tests so timers don't
	// hold them up.
	tick func(time.Duration, func(time.Time) tea.Msg) tea.Cmd
}

type turn struct {
	text    string
	events  chan tea.Msg
	done    bool // the completed event arrived
	started time.Time
	model   string
}

type toastKind int

const (
	toastOK toastKind = iota
	toastWarn
	toastErr
)

type toast struct {
	kind toastKind
	text string
	seq  int
}

func newModel(ctx context.Context, c *client.Client, o Options) *model {
	sty := styles.New(true)
	env := chat.NewEnv(sty)
	tr := chat.NewTranscript(env)
	m := &model{
		ctx: ctx, c: c, opts: o,
		keys:        newKeyMap(),
		sty:         sty,
		env:         env,
		agent:       &agentv1.Agent{Name: o.Agent},
		tr:          tr,
		view:        newChatView(tr),
		help:        help.New(),
		termFocused: true,
		tick:        tea.Tick,
	}
	m.input = newEditor()
	m.restyle(true)
	return m
}

func newEditor() textarea.Model {
	in := textarea.New()
	in.ShowLineNumbers = false
	in.CharLimit = 0
	in.DynamicHeight = true
	in.MinHeight = editorMinLines
	in.MaxHeight = editorMaxLines
	in.SetVirtualCursor(false)
	in.KeyMap.InsertNewline.SetEnabled(false) // the model handles newlines
	return in
}

// readyPlaceholders and busyPlaceholders are picked from at random, as in
// Crush.
var (
	readyPlaceholders = []string{"Ready!", "Ready...", "Ready?", "Ready for instructions"}
	busyPlaceholders  = []string{"Working!", "Brrrrr...", "Thinking...", "On it..."}
)

func pick(xs []string) string { return xs[rand.IntN(len(xs))] } //nolint:gosec // a placeholder, not a secret

// --- Messages ------------------------------------------------------------

type (
	agentMsg struct {
		agent *agentv1.Agent
		err   error
	}
	agentsMsg struct {
		list []*agentv1.Agent
		err  error
	}
	createdMsg struct {
		conv *agentv1.Conversation
		err  error
	}
	// openedMsg is a conversation loaded from the server.
	openedMsg struct {
		res *agentv1.GetConversationResponse
		err error
		// notice and notes are shown after the history.
		notice string
		notes  []chat.Item
		// restore is text sent in a turn that may not have been saved; it
		// goes back in the input if it isn't in the history.
		restore string
		poll    bool // a background check; stay quiet
	}
	eventMsg     struct{ ev *agentv1.SendMessageResponse }
	streamErrMsg struct{ err error }
	streamEndMsg struct{}
	sessionsMsg  struct {
		list []*agentv1.Conversation
		err  error
	}
	pollMsg   struct{ id string }
	cancelMsg struct {
		cancelled bool
		err       error
	}
	animTickMsg    struct{}
	toastExpireMsg struct{ seq int }
	disarmMsg      struct{ seq int }
	editorMsg      struct {
		text string
		err  error
	}
)

// --- Commands --------------------------------------------------------------

func (m *model) fetchAgent() tea.Cmd {
	name := m.opts.Agent
	return func() tea.Msg {
		ctx, cancel := m.call()
		defer cancel()
		res, err := m.c.Agents.GetAgent(ctx, connect.NewRequest(&agentv1.GetAgentRequest{Name: name}))
		if err != nil {
			return agentMsg{err: err}
		}
		return agentMsg{agent: res.Msg.Agent}
	}
}

func (m *model) listAgents() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.call()
		defer cancel()
		res, err := m.c.Agents.ListAgents(ctx, connect.NewRequest(&agentv1.ListAgentsRequest{}))
		if err != nil {
			return agentsMsg{err: err}
		}
		return agentsMsg{list: res.Msg.Agents}
	}
}

func (m *model) create() tea.Cmd {
	name := m.opts.Agent
	return func() tea.Msg {
		ctx, cancel := m.call()
		defer cancel()
		res, err := m.c.Conversations.CreateConversation(ctx, connect.NewRequest(&agentv1.CreateConversationRequest{Agent: name}))
		if err != nil {
			return createdMsg{err: err}
		}
		return createdMsg{conv: res.Msg.Conversation}
	}
}

func (m *model) open(id string, o openedMsg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.call()
		defer cancel()
		res, err := m.c.Conversations.GetConversation(ctx, connect.NewRequest(&agentv1.GetConversationRequest{Id: id}))
		if err != nil {
			o.err = err
			return o
		}
		o.res = res.Msg
		return o
	}
}

func (m *model) openLatest() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.call()
		res, err := m.c.Conversations.ListConversations(ctx, connect.NewRequest(&agentv1.ListConversationsRequest{Limit: 1}))
		cancel()
		if err != nil {
			return openedMsg{err: err}
		}
		if len(res.Msg.Conversations) == 0 {
			return openedMsg{notice: "No conversations yet; starting a new one."}
		}
		return m.open(res.Msg.Conversations[0].Id, openedMsg{})()
	}
}

func (m *model) listSessions() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.call()
		defer cancel()
		res, err := m.c.Conversations.ListConversations(ctx, connect.NewRequest(&agentv1.ListConversationsRequest{Limit: 200}))
		if err != nil {
			return sessionsMsg{err: err}
		}
		return sessionsMsg{list: res.Msg.Conversations}
	}
}

func (m *model) cancelTurn() tea.Cmd {
	if m.conv == nil {
		return m.notify(toastWarn, "Nothing to interrupt yet; try again in a moment.")
	}
	id := m.conv.Id
	return func() tea.Msg {
		ctx, cancel := m.call()
		defer cancel()
		res, err := m.c.Conversations.CancelTurn(ctx, connect.NewRequest(&agentv1.CancelTurnRequest{ConversationId: id}))
		if err != nil {
			return cancelMsg{err: err}
		}
		return cancelMsg{cancelled: res.Msg.Cancelled}
	}
}

// call bounds a unary call. The context also ends when the TUI exits.
func (m *model) call() (context.Context, context.CancelFunc) {
	return context.WithTimeout(m.ctx, callTimeout)
}

// send starts a turn. Its events arrive one at a time through wait, so the
// stream is read in step with the UI.
func (m *model) send(text string) tea.Cmd {
	t := m.turn // set by submit
	t.events = make(chan tea.Msg, 64)
	id := m.conv.Id
	// The reader stops when the stream ends or the TUI exits (m.ctx), even
	// if nothing is reading events any more.
	put := func(msg tea.Msg) bool {
		select {
		case t.events <- msg:
			return true
		case <-m.ctx.Done():
			return false
		}
	}
	go func() {
		defer close(t.events)
		stream, err := m.c.Conversations.SendMessage(m.ctx, connect.NewRequest(&agentv1.SendMessageRequest{ConversationId: id, Text: text}))
		if err != nil {
			put(streamErrMsg{err})
			return
		}
		defer stream.Close()
		for stream.Receive() {
			if !put(eventMsg{stream.Msg()}) {
				return
			}
		}
		if err := stream.Err(); err != nil {
			put(streamErrMsg{err})
		}
	}()
	return wait(t.events)
}

func wait(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		if msg, ok := <-ch; ok {
			return msg
		}
		return streamEndMsg{}
	}
}

func (m *model) pollLater(id string) tea.Cmd {
	return m.tick(pollInterval, func(time.Time) tea.Msg { return pollMsg{id} })
}

// animate starts the animation clock if something is spinning and it isn't
// already running.
func (m *model) animate() tea.Cmd {
	if m.animating || !m.view.spinning() {
		return nil
	}
	m.animating = true
	return m.tick(animInterval, func(time.Time) tea.Msg { return animTickMsg{} })
}

// notify shows a status message for a while.
func (m *model) notify(kind toastKind, text string) tea.Cmd {
	m.toastSeq++
	m.toast = toast{kind: kind, text: text, seq: m.toastSeq}
	seq := m.toastSeq
	return m.tick(toastTTL, func(time.Time) tea.Msg { return toastExpireMsg{seq} })
}

func (m *model) busy() bool { return m.turn != nil || m.serverBusy }

// restyle rebuilds the styles for the terminal's background.
func (m *model) restyle(dark bool) {
	m.sty = styles.New(dark)
	m.env.Sty = m.sty
	m.tr.Restyle()
	st := m.input.Styles()
	st.Focused.Text = m.sty.Base
	st.Focused.Placeholder = m.sty.MostSubtle
	st.Focused.CursorLine = m.sty.Base
	st.Blurred.Text = m.sty.MoreSubtle
	st.Blurred.Placeholder = m.sty.MostSubtle
	st.Blurred.CursorLine = m.sty.MoreSubtle
	st.Cursor.Color = m.sty.P.Secondary
	m.input.SetStyles(st)
	m.setPrompt()
	m.help.Styles.ShortKey = m.sty.Status.Key
	m.help.Styles.ShortDesc = m.sty.Status.Desc
	m.help.Styles.ShortSeparator = m.sty.Status.Sep
	m.help.Styles.FullKey = m.sty.Status.Key
	m.help.Styles.FullDesc = m.sty.Status.Desc
	m.help.Styles.FullSeparator = m.sty.Status.Sep
	m.help.Styles.Ellipsis = m.sty.Status.Sep
}

// setPrompt draws "  > " on the first line and "::: " after, as Crush does.
func (m *model) setPrompt() {
	sty := m.sty
	m.input.SetPromptFunc(4, func(p textarea.PromptInfo) string {
		switch {
		case p.LineNumber > 0:
			return sty.Editor.Continuation.Render("::: ")
		case p.Focused:
			return sty.Editor.Prompt.Render("  > ")
		}
		return sty.Editor.PromptBlurred.Render("  > ")
	})
}

// describe turns an API error into a sentence.
func describe(err error) string {
	var ce *connect.Error
	if errors.As(err, &ce) {
		switch ce.Code() {
		case connect.CodeUnauthenticated:
			return "the server refused the token"
		case connect.CodeUnavailable:
			return "the server is unreachable (" + ce.Message() + ")"
		}
		return ce.Message()
	}
	return err.Error()
}

func count(n int64) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}
