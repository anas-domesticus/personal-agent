package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/anas-domesticus/personal-agent/internal/tui/chat"
	"github.com/anas-domesticus/personal-agent/internal/tui/styles"
)

// regions is where each part of the screen goes, worked out after every
// update.
type regions struct {
	header, chat, editor, status, sidebar, details uv.Rectangle
	landing, wide                                  bool
}

// compact reports whether there is no room (or no wish) for the sidebar.
func (m *model) compact() bool {
	return m.width < wideWidth || m.height < wideHeight || m.compactForce
}

// landing reports whether nothing has been said yet.
func (m *model) landing() bool { return m.conv == nil && m.tr.Len() == 0 }

func (m *model) layout() {
	if m.width == 0 {
		return
	}
	// A one-cell margin all round, as in Crush.
	app := uv.Rect(1, 1, max(m.width-2, 1), max(m.height-2, 1))
	r := regions{landing: m.landing()}
	r.wide = !r.landing && !m.compact()

	main := app
	if r.wide {
		r.sidebar = uv.Rect(app.Max.X-sidebarWidth, app.Min.Y, sidebarWidth, app.Dy())
		main.Max.X = r.sidebar.Min.X - 2
	}

	helpH := 1
	if m.fullHelp {
		helpH = lipgloss.Height(m.help.FullHelpView(m.fullHelpKeys()))
	}
	r.status = uv.Rect(main.Min.X, main.Max.Y-helpH, main.Dx(), helpH)

	m.input.SetWidth(main.Dx())
	editorH := m.input.Height() + 1 // a blank row above
	r.editor = uv.Rect(main.Min.X, r.status.Min.Y-editorH-1, main.Dx(), editorH)

	top := main.Min.Y
	switch {
	case r.landing:
		h := lipgloss.Height(m.landingView(main.Dx()))
		r.header = uv.Rect(main.Min.X, top, main.Dx(), h)
		top += h
	case !r.wide:
		r.header = uv.Rect(main.Min.X, top, main.Dx(), 1)
		top += 2
		if m.details {
			w := min(main.Dx(), 60)
			r.details = uv.Rect(main.Min.X, top, w, min(lipgloss.Height(m.sidebarView(w-4, false))+2, main.Dy()-top))
		}
	}
	r.chat = uv.Rect(main.Min.X, top, main.Dx(), max(r.editor.Min.Y-top, 0))
	m.lay = r
	m.view.setSize(r.chat.Dx(), r.chat.Dy())
}

// View implements tea.Model.
func (m *model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	v.ReportFocus = true
	v.MouseMode = tea.MouseModeCellMotion
	if m.noMouse {
		v.MouseMode = tea.MouseModeNone
	}
	v.BackgroundColor = m.sty.P.BgBase
	v.WindowTitle = "agent"
	if m.conv != nil && m.conv.Title != "" {
		v.WindowTitle = "agent · " + m.conv.Title
	}
	if m.busy() {
		v.ProgressBar = tea.NewProgressBar(tea.ProgressBarIndeterminate, 0)
	}
	if m.width == 0 {
		return v
	}

	scr := uv.NewScreenBuffer(m.width, m.height)
	v.Cursor = m.draw(scr)
	content := strings.ReplaceAll(scr.Render(), "\r\n", "\n")
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	v.SetContent(strings.Join(lines, "\n"))
	return v
}

func (m *model) draw(scr uv.ScreenBuffer) *tea.Cursor {
	r := m.lay
	put := func(s string, area uv.Rectangle) {
		if area.Dx() > 0 && area.Dy() > 0 {
			uv.NewStyledString(s).Draw(scr, area)
		}
	}
	switch {
	case r.landing:
		put(m.landingView(r.header.Dx()), r.header)
	case r.wide:
		put(m.sidebarView(r.sidebar.Dx(), true), r.sidebar)
	default:
		put(m.headerView(r.header.Dx()), r.header)
	}
	put(m.view.render(), r.chat)

	editor := m.input.View()
	put(editor, uv.Rect(r.editor.Min.X, r.editor.Min.Y+1, r.editor.Dx(), r.editor.Dy()-1))
	put(m.statusView(r.status.Dx()), r.status)

	if m.details && !r.details.Empty() {
		box := m.sty.Dialog.Frame.Width(r.details.Dx()).Render(m.sidebarView(r.details.Dx()-4, false))
		put(box, r.details)
	}

	var cur *tea.Cursor
	if m.focus == focusEditor && !m.overlay.Any() {
		if c := m.input.Cursor(); c != nil {
			c.X += r.editor.Min.X
			c.Y += r.editor.Min.Y + 1
			cur = c
		}
	}
	if m.overlay.Any() {
		cur = m.overlay.Draw(scr, scr.Bounds())
	}
	return cur
}

