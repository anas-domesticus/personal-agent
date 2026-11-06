package tui

import (
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect"
	"github.com/charmbracelet/x/ansi"

	agentv1 "github.com/anas-domesticus/personal-agent/internal/gen/agent/v1"
	"github.com/anas-domesticus/personal-agent/internal/tui/anim"
	"github.com/anas-domesticus/personal-agent/internal/tui/chat"
	"github.com/anas-domesticus/personal-agent/internal/tui/dialog"
)

var animInterval = anim.FrameInterval()

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor, m.input.Focus(), m.fetchAgent()}
	m.input.Placeholder = pick(readyPlaceholders)
	switch {
	case m.opts.ConversationID != "":
		cmds = append(cmds, m.open(m.opts.ConversationID, openedMsg{}))
	case m.opts.Continue:
		cmds = append(cmds, m.openLatest())
	}
	return tea.Batch(cmds...)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.update(msg)
	m.view.sync()
	m.layout()
	return m, tea.Batch(cmd, m.animate())
}

func (m *model) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return nil

	case tea.BackgroundColorMsg:
		m.restyle(msg.IsDark())
		return nil

	case tea.FocusMsg:
		m.termFocused = true
		return nil
	case tea.BlurMsg:
		m.termFocused = false
		return nil

	case tea.KeyPressMsg:
		return m.key(msg)

	case tea.PasteMsg:
		if m.overlay.Any() {
			return m.dialogAction(m.overlay.Update(msg))
		}
		m.setFocus(focusEditor)
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return cmd

	case tea.MouseWheelMsg:
		if m.overlay.Any() {
			return m.dialogAction(m.overlay.Update(msg))
		}
		switch msg.Button {
		case tea.MouseWheelUp:
			m.view.scroll(-3)
		case tea.MouseWheelDown:
			m.view.scroll(3)
		}
		return nil

	case tea.MouseClickMsg:
		return m.click(msg)

	case animTickMsg:
		m.animating = false
		m.view.advance()
		return nil

	case toastExpireMsg:
		if msg.seq == m.toast.seq {
			m.toast = toast{}
		}
		return nil

	case disarmMsg:
		if m.cancelArmed == msg.seq {
			m.cancelArmed = 0
		}
		return nil

	case editorMsg:
		if msg.err != nil {
			return m.notify(toastErr, "Editor: "+msg.err.Error())
		}
		m.input.SetValue(strings.TrimRight(msg.text, "\n"))
		m.input.MoveToEnd()
		m.setFocus(focusEditor)
		return nil

	case agentMsg:
		if msg.err != nil {
			m.tr.Error("Can't load agent "+m.opts.Agent, describe(msg.err))
			return nil
		}
		m.agent = msg.agent
		return nil

	case agentsMsg:
		if msg.err != nil {
			return m.notify(toastErr, "Can't list agents: "+describe(msg.err))
		}
		if m.overlay.Any() {
			return nil // something else was opened meanwhile
		}
		m.openAgents(msg.list)
		return nil

	case createdMsg:
		text := m.pending
		m.pending = ""
		if msg.err != nil {
			m.turn = nil
			m.tr.Settle(false)
			m.tr.DropLastUser(text) // it goes back in the input instead
			m.tr.Error("Can't start a conversation", describe(msg.err))
			m.restore(text)
			return nil
		}
		m.conv = msg.conv
		return m.send(text)

	case openedMsg:
		return m.opened(msg)

	case pollMsg:
		if m.conv == nil || m.conv.Id != msg.id || m.turn != nil {
			return nil
		}
		return m.open(msg.id, openedMsg{poll: true})

	case eventMsg:
		cmd := m.event(msg.ev)
		return tea.Batch(cmd, wait(m.turn.events))

	case streamErrMsg:
		return m.streamErr(msg)

	case streamEndMsg:
		if t := m.turn; t != nil && !t.done && m.reload == nil {
			// The stream closed without saying how the turn ended.
			m.tr.Settle(false)
			m.tr.Error("The stream ended before the turn did", "Showing what the server saved.")
			items := m.tr.Items()
			m.reload = &openedMsg{restore: t.text, notes: items[len(items)-1:]}
		}
		m.turn = nil
		m.cancelArmed = 0
		m.input.Placeholder = pick(readyPlaceholders)
		if r := m.reload; r != nil && m.conv != nil {
			m.reload = nil
			return m.open(m.conv.Id, *r)
		}
		return nil

	case cancelMsg:
		switch {
		case msg.err != nil:
			return m.notify(toastErr, "Can't interrupt: "+describe(msg.err))
		case !msg.cancelled:
			return m.notify(toastWarn, "Nothing to interrupt yet; try again in a moment.")
		}
		return nil

	case sessionsMsg:
		if msg.err != nil {
			return m.notify(toastErr, "Can't list conversations: "+describe(msg.err))
		}
		if m.overlay.Any() {
			return nil // something else was opened meanwhile
		}
		m.openSessions(msg.list)
		return nil
	}

	if m.focus == focusEditor && !m.overlay.Any() {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return cmd
	}
	return nil
}

