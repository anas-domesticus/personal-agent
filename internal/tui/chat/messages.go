package chat

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/anas-domesticus/personal-agent/internal/tui/anim"
	"github.com/anas-domesticus/personal-agent/internal/tui/styles"
)

// User is a message the user sent.
type User struct {
	base
	text string
}

// NewUser returns a user message.
func NewUser(env *Env, text string) *User { return &User{base: newBase(env), text: text} }

// Render implements list.Item.
func (u *User) Render(width int) string {
	if s, ok := u.cached(width); ok {
		return s
	}
	st := u.env.Sty
	body := u.env.Markdown(u.text, contentWidth(width))
	return u.store(width, gutter(body, st.Chat.UserBar, st.Chat.FocusBar, styles.UserBar, u.focused))
}

// Finished implements list.Item.
func (u *User) Finished() bool { return true }

// Text implements Item.
func (u *User) Text() string { return u.text }

// Content is the message as sent.
func (u *User) Content() string { return u.text }

// Assistant is the agent's reply: optional reasoning, then content. While
// it streams with nothing to show yet, it shows the spinner.
type Assistant struct {
	base
	content, reasoning string
	streaming          bool
	canceled           bool

	thinkStart, thinkEnd time.Time // set for a streamed reply
	expanded             bool      // reasoning shown in full

	spin *anim.Anim

	// mdPrefix caches the markdown of the stable part of a streaming reply,
	// so each delta only re-renders the tail.
	mdPrefix, mdPrefixOut string
	mdPrefixWidth         int
}

// NewAssistant returns a complete assistant message.
func NewAssistant(env *Env, content, reasoning string) *Assistant {
	return &Assistant{base: newBase(env), content: content, reasoning: reasoning}
}

// NewStreaming returns an assistant message that is being written, with a
// spinner showing time since the turn started.
func NewStreaming(env *Env, turnStart time.Time) *Assistant {
	a := &Assistant{base: newBase(env), streaming: true}
	a.spin = env.spinner("Thinking", 15, turnStart)
	return a
}

// Streaming reports whether the message is still being written.
func (a *Assistant) Streaming() bool { return a.streaming }

// Empty reports whether there is nothing to show.
func (a *Assistant) Empty() bool {
	return strings.TrimSpace(a.content) == "" && strings.TrimSpace(a.reasoning) == ""
}

// Append adds a streamed fragment.
func (a *Assistant) Append(content, reasoning string) {
	if reasoning != "" && a.thinkStart.IsZero() {
		a.thinkStart = time.Now()
	}
	if content != "" && !a.thinkStart.IsZero() && a.thinkEnd.IsZero() {
		a.thinkEnd = time.Now()
	}
	a.content += content
	a.reasoning += reasoning
	a.Bump()
}

// Finish replaces the fragments with the complete message.
func (a *Assistant) Finish(content, reasoning string) {
	if !a.thinkStart.IsZero() && a.thinkEnd.IsZero() {
		a.thinkEnd = time.Now()
	}
	a.content, a.reasoning, a.streaming = content, reasoning, false
	a.Bump()
}

// Cancel ends streaming for a turn that was interrupted.
func (a *Assistant) Cancel() {
	a.streaming, a.canceled = false, true
	a.Bump()
}

// Settle ends streaming without marking the message cancelled.
func (a *Assistant) Settle() {
	a.streaming = false
	a.Bump()
}

// Spinning implements Animatable.
func (a *Assistant) Spinning() bool { return a.streaming && a.Empty() && a.spin != nil }

// Advance implements Animatable.
func (a *Assistant) Advance() {
	a.spin.Advance()
	a.Bump()
}

// ToggleExpanded implements Expandable: it shows or hides the reasoning.
func (a *Assistant) ToggleExpanded() bool {
	if strings.TrimSpace(a.reasoning) == "" {
		return false
	}
	a.expanded = !a.expanded
	a.Bump()
	return true
}

// Restyle implements a theme change.
func (a *Assistant) Restyle() {
	if a.spin != nil {
		p := a.env.Sty.P
		a.spin.SetColors(p.FgMoreSubtle, p.Primary, p.Secondary, p.FgMostSubtle)
	}
	a.mdPrefix = ""
	a.base.Restyle()
}

// Render implements list.Item.
func (a *Assistant) Render(width int) string {
	if s, ok := a.cached(width); ok {
		return s
	}
	st := a.env.Sty
	w := contentWidth(width)
	var parts []string
	if r := strings.TrimSpace(a.reasoning); r != "" {
		parts = append(parts, a.renderThinking(r, w))
	}
	switch {
	case a.Spinning():
		parts = append(parts, a.spin.Render())
	case strings.TrimSpace(a.content) != "":
		parts = append(parts, a.renderContent(w))
	}
	if a.canceled {
		parts = append(parts, st.Chat.Canceled.Render("Interrupted"))
	}
	out := strings.Join(parts, "\n\n")
	return a.store(width, gutter(out, st.Chat.UserBar, st.Chat.AssistantFocusBar, "", a.focused))
}

func (a *Assistant) renderThinking(r string, w int) string {
	st := a.env.Sty
	body := lipgloss.Wrap(r, w-2, "")
	hidden := 0
	if !a.expanded {
		body, hidden = collapse(body, collapsedLines)
	}
	var lines []string
	if hidden > 0 {
		lines = append(lines, st.Chat.ThinkingHint.Render(fmt.Sprintf("… (%d lines hidden) [space to expand]", hidden)))
	}
	lines = append(lines, body)
	box := st.Chat.ThinkingBox.Width(w).Render(strings.Join(lines, "\n"))
	if !a.thinkStart.IsZero() && !a.thinkEnd.IsZero() {
		box += "\n" + st.Chat.ThinkingFooter.Render("Thought for "+fmt.Sprintf("%.1fs", a.thinkEnd.Sub(a.thinkStart).Seconds()))
	}
	return box
}

