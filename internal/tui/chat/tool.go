package chat

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/anas-domesticus/personal-agent/internal/tui/anim"
	"github.com/anas-domesticus/personal-agent/internal/tui/styles"
)

// ToolState is where a tool call has got to.
type ToolState int

// The states of a tool call.
const (
	ToolRunning ToolState = iota
	ToolDone
	ToolFailed
	ToolCanceled
)

// Tool is a tool call and, once it has run, its result.
type Tool struct {
	base
	id, name, args string
	state          ToolState
	output         string // what the tool returned, or why it failed
	expanded       bool
	spin           *anim.Anim
}

// NewTool returns a running tool call.
func NewTool(env *Env, id, name, args string) *Tool {
	return &Tool{
		base: newBase(env), id: id, name: name, args: args,
		spin: env.spinner("", 8, time.Time{}),
	}
}

// ID is the call's id.
func (t *Tool) ID() string { return t.id }

// Running reports whether the call has no result yet.
func (t *Tool) Running() bool { return t.state == ToolRunning }

// SetResult records what the tool returned. Tools return
// {"ok": bool, "error": ..., "data": ...}; anything else is shown as is.
func (t *Tool) SetResult(content string) {
	t.state, t.output = ToolDone, content
	var r struct {
		OK    *bool           `json:"ok"`
		Error string          `json:"error"`
		Data  json.RawMessage `json:"data"`
	}
	if json.Unmarshal([]byte(content), &r) == nil && r.OK != nil {
		switch {
		case !*r.OK:
			t.state, t.output = ToolFailed, r.Error
		case len(r.Data) > 0:
			t.output = string(r.Data)
		default:
			t.output = ""
		}
	}
	t.Bump()
}

// Cancel marks a call that never got a result.
func (t *Tool) Cancel() {
	if t.state == ToolRunning {
		t.state = ToolCanceled
		t.Bump()
	}
}

// Spinning implements Animatable.
func (t *Tool) Spinning() bool { return t.state == ToolRunning }

// Advance implements Animatable.
func (t *Tool) Advance() {
	t.spin.Advance()
	t.Bump()
}

// ToggleExpanded implements Expandable: it shows the whole output.
func (t *Tool) ToggleExpanded() bool {
	if t.state == ToolRunning || strings.TrimSpace(t.output) == "" {
		return false
	}
	t.expanded = !t.expanded
	t.Bump()
	return true
}

// Restyle implements a theme change.
func (t *Tool) Restyle() {
	p := t.env.Sty.P
	t.spin.SetColors(p.FgMoreSubtle, p.Primary, p.Secondary, p.FgMostSubtle)
	t.base.Restyle()
}

// Finished implements list.Item.
func (t *Tool) Finished() bool { return t.state != ToolRunning }

// Text implements Item: the call and its result.
func (t *Tool) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", t.name, t.args)
	if t.output != "" {
		b.WriteString("\n\n" + t.output)
	}
	return b.String()
}

// Render implements list.Item.
func (t *Tool) Render(width int) string {
	if s, ok := t.cached(width); ok {
		return s
	}
	st := t.env.Sty
	w := contentWidth(width)
	out := t.header(w)
	switch t.state {
	case ToolRunning:
		out += " " + t.spin.Render()
	case ToolCanceled:
		out += "\n\n" + st.Tool.Status.Render("Interrupted.")
	case ToolFailed:
		msg := strings.Join(strings.Fields(t.output), " ")
		line := st.Chat.ErrorTag.Render("ERROR") + " " + st.Chat.ErrorDetail.Render(msg)
		if !t.expanded {
			line = ansi.Truncate(line, w, styles.EllipsisGlyph)
		} else {
			line = st.Chat.ErrorTag.Render("ERROR") + "\n" + st.Chat.ErrorDetail.Render(lipgloss.Wrap(t.output, w, ""))
		}
		out += "\n\n" + line
	case ToolDone:
		if body := t.body(w); body != "" {
			out += "\n\n" + body
		}
	}
	return t.store(width, gutter(out, st.Chat.UserBar, st.Chat.FocusBar, "", t.focused))
}