// --- Turns -------------------------------------------------------------------

func (m *model) event(ev *agentv1.SendMessageResponse) tea.Cmd {
	t := m.turn
	switch e := ev.Event.(type) {
	case *agentv1.SendMessageResponse_Started:
		m.agent.Name, m.agent.Version, m.agent.Model = e.Started.Agent, e.Started.Version, e.Started.Model
		t.model = e.Started.Model
	case *agentv1.SendMessageResponse_Delta:
		m.tr.Delta(e.Delta)
	case *agentv1.SendMessageResponse_Message:
		m.tr.Message(e.Message)
		// After a tool result the model is working again.
		if e.Message.Role == agentv1.Role_ROLE_TOOL {
			m.tr.Pending(t.started)
		}
	case *agentv1.SendMessageResponse_Completed:
		done := e.Completed
		t.done = true
		m.lastTokens = [2]int64{done.PromptTokens, done.CompletionTokens}
		if done.Conversation != nil {
			m.conv = done.Conversation
		}
		var cmd tea.Cmd
		if !m.termFocused {
			cmd = tea.Raw(ansi.Notify("personal agent: the agent has replied"))
		}
		switch done.Status {
		case agentv1.TurnStatus_TURN_STATUS_COMPLETED:
			m.tr.Settle(false)
			if t.model != "" {
				m.tr.Info(t.model, time.Since(t.started))
			}
			return cmd
		case agentv1.TurnStatus_TURN_STATUS_INTERRUPTED:
			m.tr.Settle(true)
			m.tr.Notice("Interrupted.")
		default:
			m.tr.Settle(false)
			m.tr.Error("The turn failed", done.Error)
		}
		// A turn that stopped early saved only what had to be kept. Reload
		// when the stream ends, so the screen shows what the agent will see
		// next time.
		items := m.tr.Items()
		m.reload = &openedMsg{restore: t.text, notes: items[len(items)-1:]}
		return cmd
	}
	return nil
}

func (m *model) streamErr(msg streamErrMsg) tea.Cmd {
	t := m.turn
	if t.done {
		return wait(t.events)
	}
	switch connect.CodeOf(msg.err) {
	case connect.CodeCanceled:
		m.tr.Settle(true)
		m.tr.Notice("Interrupted.") // cancelled before the turn got going
	case connect.CodeFailedPrecondition:
		m.tr.Settle(false)
		m.tr.Error("The agent is still working on an earlier message", "Wait, or press esc twice to interrupt it.")
	case connect.CodeUnavailable, connect.CodeUnknown:
		m.tr.Settle(false)
		m.tr.Error("Lost the connection to the server", describe(msg.err)+". A turn that had started carries on there.")
	default:
		m.tr.Settle(false)
		m.tr.Error("The agent couldn't start", describe(msg.err))
	}
	// Whether the turn ran, and is still running, is the server's to say.
	items := m.tr.Items()
	m.reload = &openedMsg{restore: t.text, notes: items[len(items)-1:]}
	return wait(t.events)
}