// --- Regions -------------------------------------------------------------------

// logoMaxWidth keeps the landing logo from running the full width of a
// wide terminal.
const logoMaxWidth = 64

// logo is three rows of diagonals with the wordmark set into the middle.
func (m *model) logo(w int) string {
	sty := m.sty
	w = min(w, logoMaxWidth)
	mark := sty.Wordmark()
	lead := 3
	rest := max(w-lead-lipgloss.Width(mark)-2, 0)
	return strings.Join([]string{
		sty.Diagonals(w),
		sty.Diagonals(lead) + " " + mark + " " + sty.Diagonals(rest),
		sty.Diagonals(w),
	}, "\n")
}

func (m *model) landingView(w int) string {
	sty := m.sty
	a := m.agent
	lines := []string{m.logo(w), ""}
	who := sty.Header.Name.Render(styles.IconAgent + " " + a.Name)
	if a.Version > 0 {
		who += sty.MoreSubtle.Render(fmt.Sprintf(" v%d", a.Version))
	}
	lines = append(lines, who)
	if a.Description != "" {
		lines = append(lines, sty.Subtle.Render(lipgloss.Wrap(a.Description, min(w, 100), "")))
	}
	if a.Model != "" {
		lines = append(lines, sty.MoreSubtle.Render(styles.IconModel+" "+a.Model))
	}
	lines = append(lines, sty.MostSubtle.Render(m.serverLabel()), "",
		sty.MoreSubtle.Render("Type a message to start. ")+sty.MostSubtle.Render("ctrl+p commands · ctrl+s conversations · ctrl+l agents"))
	return strings.Join(lines, "\n")
}

func (m *model) serverLabel() string {
	if m.opts.Remote != "" {
		return "Server: " + m.opts.Remote
	}
	return "Server: local"
}

// headerView is the one-line header of the compact layout.
func (m *model) headerView(w int) string {
	sty := m.sty
	mark := sty.Wordmark()
	var details []string
	if m.conv != nil && m.conv.Title != "" {
		details = append(details, sty.Subtle.Render(oneLine(m.conv.Title)))
	}
	if m.agent.Model != "" {
		details = append(details, sty.MoreSubtle.Render(styles.IconModel+" "+m.agent.Model))
	}
	if t := m.lastTokens; t[0]+t[1] > 0 {
		details = append(details, sty.MoreSubtle.Render(fmt.Sprintf("↑%s ↓%s", count(t[0]), count(t[1]))))
	}
	if m.conv != nil {
		hint := "open"
		if m.details {
			hint = "close"
		}
		details = append(details, sty.MostSubtle.Render("ctrl+d "+hint))
	}
	right := strings.Join(details, sty.Header.Separator.Render(styles.Separator))
	avail := w - lipgloss.Width(mark) - 2
	right = ansi.Truncate(right, max(avail-3, 0), styles.EllipsisGlyph)
	fill := max(avail-lipgloss.Width(right)-1, 3)
	return ansi.Truncate(mark+" "+sty.Diagonals(fill)+" "+right, w, "")
}

