// Package chat holds the transcript's items: user and assistant messages,
// tool calls, notices and errors. Each is a list.Item that renders itself
// at a width, caches the result, and bumps its version when it changes.
//
// The items and their look follow Crush (github.com/charmbracelet/crush,
// internal/ui/chat): a left gutter that thickens on the selected item, no
// boxes, collapsible reasoning and tool output, and an animated spinner.
package chat

import (
	"fmt"
	"strings"
	"time"

	"charm.land/glamour/v2"
	gansi "charm.land/glamour/v2/ansi"
	mdstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/anas-domesticus/personal-agent/internal/tui/anim"
	"github.com/anas-domesticus/personal-agent/internal/tui/list"
	"github.com/anas-domesticus/personal-agent/internal/tui/styles"
)

const (
	// maxTextWidth caps prose so long lines stay readable.
	maxTextWidth = 120
	// gutterWidth is the width of the left bar and its space.
	gutterWidth = 2
	// collapsedLines is how many lines of reasoning or tool output show
	// before the rest is hidden behind a hint.
	collapsedLines = 10
)

// Item is a transcript entry.
type Item interface {
	list.Item
	list.Focusable
	// Text is the item as plain text, for copying.
	Text() string
}

// Expandable is an item with hidden content that space shows.
type Expandable interface {
	// ToggleExpanded shows or hides the content and reports whether the
	// item had anything to toggle.
	ToggleExpanded() bool
}

// Animatable is an item showing a spinner. The UI runs one clock and
// advances every item that is spinning.
type Animatable interface {
	Spinning() bool
	Advance()
}

// Env is what items need to render: the styles and a markdown renderer.
// The UI owns one and swaps the styles when the terminal's background is
// known.
type Env struct {
	Sty *styles.Styles

	md      *glamour.TermRenderer
	mdWidth int
	mdDark  bool
}

// NewEnv returns an Env for the given styles.
func NewEnv(sty *styles.Styles) *Env { return &Env{Sty: sty} }

// Markdown renders s as markdown at width. It falls back to wrapped text
// if the renderer fails.
func (e *Env) Markdown(s string, width int) string {
	if e.md == nil || e.mdWidth != width || e.mdDark != e.Sty.Dark {
		md, err := glamour.NewTermRenderer(glamour.WithStyles(markdownStyle(e.Sty.Dark)), glamour.WithWordWrap(width), glamour.WithPreservedNewLines())
		if err != nil {
			return lipgloss.Wrap(s, width, "")
		}
		e.md, e.mdWidth, e.mdDark = md, width, e.Sty.Dark
	}
	out, err := e.md.Render(s)
	if err != nil {
		return lipgloss.Wrap(s, width, "")
	}
	return trimBlank(out)
}

// markdownStyle is glamour's standard style without its margins: items
// draw their own gutter.
func markdownStyle(dark bool) gansi.StyleConfig {
	st := mdstyles.LightStyleConfig
	if dark {
		st = mdstyles.DarkStyleConfig
	}
	zero := uint(0)
	st.Document.Margin = &zero
	st.CodeBlock.Margin = &zero
	return st
}

// spinner builds a turn spinner with a label and an optional elapsed time.
func (e *Env) spinner(label string, size int, since time.Time) *anim.Anim {
	p := e.Sty.P
	set := anim.Settings{
		Size:        size,
		Label:       label,
		LabelColor:  p.FgMoreSubtle,
		GradColorA:  p.Primary,
		GradColorB:  p.Secondary,
		CycleColors: true,
		SuffixColor: p.FgMostSubtle,
	}
	if !since.IsZero() {
		set.Suffix = func() string { return Elapsed(time.Since(since)) }
	}
	return anim.New(set)
}

// Elapsed formats a duration as "12s", "3m 4s" or "1h 2m".
func Elapsed(d time.Duration) string {
	s := int(d.Seconds())
	switch {
	case s >= 3600:
		return fmt.Sprintf("%dh %dm", s/3600, s/60%60)
	case s >= 60:
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	}
	return fmt.Sprintf("%ds", s)
}

// base holds what every item shares: its version, focus and render cache.
type base struct {
	*list.Versioned
	env     *Env
	focused bool

	cache      string
	cacheWidth int
	cacheVer   uint64
}

func newBase(env *Env) base { return base{Versioned: list.NewVersioned(), env: env} }

// SetFocused implements list.Focusable.
func (b *base) SetFocused(f bool) {
	if b.focused != f {
		b.focused = f
		b.Bump()
	}
}

// cached returns the cached render if nothing changed since.
func (b *base) cached(width int) (string, bool) {
	return b.cache, b.cache != "" && b.cacheWidth == width && b.cacheVer == b.Version()
}

func (b *base) store(width int, s string) string {
	b.cache, b.cacheWidth, b.cacheVer = s, width, b.Version()
	return s
}

// Restyle drops the cache after a theme change.
func (b *base) Restyle() {
	b.cache = ""
	b.Bump()
}

// contentWidth is the width available to an item's text.
func contentWidth(width int) int {
	return max(min(width-gutterWidth, maxTextWidth), 10)
}

// gutter prefixes every line with bar, or the focus bar when focused.
func gutter(s string, bar, focus lipgloss.Style, barGlyph string, focused bool) string {
	prefix := bar.Render(barGlyph) + " "
	if barGlyph == "" {
		prefix = "  "
	}
	if focused {
		prefix = focus.Render(styles.FocusBar) + " "
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// collapse keeps the last n lines of s, and says how many it hid.
func collapse(s string, n int) (string, int) {
	lines := strings.Split(s, "\n")
	if len(lines) <= n+1 { // hiding one line saves nothing
		return s, 0
	}
	return strings.Join(lines[len(lines)-n:], "\n"), len(lines) - n
}

// head keeps the first n lines of s, and says how many it hid.
func head(s string, n int) (string, int) {
	lines := strings.Split(s, "\n")
	if len(lines) <= n+1 {
		return s, 0
	}
	return strings.Join(lines[:n], "\n"), len(lines) - n
}

func trimBlank(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[0])) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
