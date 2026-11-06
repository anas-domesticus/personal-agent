// Package styles holds the TUI's palette and every style built from it, so
// components use semantic names ("subtle", "tool name") rather than colours.
//
// The structure follows Crush (github.com/charmbracelet/crush): a small set
// of colour tokens feeds one Styles struct, grouped by the part of the screen
// it styles. The palette is our own.
package styles

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Glyphs used across the UI.
const (
	IconPending   = "●"
	IconSuccess   = "✓"
	IconError     = "×"
	IconModel     = "◇"
	IconAgent     = "◆"
	FocusBar      = "▌"
	UserBar       = "│"
	Diagonal      = "╱"
	SectionRule   = "─"
	ScrollThumb   = "┃"
	ScrollTrack   = "│"
	RadioOn       = "◉"
	RadioOff      = "○"
	Separator     = " • "
	EllipsisGlyph = "…"
)

// Palette is the set of colour tokens every style is built from.
type Palette struct {
	Primary, Secondary, Accent color.Color

	FgBase, FgSubtle, FgMoreSubtle, FgMostSubtle color.Color
	BgBase, BgLeastVisible, BgMoreVisible        color.Color
	Separator                                    color.Color

	Success, SuccessSubtle color.Color
	Error, Warning, Info   color.Color
	OnPrimary              color.Color
}

func darkPalette() Palette {
	return Palette{
		Primary:   lipgloss.Color("#5E8BFF"),
		Secondary: lipgloss.Color("#3DDBC6"),
		Accent:    lipgloss.Color("#FFB86B"),

		FgBase:       lipgloss.Color("#E8EAF0"),
		FgSubtle:     lipgloss.Color("#B8BCCB"),
		FgMoreSubtle: lipgloss.Color("#828799"),
		FgMostSubtle: lipgloss.Color("#5C6070"),

		BgBase:         lipgloss.Color("#1B1D24"),
		BgLeastVisible: lipgloss.Color("#262932"),
		BgMoreVisible:  lipgloss.Color("#3A3E4A"),
		Separator:      lipgloss.Color("#353845"),

		Success:       lipgloss.Color("#4BE3A0"),
		SuccessSubtle: lipgloss.Color("#2FA877"),
		Error:         lipgloss.Color("#F0566E"),
		Warning:       lipgloss.Color("#F2D04B"),
		Info:          lipgloss.Color("#4FB4FF"),
		OnPrimary:     lipgloss.Color("#FAFBFF"),
	}
}

func lightPalette() Palette {
	return Palette{
		Primary:   lipgloss.Color("#3360E0"),
		Secondary: lipgloss.Color("#0E9C8A"),
		Accent:    lipgloss.Color("#C46A12"),

		FgBase:       lipgloss.Color("#1E2029"),
		FgSubtle:     lipgloss.Color("#474B5A"),
		FgMoreSubtle: lipgloss.Color("#6E7385"),
		FgMostSubtle: lipgloss.Color("#9A9EAD"),

		BgBase:         lipgloss.Color("#FBFBFD"),
		BgLeastVisible: lipgloss.Color("#EEF0F5"),
		BgMoreVisible:  lipgloss.Color("#D9DCE5"),
		Separator:      lipgloss.Color("#D3D6DF"),

		Success:       lipgloss.Color("#13985E"),
		SuccessSubtle: lipgloss.Color("#3BAF7E"),
		Error:         lipgloss.Color("#D42D4A"),
		Warning:       lipgloss.Color("#A67C00"),
		Info:          lipgloss.Color("#1C7FD6"),
		OnPrimary:     lipgloss.Color("#FFFFFF"),
	}
}