// header is "● Name main-param (k=v, …)", truncated to w.
func (t *Tool) header(w int) string {
	st := t.env.Sty
	var icon string
	switch t.state {
	case ToolRunning:
		icon = st.Tool.Pending.Render(styles.IconPending)
	case ToolDone:
		icon = st.Tool.Success.Render(styles.IconSuccess)
	case ToolFailed:
		icon = st.Tool.Error.Render(styles.IconError)
	case ToolCanceled:
		icon = st.Tool.Canceled.Render(styles.IconPending)
	}
	name := st.Tool.Name.Render(humanize(t.name))
	head := icon + " " + name
	main, rest := summarize(t.args)
	avail := w - lipgloss.Width(head) - 1
	if main != "" {
		params := main
		// Keep the extra parameters only if the main one still has room.
		if extra := strings.Join(rest, ", "); extra != "" && avail-len(" ("+extra+")") >= 30 {
			params += " (" + extra + ")"
		}
		head += " " + st.Tool.Param.Render(ansi.Truncate(params, max(avail, 1), styles.EllipsisGlyph))
	}
	return ansi.Truncate(head, w, styles.EllipsisGlyph)
}

// body is the tool's output on a panel, cut to its first lines unless
// expanded. JSON is pretty-printed as a highlighted code block.
func (t *Tool) body(w int) string {
	st := t.env.Sty
	text := strings.TrimSpace(t.output)
	if text == "" {
		return ""
	}
	if s, ok := unquote(text); ok {
		text = s
	} else if pretty, ok := prettyJSON(text); ok {
		text = pretty
		shown, hidden := text, 0
		if !t.expanded {
			shown, hidden = head(text, collapsedLines)
		}
		out := t.env.Markdown("```json\n"+shown+"\n```", w)
		if hidden > 0 {
			out += "\n" + t.hint(hidden)
		}
		return out
	}
	shown, hidden := text, 0
	if !t.expanded {
		shown, hidden = head(text, collapsedLines)
	}
	lines := strings.Split(shown, "\n")
	for i, l := range lines {
		l = ansi.Truncate(strings.TrimRight(l, "\r"), w-2, styles.EllipsisGlyph)
		lines[i] = st.Tool.Body.Width(w).Padding(0, 1).Render(l)
	}
	out := strings.Join(lines, "\n")
	if hidden > 0 {
		out += "\n" + t.hint(hidden)
	}
	return out
}

func (t *Tool) hint(hidden int) string {
	return t.env.Sty.Tool.Hint.Render(fmt.Sprintf("… (%d lines hidden) [space to expand]", hidden))
}

// mainParams are the arguments that best describe a call, in order.
var mainParams = []string{"command", "path", "file_path", "url", "query", "pattern", "name", "prompt", "description"}

// summarize splits a call's JSON arguments into its main parameter and the
// rest as k=v.
func summarize(raw string) (main string, rest []string) {
	var args map[string]any
	if json.Unmarshal([]byte(raw), &args) != nil {
		return oneLine(raw), nil
	}
	if len(args) == 0 {
		return "", nil
	}
	for _, k := range mainParams {
		if s, ok := args[k].(string); ok && s != "" {
			main = oneLine(s)
			delete(args, k)
			break
		}
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		v, err := json.Marshal(args[k])
		if err != nil {
			continue
		}
		s := strings.Trim(string(v), `"`)
		if len(s) > 40 {
			s = s[:39] + styles.EllipsisGlyph
		}
		rest = append(rest, k+"="+oneLine(s))
	}
	if main == "" && len(rest) > 0 {
		main, rest = rest[0], rest[1:]
	}
	return main, rest
}

// humanize turns a tool name such as "search_memories" into "Search memories".
func humanize(name string) string {
	s := strings.NewReplacer("_", " ", "-", " ").Replace(name)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func unquote(s string) (string, bool) {
	var out string
	if json.Unmarshal([]byte(s), &out) != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

func prettyJSON(s string) (string, bool) {
	if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
		return "", false
	}
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return "", false
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", false
	}
	return string(out), true
}