func (m *model) opened(msg openedMsg) tea.Cmd {
	if msg.err != nil {
		if !msg.poll {
			m.tr.Error("Can't load the conversation", describe(msg.err))
			return nil
		}
		// A check of a turn running on the server failed. Keep trying for a
		// while, then stop waiting so the TUI isn't stuck busy.
		m.pollFails++
		if m.pollFails < pollAttempts && m.conv != nil {
			return m.pollLater(m.conv.Id)
		}
		m.serverBusy, m.pollFails = false, 0
		m.tr.Settle(false)
		m.tr.Error("Lost track of the running turn", describe(msg.err)+". Reopen the conversation (ctrl+s) to see it.")
		return nil
	}
	m.pollFails = 0
	if msg.res == nil { // nothing to open
		if msg.notice != "" {
			return m.notify(toastWarn, msg.notice)
		}
		return nil
	}
	res := msg.res
	changed := m.conv == nil || m.conv.Id != res.Conversation.Id
	m.conv = res.Conversation
	if msg.poll && !changed {
		switch {
		case res.Busy && m.serverBusy:
			// Still running: leave the screen alone and check again later.
			return m.pollLater(m.conv.Id)
		case !res.Busy && !m.serverBusy:
			return nil
		}
	}
	m.setTranscript(chat.FromMessages(m.env, res.Messages))
	if !res.Busy {
		// Nothing is running, so a call without a stored result never got
		// one.
		m.tr.Settle(true)
	}
	var prompts []string
	for _, mm := range res.Messages {
		if mm.Role == agentv1.Role_ROLE_USER {
			prompts = append(prompts, mm.Content)
		}
	}
	m.history.reset(prompts)
	for _, n := range msg.notes {
		m.tr.Add(n)
	}
	if msg.notice != "" {
		m.tr.Notice(msg.notice)
	}
	if msg.restore != "" && !lastUserIs(res.Messages, msg.restore) {
		if m.restore(msg.restore) {
			m.tr.Notice("That message wasn't kept, so it's back in the input.")
		}
	}
	if res.Conversation.Agent != "" && res.Conversation.Agent != m.agent.Name {
		m.opts.Agent = res.Conversation.Agent
		m.agent = &agentv1.Agent{Name: res.Conversation.Agent}
		return tea.Batch(m.fetchAgent(), m.watchServerTurn(res))
	}
	return m.watchServerTurn(res)
}

// watchServerTurn polls while a turn this client isn't streaming runs.
func (m *model) watchServerTurn(res *agentv1.GetConversationResponse) tea.Cmd {
	wasBusy := m.serverBusy
	m.serverBusy = res.Busy && m.turn == nil
	if !m.serverBusy {
		return nil
	}
	m.tr.Pending(time.Now())
	var cmd tea.Cmd
	if !wasBusy {
		cmd = m.notify(toastWarn, "A turn is running on the server; showing it when it finishes.")
	}
	return tea.Batch(cmd, m.pollLater(m.conv.Id))
}

func (m *model) setTranscript(tr *chat.Transcript) {
	m.tr = tr
	m.view.setTranscript(tr)
}

// restore puts text back in an empty input. It reports whether it did.
func (m *model) restore(text string) bool {
	if text == "" || strings.TrimSpace(m.input.Value()) != "" {
		return false
	}
	m.input.SetValue(text)
	return true
}

func lastUserIs(msgs []*agentv1.Message, text string) bool {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == agentv1.Role_ROLE_USER {
			return strings.TrimSpace(msgs[i].Content) == strings.TrimSpace(text)
		}
	}
	return false
}

// submit sends what's in the editor.
func (m *model) submit() tea.Cmd {
	raw := m.input.Value()
	// A trailing backslash continues on a new line instead of sending.
	if strings.HasSuffix(raw, "\\") {
		m.input.SetValue(strings.TrimSuffix(raw, "\\") + "\n")
		m.input.MoveToEnd()
		return nil
	}
	text := strings.TrimSpace(raw)
	switch text {
	case "":
		return nil
	case "exit", "quit", "/exit", "/quit":
		m.input.Reset()
		m.openQuit()
		return nil
	}
	if m.busy() {
		return m.notify(toastWarn, "The agent is busy. Wait, or press esc twice to interrupt it.")
	}
	m.input.Reset()
	m.history.add(text)
	m.input.Placeholder = pick(busyPlaceholders)
	m.tr.User(text)
	m.view.follow = true
	m.turn = &turn{text: text, started: time.Now()}
	m.tr.Pending(m.turn.started)
	if m.conv == nil {
		m.pending = text
		return m.create()
	}
	return m.send(text)
}

// --- Actions -----------------------------------------------------------------

func (m *model) newConversation() tea.Cmd {
	if m.busy() {
		return m.notify(toastWarn, "Wait for the agent to finish, or press esc twice to interrupt it.")
	}
	m.conv, m.lastTokens = nil, [2]int64{}
	m.setTranscript(chat.NewTranscript(m.env))
	m.history.reset(nil)
	m.setFocus(focusEditor)
	return m.fetchAgent()
}

func (m *model) openSessions(list []*agentv1.Conversation) {
	entries := make([]dialog.Entry, 0, len(list))
	for _, c := range list {
		title := c.Title
		if title == "" {
			title = "(untitled)"
		}
		e := dialog.Entry{ID: c.Id, Title: oneLine(title), Aliases: []string{c.Agent}}
		if c.UpdateTime != nil {
			e.Info = ago(c.UpdateTime.AsTime())
		}
		entries = append(entries, e)
	}
	cur := ""
	if m.conv != nil {
		cur = m.conv.Id
	}
	m.overlay.Open(dialog.NewPicker(m.sty, sessionsID, "Conversations", entries, cur, "No conversations yet."))
}