// Styles is every style the TUI uses.
type Styles struct {
	Dark bool
	P    Palette

	Base, Subtle, MoreSubtle, MostSubtle lipgloss.Style
	Bold                                 lipgloss.Style

	Header struct {
		Name, Detail, Separator lipgloss.Style
	}

	Chat struct {
		UserBar, FocusBar, AssistantFocusBar lipgloss.Style
		User                                 lipgloss.Style
		ThinkingBox, ThinkingHint            lipgloss.Style
		ThinkingFooter                       lipgloss.Style
		Canceled                             lipgloss.Style
		Notice                               lipgloss.Style
		ErrorTag, WarnTag                    lipgloss.Style
		ErrorTitle, ErrorDetail              lipgloss.Style
		Info, InfoRule                       lipgloss.Style
	}

	Tool struct {
		Pending, Success, Error, Canceled lipgloss.Style
		Name, Param, Body, Hint, Status   lipgloss.Style
	}

	Editor struct {
		Prompt, PromptBlurred, Continuation lipgloss.Style
	}

	Dialog struct {
		Frame, DangerFrame             lipgloss.Style
		Title, DangerTitle             lipgloss.Style
		Prompt, Input, Placeholder     lipgloss.Style
		Row, RowSelected, Info, Match  lipgloss.Style
		InfoSelected, Empty            lipgloss.Style
		Button, ButtonSelected         lipgloss.Style
		ScrollThumb, ScrollTrack, Help lipgloss.Style
	}

	Status struct {
		Key, Desc, Sep lipgloss.Style
		OKTag, OKBand  lipgloss.Style
		WarnTag        lipgloss.Style
		WarnBand       lipgloss.Style
		ErrTag         lipgloss.Style
		ErrBand        lipgloss.Style
	}

	Section struct {
		Title, Line lipgloss.Style
	}
}

// New builds the styles for a dark or light terminal background.
func New(dark bool) *Styles {
	p := lightPalette()
	if dark {
		p = darkPalette()
	}
	s := &Styles{Dark: dark, P: p}
	fg := func(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

	s.Base = fg(p.FgBase)
	s.Subtle = fg(p.FgSubtle)
	s.MoreSubtle = fg(p.FgMoreSubtle)
	s.MostSubtle = fg(p.FgMostSubtle)
	s.Bold = s.Base.Bold(true)

	s.Header.Name = fg(p.Primary).Bold(true)
	s.Header.Detail = s.MoreSubtle
	s.Header.Separator = fg(p.Separator)

	c := &s.Chat
	c.UserBar = fg(p.Primary)
	c.FocusBar = fg(p.Primary)
	c.AssistantFocusBar = fg(p.SuccessSubtle)
	c.User = s.Base
	c.ThinkingBox = s.MoreSubtle.Background(p.BgLeastVisible).Padding(0, 1)
	c.ThinkingHint = s.MostSubtle.Italic(true)
	c.ThinkingFooter = s.MostSubtle
	c.Canceled = s.MoreSubtle.Italic(true)
	c.Notice = s.MoreSubtle.Italic(true)
	c.ErrorTag = lipgloss.NewStyle().Foreground(p.OnPrimary).Background(p.Error).Bold(true).Padding(0, 1)
	c.WarnTag = lipgloss.NewStyle().Foreground(p.BgBase).Background(p.Warning).Bold(true).Padding(0, 1)
	c.ErrorTitle = s.Subtle
	c.ErrorDetail = s.MostSubtle
	c.Info = s.MoreSubtle
	c.InfoRule = fg(p.Separator)

	t := &s.Tool
	t.Pending = fg(p.SuccessSubtle)
	t.Success = fg(p.Success)
	t.Error = fg(p.Error)
	t.Canceled = s.MostSubtle
	t.Name = fg(p.Info)
	t.Param = s.MoreSubtle
	t.Body = s.MoreSubtle.Background(p.BgLeastVisible)
	t.Hint = s.MostSubtle.Italic(true)
	t.Status = s.MoreSubtle.Italic(true)

	s.Editor.Prompt = fg(p.Success).Bold(true)
	s.Editor.PromptBlurred = s.MostSubtle
	s.Editor.Continuation = s.MostSubtle

	d := &s.Dialog
	d.Frame = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Primary).Padding(0, 1)
	d.DangerFrame = d.Frame.BorderForeground(p.Error)
	d.Title = fg(p.Primary).Bold(true)
	d.DangerTitle = fg(p.Error).Bold(true)
	d.Prompt = fg(p.Success).Bold(true)
	d.Input = s.Base
	d.Placeholder = s.MostSubtle
	d.Row = s.Subtle.Padding(0, 1)
	d.RowSelected = lipgloss.NewStyle().Foreground(p.OnPrimary).Background(p.Primary).Padding(0, 1)
	d.Info = s.MostSubtle
	d.InfoSelected = lipgloss.NewStyle().Foreground(p.OnPrimary).Background(p.Primary)
	d.Match = lipgloss.NewStyle().Underline(true)
	d.Empty = s.MostSubtle.Italic(true).Padding(0, 1)
	d.Button = s.Subtle.Background(p.BgMoreVisible).Padding(0, 2)
	d.ButtonSelected = lipgloss.NewStyle().Foreground(p.OnPrimary).Background(p.Primary).Padding(0, 2)
	d.ScrollThumb = fg(p.Primary)
	d.ScrollTrack = fg(p.Separator)
	d.Help = s.MostSubtle

	st := &s.Status
	st.Key = s.MoreSubtle
	st.Desc = s.MostSubtle
	st.Sep = fg(p.Separator)
	st.OKTag = lipgloss.NewStyle().Foreground(p.BgBase).Background(p.Success).Bold(true).Padding(0, 1)
	st.OKBand = lipgloss.NewStyle().Foreground(p.BgBase).Background(p.SuccessSubtle).Padding(0, 1)
	st.WarnTag = lipgloss.NewStyle().Foreground(p.BgBase).Background(p.Warning).Bold(true).Padding(0, 1)
	st.WarnBand = lipgloss.NewStyle().Foreground(p.FgBase).Background(p.BgMoreVisible).Padding(0, 1)
	st.ErrTag = lipgloss.NewStyle().Foreground(p.OnPrimary).Background(p.Error).Bold(true).Padding(0, 1)
	st.ErrBand = lipgloss.NewStyle().Foreground(p.FgBase).Background(p.BgMoreVisible).Padding(0, 1)

	s.Section.Title = s.MoreSubtle
	s.Section.Line = fg(p.Separator)
	return s
}

