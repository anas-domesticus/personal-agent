package dialog

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"

	"github.com/anas-domesticus/personal-agent/internal/tui/styles"
)

const (
	maxWidth  = 70
	maxHeight = 20
	// infoShare is the most of a row the info column may take before it is
	// hidden for every row.
	infoShare = 0.35
)

// Entry is one row of a picker.
type Entry struct {
	ID    string
	Title string
	// Info is shown on the right: a shortcut, a time, a version.
	Info string
	// Aliases also match the filter ("clear" finds New conversation).
	Aliases []string
}

// Picker is a filterable list: the command palette and the conversation
// and agent pickers.
type Picker struct {
	id, title string
	sty       *styles.Styles
	all       []Entry
	shown     []shown
	cursor    int
	offset    int
	input     textinput.Model
	help      string
	empty     string
	lastRows  int
}

type shown struct {
	Entry
	matched map[int]bool // rune positions in Title to underline
}

// NewPicker returns a picker. selected is the ID to start on, if present.
func NewPicker(sty *styles.Styles, id, title string, entries []Entry, selected, empty string) *Picker {
	in := textinput.New()
	in.Prompt = "> "
	in.Placeholder = "Type to filter"
	in.SetVirtualCursor(false)
	in.SetStyles(textinput.Styles{
		Focused: textinput.StyleState{Prompt: sty.Dialog.Prompt, Text: sty.Dialog.Input, Placeholder: sty.Dialog.Placeholder},
		Blurred: textinput.StyleState{Prompt: sty.Dialog.Prompt, Text: sty.Dialog.Input, Placeholder: sty.Dialog.Placeholder},
	})
	in.Focus()
	p := &Picker{
		id: id, title: title, sty: sty, all: entries, input: in, empty: empty,
		help: "↑↓ choose · enter select · esc close",
	}
	p.filter()
	for i, e := range p.shown {
		if e.ID == selected {
			p.cursor = i
		}
	}
	return p
}

// ID implements Dialog.
func (p *Picker) ID() string { return p.id }

// Len is how many entries pass the filter.
func (p *Picker) Len() int { return len(p.shown) }

// HandleMsg implements Dialog.
func (p *Picker) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "alt+esc", "ctrl+c":
			return Close{}
		case "up", "ctrl+p", "ctrl+k":
			p.move(-1)
			return nil
		case "down", "ctrl+n", "ctrl+j":
			p.move(1)
			return nil
		case "pgup":
			p.move(-p.rows())
			return nil
		case "pgdown":
			p.move(p.rows())
			return nil
		case "enter", "tab", "ctrl+y":
			if len(p.shown) == 0 {
				return nil
			}
			return Selected{Dialog: p.id, ID: p.shown[p.cursor].ID}
		}
		before := p.input.Value()
		var cmd tea.Cmd
		p.input, cmd = p.input.Update(msg)
		_ = cmd // the cursor is drawn by the terminal; no blink to schedule
		if p.input.Value() != before {
			p.filter()
		}
	case tea.MouseWheelMsg:
		if msg.Button == tea.MouseWheelUp {
			p.move(-1)
		} else {
			p.move(1)
		}
	case tea.PasteMsg:
		p.input, _ = p.input.Update(msg)
		p.filter()
	}
	return nil
}

// move moves the cursor, wrapping at the ends for single steps.
func (p *Picker) move(n int) {
	if len(p.shown) == 0 {
		return
	}
	c := p.cursor + n
	switch {
	case c < 0 && (n == -1 || p.cursor == 0):
		c = len(p.shown) - 1
	case c < 0:
		c = 0
	case c >= len(p.shown) && (n == 1 || p.cursor == len(p.shown)-1):
		c = 0
	case c >= len(p.shown):
		c = len(p.shown) - 1
	}
	p.cursor = c
}

func (p *Picker) filter() {
	q := strings.TrimSpace(p.input.Value())
	p.shown = p.shown[:0]
	p.cursor, p.offset = 0, 0
	if q == "" {
		for _, e := range p.all {
			p.shown = append(p.shown, shown{Entry: e})
		}
		return
	}
	// Match each entry's title and aliases; the best match of either counts.
	type cand struct {
		entry int
		title bool
	}
	var src []string
	var cands []cand
	for i, e := range p.all {
		src = append(src, e.Title)
		cands = append(cands, cand{i, true})
		for _, a := range e.Aliases {
			src = append(src, a)
			cands = append(cands, cand{i, false})
		}
	}
	seen := map[int]bool{}
	for _, m := range fuzzy.Find(q, src) {
		c := cands[m.Index]
		if seen[c.entry] {
			continue
		}
		seen[c.entry] = true
		s := shown{Entry: p.all[c.entry]}
		if c.title {
			s.matched = runePositions(s.Title, m.MatchedIndexes)
		}
		p.shown = append(p.shown, s)
	}
}