func (m *model) openAgents(list []*agentv1.Agent) {
	entries := make([]dialog.Entry, 0, len(list))
	for _, a := range list {
		e := dialog.Entry{ID: a.Name, Title: a.Name, Aliases: []string{a.Description}}
		if a.Version > 0 {
			e.Info = "v" + itoa(int(a.Version))
		}
		entries = append(entries, e)
	}
	m.overlay.Open(dialog.NewPicker(m.sty, agentsID, "Switch Agent", entries, m.agent.Name, "No agents."))
}

func (m *model) openQuit() {
	note := ""
	if m.busy() {
		note = "The running turn will be interrupted."
		if m.opts.Remote != "" {
			note = "The running turn carries on on the server."
		}
	}
	m.overlay.Open(dialog.NewQuit(m.sty, note))
}

func (m *model) openEditor() tea.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	f, err := os.CreateTemp("", "msg_*.md")
	if err != nil {
		return m.notify(toastErr, "Can't open the editor: "+err.Error())
	}
	path := f.Name()
	_, werr := f.WriteString(m.input.Value())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return m.notify(toastErr, "Can't open the editor: "+werr.Error())
	}
	fields := strings.Fields(editor)
	cmd := exec.Command(fields[0], append(fields[1:], path)...) //nolint:gosec,noctx // the user's own $EDITOR, run in the foreground
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return editorMsg{err: err}
		}
		b, err := os.ReadFile(path) //nolint:gosec // the file we just created
		return editorMsg{text: string(b), err: err}
	})
}

func (m *model) copyItem(it chat.Item) tea.Cmd {
	if it == nil {
		return nil
	}
	return tea.Batch(tea.SetClipboard(it.Text()), m.notify(toastOK, "Copied to clipboard."))
}

func (m *model) setFocus(f focus) {
	m.focus = f
	if f == focusChat {
		m.input.Blur()
		m.view.focus()
		return
	}
	m.view.blur()
	m.input.Focus()
}

func (m *model) dialogAction(a dialog.Action) tea.Cmd {
	switch a := a.(type) {
	case dialog.Close:
		m.overlay.CloseFront()
	case dialog.Quit:
		return tea.Quit
	case dialog.Selected:
		m.overlay.Close(a.Dialog)
		switch a.Dialog {
		case sessionsID:
			if m.busy() {
				return m.notify(toastWarn, "Wait for the agent to finish, or press esc twice to interrupt it.")
			}
			if m.conv != nil && m.conv.Id == a.ID {
				return nil
			}
			m.setFocus(focusEditor)
			return m.open(a.ID, openedMsg{})
		case agentsID:
			if m.busy() {
				return m.notify(toastWarn, "Wait for the agent to finish, or press esc twice to interrupt it.")
			}
			if a.ID == m.agent.Name && m.conv == nil {
				return nil
			}
			m.opts.Agent = a.ID
			m.agent = &agentv1.Agent{Name: a.ID}
			return tea.Batch(m.newConversation(), m.notify(toastOK, "New conversations go to "+a.ID+"."))
		case commandsID:
			return m.runCommand(a.ID)
		}
	}
	return nil
}

// --- Keys --------------------------------------------------------------------

func (m *model) key(msg tea.KeyPressMsg) tea.Cmd {
	km := m.keys
	// ctrl+c always asks before quitting, even over another dialog.
	if front := m.overlay.Front(); key.Matches(msg, km.Quit) && (front == nil || front.ID() != dialog.QuitID) {
		m.openQuit()
		return nil
	}
	if m.overlay.Any() {
		return m.dialogAction(m.overlay.Update(msg))
	}
	// esc while busy: once to arm, again to interrupt.
	if key.Matches(msg, km.Cancel) && m.busy() && (m.focus == focusEditor || m.view.selected() == nil) {
		if m.cancelArmed > 0 {
			m.cancelArmed = 0
			m.toast = toast{}
			return m.cancelTurn()
		}
		cmd := m.notify(toastWarn, "Press esc again to interrupt.")
		m.cancelArmed = m.toast.seq
		seq := m.cancelArmed
		return tea.Batch(cmd, m.tick(cancelWindow, func(time.Time) tea.Msg { return disarmMsg{seq} }))
	}

	switch {
	case key.Matches(msg, km.Help):
		m.fullHelp = !m.fullHelp
		return nil
	case key.Matches(msg, km.Commands):
		m.openCommands()
		return nil
	case key.Matches(msg, km.Agents):
		return m.listAgents()
	case key.Matches(msg, km.Sessions):
		return m.listSessions()
	case key.Matches(msg, km.New):
		return m.newConversation()
	case key.Matches(msg, km.Sidebar):
		if m.width >= wideWidth && m.height >= wideHeight {
			m.compactForce = !m.compactForce
		}
		return nil
	case key.Matches(msg, km.Details):
		if m.compact() && m.conv != nil && (m.focus == focusChat || m.input.Value() == "") {
			m.details = !m.details
			return nil
		}
	case key.Matches(msg, km.Suspend):
		if m.busy() {
			return m.notify(toastWarn, "Can't suspend while the agent is working.")
		}
		return tea.Suspend
	case key.Matches(msg, km.Tab):
		if m.focus == focusEditor {
			if m.tr.Len() > 0 {
				m.setFocus(focusChat)
			}
		} else {
			m.setFocus(focusEditor)
		}
		return nil
	}
	m.details = false

	if m.focus == focusChat {
		return m.chatKey(msg)
	}
	return m.editorKey(msg)
}