// Gradient renders text with a horizontal gradient from one colour to
// another, one rune at a time.
func Gradient(text string, from, to color.Color, bold bool) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	ramp := lipgloss.Blend1D(len(runes), from, to)
	var b strings.Builder
	for i, r := range runes {
		st := lipgloss.NewStyle().Foreground(ramp[i]).Bold(bold)
		b.WriteString(st.Render(string(r)))
	}
	return b.String()
}

// Diagonals is a run of ╱ in the primary-to-secondary gradient.
func (s *Styles) Diagonals(n int) string {
	if n <= 0 {
		return ""
	}
	return Gradient(strings.Repeat(Diagonal, n), s.P.Primary, s.P.Secondary, false)
}

// Wordmark is the product name in its gradient.
func (s *Styles) Wordmark() string {
	return Gradient("personal agent", s.P.Primary, s.P.Secondary, true)
}

// Title is a title followed by diagonals filling width, truncated rather
// than wrapped when it doesn't fit.
func (s *Styles) Title(title string, st lipgloss.Style, width int) string {
	title = st.Render(title)
	if lipgloss.Width(title) >= width {
		return ansi.Truncate(title, width, EllipsisGlyph)
	}
	return title + " " + s.Diagonals(width-lipgloss.Width(title)-1)
}

// SectionTitle is a heading followed by a rule filling width.
func (s *Styles) SectionTitle(title string, width int) string {
	title = s.Section.Title.Render(title)
	rest := width - lipgloss.Width(title) - 1
	if rest <= 0 {
		return ansi.Truncate(title, width, EllipsisGlyph)
	}
	return title + " " + s.Section.Line.Render(strings.Repeat(SectionRule, rest))
}

// Scrollbar draws a vertical scrollbar of height rows for content of
// contentSize rows seen through a viewport of viewSize rows at offset. It
// is empty when everything fits.
func (s *Styles) Scrollbar(height, contentSize, viewSize, offset int) string {
	if height <= 0 || contentSize <= viewSize {
		return ""
	}
	thumb := max(1, height*viewSize/contentSize)
	maxOffset := contentSize - viewSize
	track := height - thumb
	pos := 0
	if track > 0 {
		pos = min(track, offset*track/maxOffset)
	}
	rows := make([]string, height)
	for i := range rows {
		if i >= pos && i < pos+thumb {
			rows[i] = s.Dialog.ScrollThumb.Render(ScrollThumb)
		} else {
			rows[i] = s.Dialog.ScrollTrack.Render(ScrollTrack)
		}
	}
	return strings.Join(rows, "\n")
}