// runePositions converts byte offsets to rune positions.
func runePositions(s string, bytes []int) map[int]bool {
	want := map[int]bool{}
	for _, b := range bytes {
		want[b] = true
	}
	out := map[int]bool{}
	i := 0
	for off := range s {
		if want[off] {
			out[i] = true
		}
		i++
	}
	return out
}

func (p *Picker) size(area uv.Rectangle) (w, rows int) {
	w = max(min(maxWidth, area.Dx()-4), 20)
	h := min(maxHeight, area.Dy()-2)
	// frame (2) + title + input + blank + blank + help
	return w, max(h-7, 1)
}

// rows is how many rows the last draw showed, for paging.
func (p *Picker) rows() int { return max(p.lastRows, 1) }

// Draw implements Dialog.
func (p *Picker) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	sty := p.sty
	w, rows := p.size(area)
	p.lastRows = rows
	inner := w - sty.Dialog.Frame.GetHorizontalFrameSize()

	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	p.offset = max(min(p.offset, len(p.shown)-rows), 0)

	p.input.SetWidth(inner - 2)
	lines := []string{
		sty.Title(p.title, sty.Dialog.Title, inner),
		p.input.View(),
		"",
	}
	listW := inner - 1 // room for the scrollbar
	showInfo := p.infoFits(listW)
	var body []string
	if len(p.shown) == 0 {
		body = append(body, sty.Dialog.Empty.Render(p.empty))
	}
	end := min(p.offset+rows, len(p.shown))
	for i := p.offset; i < end; i++ {
		body = append(body, p.row(p.shown[i], i == p.cursor, listW, showInfo))
	}
	block := strings.Join(body, "\n")
	if bar := sty.Scrollbar(len(body), len(p.shown), rows, p.offset); bar != "" {
		block = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(listW).Render(block), bar)
	}
	lines = append(lines, block, "", sty.Dialog.Help.Render(ansi.Truncate(p.help, inner, styles.EllipsisGlyph)))
	view := sty.Dialog.Frame.Width(w).Render(strings.Join(lines, "\n"))

	var cur *tea.Cursor
	if c := p.input.Cursor(); c != nil {
		cur = c
		// Inside the frame: border and padding, then the title row.
		cur.X += sty.Dialog.Frame.GetBorderLeftSize() + sty.Dialog.Frame.GetPaddingLeft()
		cur.Y += sty.Dialog.Frame.GetBorderTopSize() + 1
	}
	return drawCentered(scr, area, view, cur)
}

// infoFits reports whether the info column fits for every row.
func (p *Picker) infoFits(w int) bool {
	widest := 0
	for _, e := range p.shown {
		widest = max(widest, lipgloss.Width(e.Info))
	}
	return widest > 0 && float64(widest) <= float64(w)*infoShare
}

func (p *Picker) row(e shown, selected bool, w int, showInfo bool) string {
	sty := p.sty
	rowSt, infoSt := sty.Dialog.Row, sty.Dialog.Info
	if selected {
		rowSt, infoSt = sty.Dialog.RowSelected, sty.Dialog.InfoSelected
	}
	textW := w - rowSt.GetHorizontalFrameSize()
	info := ""
	if showInfo && e.Info != "" {
		info = e.Info
	}
	titleW := textW
	if info != "" {
		titleW = textW - lipgloss.Width(info) - 1
	}
	title := ansi.Truncate(e.Title, titleW, styles.EllipsisGlyph)
	var b strings.Builder
	base := lipgloss.NewStyle().Inherit(rowSt).UnsetPadding()
	i := 0
	for _, r := range title {
		st := base
		if e.matched[i] {
			st = st.Inherit(sty.Dialog.Match).Underline(true)
		}
		b.WriteString(st.Render(string(r)))
		i++
	}
	text := b.String()
	if gap := textW - lipgloss.Width(text) - lipgloss.Width(info); info != "" && gap > 0 {
		text += base.Render(strings.Repeat(" ", gap)) + infoSt.Background(rowSt.GetBackground()).Render(info)
	}
	return rowSt.Width(w).Render(text)
}