// sidebarView is the agent, model and conversation, for the wide layout
// and the compact layout's details panel.
func (m *model) sidebarView(w int, logo bool) string {
	sty := m.sty
	a := m.agent
	var lines []string
	if logo {
		lines = append(lines, m.logo(w), "")
	}
	title := "New conversation"
	if m.conv != nil && m.conv.Title != "" {
		title = oneLine(m.conv.Title)
	}
	lines = append(lines, sty.MoreSubtle.Render(clampLines(lipgloss.Wrap(title, w, ""), 2)), "")

	lines = append(lines, sty.SectionTitle("Agent", w))
	who := sty.Header.Name.Render(styles.IconAgent + " " + a.Name)
	if a.Version > 0 {
		who += sty.MoreSubtle.Render(fmt.Sprintf(" v%d", a.Version))
	}
	lines = append(lines, who)
	if a.Model != "" {
		lines = append(lines, sty.Subtle.Render(lipgloss.Wrap(styles.IconModel+" "+a.Model, w, "")))
	}
	if a.Description != "" {
		lines = append(lines, sty.MostSubtle.Render(clampLines(lipgloss.Wrap(a.Description, w, ""), 3)))
	}

	lines = append(lines, "", sty.SectionTitle("Session", w))
	if t := m.lastTokens; t[0]+t[1] > 0 {
		lines = append(lines, sty.Subtle.Render(fmt.Sprintf("↑%s ↓%s", count(t[0]), count(t[1])))+sty.MostSubtle.Render(" last turn"))
	}
	if m.busy() {
		since := time.Now()
		if m.turn != nil {
			since = m.turn.started
		}
		lines = append(lines, sty.Tool.Pending.Render(styles.IconPending)+sty.Subtle.Render(" working ")+sty.MostSubtle.Render(chat.Elapsed(time.Since(since))))
	}
	lines = append(lines, sty.MostSubtle.Render(ansi.Truncate(m.serverLabel(), w, styles.EllipsisGlyph)))
	if m.conv != nil {
		lines = append(lines, sty.MostSubtle.Render(ansi.Truncate("ID: "+m.conv.Id, w, styles.EllipsisGlyph)))
	}
	return strings.Join(lines, "\n")
}

func clampLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	lines = lines[:n]
	lines[n-1] = strings.TrimRight(lines[n-1], " ") + styles.EllipsisGlyph
	return strings.Join(lines, "\n")
}

// statusView is the help line, or a status message over it.
func (m *model) statusView(w int) string {
	if m.toast.text != "" {
		st := m.sty.Status
		tag, band := st.OKTag.Render("OKAY!"), st.OKBand
		switch m.toast.kind {
		case toastWarn:
			tag, band = st.WarnTag.Render("HEY!"), st.WarnBand
		case toastErr:
			tag, band = st.ErrTag.Render("ERROR"), st.ErrBand
		case toastOK:
		}
		rest := max(w-lipgloss.Width(tag), 0)
		text := ansi.Truncate(m.toast.text, max(rest-band.GetHorizontalFrameSize(), 0), styles.EllipsisGlyph)
		return tag + band.Width(rest).Render(text)
	}
	m.help.SetWidth(w)
	if m.fullHelp {
		return m.help.FullHelpView(m.fullHelpKeys())
	}
	return m.help.ShortHelpView(m.shortHelpKeys())
}

// --- Help ------------------------------------------------------------------------

func withHelp(b key.Binding, desc string) key.Binding {
	b.SetHelp(b.Help().Key, desc)
	return b
}

func (m *model) shortHelpKeys() []key.Binding {
	km := m.keys
	var out []key.Binding
	if m.busy() {
		desc := "interrupt"
		if m.cancelArmed > 0 {
			desc = "press again to interrupt"
		}
		out = append(out, withHelp(km.Cancel, desc))
	}
	if m.focus == focusChat {
		c := km.Chat
		out = append(out,
			key.NewBinding(key.WithKeys("up"), key.WithHelp("↑↓", "scroll")),
			key.NewBinding(key.WithKeys("shift+up"), key.WithHelp("shift+↑↓", "select")),
			c.Expand, c.Copy, withHelp(km.Tab, "focus editor"))
	} else {
		out = append(out, km.Editor.Send)
		if m.tr.Len() > 0 {
			out = append(out, km.Tab)
		}
		cmds := km.Commands
		if m.input.Value() == "" {
			cmds = withHelp(key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("/ or ctrl+p", "")), "commands")
		}
		out = append(out, cmds, km.Sessions)
	}
	return append(out, km.Help)
}

func (m *model) fullHelpKeys() [][]key.Binding {
	km := m.keys
	c := km.Chat
	e := km.Editor
	h := func(k, desc string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, desc)) }
	// The global keys, then those of whatever has focus.
	cols := [][]key.Binding{
		{km.Commands, km.Sessions, km.Agents, km.New, withHelp(km.Cancel, "interrupt (twice)"), withHelp(km.Help, "less")},
		{withHelp(km.Tab, "switch focus"), km.Sidebar, km.Details, km.Suspend, km.Quit},
	}
	if m.focus == focusChat {
		return append(cols, []key.Binding{h("↑↓/jk", "scroll"), h("shift+↑↓", "select"), h("u/d", "half page"), h("b/f", "page"), h("g/G", "top/bottom"), c.Expand, c.Copy})
	}
	return append(cols, []key.Binding{e.Send, e.Newline, e.OpenEditor, h("↑↓", "history"), h("/", "commands")})
}