func (m *model) editorKey(msg tea.KeyPressMsg) tea.Cmd {
	km := m.keys.Editor
	switch {
	case key.Matches(msg, km.Send):
		return m.submit()
	case key.Matches(msg, km.Newline):
		m.input.InsertString("\n")
		return nil
	case key.Matches(msg, km.OpenEditor):
		return m.openEditor()
	case msg.String() == "/" && m.input.Value() == "":
		m.openCommands()
		return nil
	case key.Matches(msg, km.HistoryPrev):
		if m.input.Value() == "" || (m.input.Line() == 0 && m.input.Column() == 0) || m.history.browsing() && m.input.Line() == 0 {
			if s, ok := m.history.prev(m.input.Value()); ok {
				m.input.SetValue(s)
				m.input.MoveToBegin()
			}
			return nil
		}
		if m.input.Line() == 0 {
			m.input.CursorStart()
			return nil
		}
	case key.Matches(msg, km.HistoryNext):
		if m.history.browsing() && m.input.Line() == m.input.LineCount()-1 {
			if s, ok := m.history.next(); ok {
				m.input.SetValue(s)
				m.input.MoveToEnd()
			}
			return nil
		}
	case msg.String() == "esc" && m.history.browsing():
		m.history.leave()
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *model) chatKey(msg tea.KeyPressMsg) tea.Cmd {
	km := m.keys.Chat
	page := max(m.view.list.Height(), 1)
	switch {
	case key.Matches(msg, km.PrevItem):
		m.view.selectBy(-1)
	case key.Matches(msg, km.NextItem):
		m.view.selectBy(1)
	case key.Matches(msg, km.Up):
		m.view.scroll(-1)
	case key.Matches(msg, km.Down):
		m.view.scroll(1)
	case key.Matches(msg, km.HalfUp):
		m.view.scroll(-page / 2)
	case key.Matches(msg, km.HalfDown):
		m.view.scroll(page / 2)
	case key.Matches(msg, km.PageUp):
		m.view.scroll(-page)
	case key.Matches(msg, km.PageDown):
		m.view.scroll(page)
	case key.Matches(msg, km.Top):
		m.view.top()
	case key.Matches(msg, km.Bottom):
		m.view.bottom()
	case key.Matches(msg, km.Expand):
		if e, ok := m.view.selected().(chat.Expandable); ok && e.ToggleExpanded() {
			m.view.list.ScrollToSelected()
		}
	case key.Matches(msg, km.Copy):
		return m.copyItem(m.view.selected())
	case key.Matches(msg, km.ClearSelect):
		m.setFocus(focusEditor)
	default:
		// Typing goes to the editor.
		if msg.Text != "" {
			m.setFocus(focusEditor)
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return cmd
		}
	}
	return nil
}

// click selects the transcript item under the pointer; clicking the
// selected item expands it.
func (m *model) click(msg tea.MouseClickMsg) tea.Cmd {
	if m.overlay.Any() || msg.Button != tea.MouseLeft {
		return nil
	}
	r := m.lay.chat
	if msg.X < r.Min.X || msg.X >= r.Max.X || msg.Y < r.Min.Y || msg.Y >= r.Max.Y {
		if msg.Y >= m.lay.editor.Min.Y && msg.Y < m.lay.editor.Max.Y {
			m.setFocus(focusEditor)
		}
		return nil
	}
	i := m.view.itemAt(msg.Y - r.Min.Y)
	if i < 0 {
		return nil
	}
	if m.focus == focusChat && m.view.list.Selected() == i {
		if e, ok := m.view.selected().(chat.Expandable); ok {
			e.ToggleExpanded()
		}
		return nil
	}
	m.setFocus(focusChat)
	m.view.list.SetSelected(i)
	return nil
}