// renderContent renders the reply as markdown. While streaming, the part
// up to the last paragraph break outside a code block is stable, so its
// render is kept and only the tail is redone.
func (a *Assistant) renderContent(w int) string {
	if !a.streaming {
		return a.env.Markdown(a.content, w)
	}
	cut := stableCut(a.content)
	prefix, tail := a.content[:cut], a.content[cut:]
	if prefix != a.mdPrefix || w != a.mdPrefixWidth {
		a.mdPrefix, a.mdPrefixWidth = prefix, w
		a.mdPrefixOut = ""
		if strings.TrimSpace(prefix) != "" {
			a.mdPrefixOut = a.env.Markdown(prefix, w)
		}
	}
	tailOut := ""
	if strings.TrimSpace(tail) != "" {
		tailOut = a.env.Markdown(tail, w)
	}
	switch {
	case a.mdPrefixOut == "":
		return tailOut
	case tailOut == "":
		return a.mdPrefixOut
	}
	return a.mdPrefixOut + "\n\n" + tailOut
}

// stableCut is the index after the last blank line that isn't inside a
// fenced code block, or 0.
func stableCut(s string) int {
	cut, fenced, off := 0, false, 0
	for line := range strings.SplitAfterSeq(s, "\n") {
		off += len(line)
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~"):
			fenced = !fenced
		case t == "" && !fenced && strings.HasSuffix(line, "\n"):
			cut = off
		}
	}
	return cut
}

// Finished implements list.Item.
func (a *Assistant) Finished() bool { return !a.streaming }

// Text implements Item.
func (a *Assistant) Text() string { return a.content }

// Notice is a line from the TUI itself, such as "Interrupted.".
type Notice struct {
	base
	text string
}

// NewNotice returns a notice.
func NewNotice(env *Env, text string) *Notice { return &Notice{base: newBase(env), text: text} }

// Render implements list.Item.
func (n *Notice) Render(width int) string {
	if s, ok := n.cached(width); ok {
		return s
	}
	st := n.env.Sty
	body := st.Chat.Notice.Render(lipgloss.Wrap(n.text, contentWidth(width), ""))
	return n.store(width, gutter(body, st.Chat.UserBar, st.Chat.FocusBar, "", n.focused))
}

// Finished implements list.Item.
func (n *Notice) Finished() bool { return true }

// Text implements Item.
func (n *Notice) Text() string { return n.text }

// Error is a failure: a tag, a title and optional details.
type Error struct {
	base
	title, detail string
	warn          bool
}

// NewError returns an error banner.
func NewError(env *Env, title, detail string) *Error {
	return &Error{base: newBase(env), title: title, detail: detail}
}

// NewWarning returns a warning banner.
func NewWarning(env *Env, title, detail string) *Error {
	return &Error{base: newBase(env), title: title, detail: detail, warn: true}
}

// Render implements list.Item.
func (e *Error) Render(width int) string {
	if s, ok := e.cached(width); ok {
		return s
	}
	st := e.env.Sty
	w := contentWidth(width)
	tag := st.Chat.ErrorTag.Render("ERROR")
	if e.warn {
		tag = st.Chat.WarnTag.Render("WARN")
	}
	titleW := w - lipgloss.Width(tag) - 1
	title := st.Chat.ErrorTitle.Render(lipgloss.Wrap(e.title, max(titleW, 10), ""))
	out := lipgloss.JoinHorizontal(lipgloss.Top, tag, " ", title)
	if e.detail != "" {
		out += "\n\n" + st.Chat.ErrorDetail.Render(lipgloss.Wrap(e.detail, w, ""))
	}
	return e.store(width, gutter(out, st.Chat.UserBar, st.Chat.FocusBar, "", e.focused))
}

// Finished implements list.Item.
func (e *Error) Finished() bool { return true }

// Text implements Item.
func (e *Error) Text() string {
	if e.detail == "" {
		return e.title
	}
	return e.title + "\n\n" + e.detail
}

// Info closes a turn: the model that answered and how long it took.
type Info struct {
	base
	model    string
	duration time.Duration
}

// NewInfo returns a turn footer.
func NewInfo(env *Env, model string, d time.Duration) *Info {
	return &Info{base: newBase(env), model: model, duration: d}
}

// Render implements list.Item.
func (i *Info) Render(width int) string {
	if s, ok := i.cached(width); ok {
		return s
	}
	st := i.env.Sty
	w := contentWidth(width)
	text := st.Chat.Info.Render(styles.IconModel + " " + i.model)
	if i.duration > 0 {
		text += st.MostSubtle.Render(" in " + Elapsed(i.duration))
	}
	text = ansi.Truncate(text, w, styles.EllipsisGlyph)
	if rest := w - lipgloss.Width(text) - 1; rest > 0 {
		text += " " + st.Chat.InfoRule.Render(strings.Repeat(styles.SectionRule, rest))
	}
	return i.store(width, gutter(text, st.Chat.UserBar, st.Chat.FocusBar, "", i.focused))
}

// Finished implements list.Item.
func (i *Info) Finished() bool { return true }

// Text implements Item.
func (i *Info) Text() string { return i.model }
