package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/anas-domesticus/personal-agent/internal/tui/chat"
	"github.com/anas-domesticus/personal-agent/internal/tui/dialog"
)

// Dialog IDs.
const (
	commandsID = "commands"
	sessionsID = "sessions"
	agentsID   = "agents"
)

// command is an entry in the palette (ctrl+p, or / in an empty editor).
type command struct {
	id, title, keys string
	aliases         []string
	// when, if set, says whether the command applies now.
	when func(m *model) bool
}

var commands = []command{
	{id: "new", title: "New Conversation", keys: "ctrl+n", aliases: []string{"clear", "reset"}},
	{id: "sessions", title: "Conversations", keys: "ctrl+s", aliases: []string{"sessions", "history", "open"}},
	{id: "agents", title: "Switch Agent", keys: "ctrl+l", aliases: []string{"agent", "model"}},
	{id: "interrupt", title: "Interrupt Turn", keys: "esc esc", aliases: []string{"cancel", "stop"},
		when: func(m *model) bool { return m.busy() }},
	{id: "copy", title: "Copy Last Reply", aliases: []string{"yank"},
		when: func(m *model) bool { return m.lastReply() != nil }},
	{id: "editor", title: "Open External Editor", keys: "ctrl+o"},
	{id: "sidebar", title: "Toggle Sidebar", keys: "ctrl+b",
		when: func(m *model) bool { return m.width >= wideWidth && m.height >= wideHeight }},
	{id: "help", title: "Toggle Help", keys: "ctrl+g", aliases: []string{"keys"}},
	{id: "mouse", title: "Toggle Mouse Support", aliases: []string{"select"}},
	{id: "quit", title: "Quit", keys: "ctrl+c", aliases: []string{"exit"}},
}

func (m *model) openCommands() {
	var entries []dialog.Entry
	for _, c := range commands {
		if c.when != nil && !c.when(m) {
			continue
		}
		entries = append(entries, dialog.Entry{ID: c.id, Title: c.title, Info: c.keys, Aliases: c.aliases})
	}
	m.overlay.Open(dialog.NewPicker(m.sty, commandsID, "Commands", entries, "", "No matching commands."))
}

func (m *model) runCommand(id string) tea.Cmd {
	switch id {
	case "new":
		return m.newConversation()
	case "sessions":
		return m.listSessions()
	case "agents":
		return m.listAgents()
	case "interrupt":
		return m.cancelTurn()
	case "copy":
		return m.copyItem(m.lastReply())
	case "editor":
		return m.openEditor()
	case "sidebar":
		m.compactForce = !m.compactForce
	case "help":
		m.fullHelp = !m.fullHelp
	case "mouse":
		m.noMouse = !m.noMouse
		if m.noMouse {
			return m.notify(toastOK, "Mouse off: select text with the terminal. Turn it back on from the commands.")
		}
		return m.notify(toastOK, "Mouse on.")
	case "quit":
		m.openQuit()
	}
	return nil
}

// lastReply is the last assistant message with content.
func (m *model) lastReply() chat.Item {
	items := m.tr.Items()
	for i := len(items) - 1; i >= 0; i-- {
		if a, ok := items[i].(*chat.Assistant); ok && strings.TrimSpace(a.Text()) != "" {
			return a
		}
	}
	return nil
}

// ago formats a time as "just now", "5 minutes ago", "3 days ago".
func ago(t time.Time) string {
	d := time.Since(t)
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit + " ago"
		}
		return fmt.Sprintf("%d %ss ago", n, unit)
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	case d < 30*24*time.Hour:
		return plural(int(d.Hours()/24), "day")
	}
	return t.Format("2 Jan 2006")
}

func itoa(n int) string { return strconv.Itoa(n) }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
