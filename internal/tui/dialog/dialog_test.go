package dialog

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anas-domesticus/personal-agent/internal/tui/styles"
)

var entries = []Entry{
	{ID: "new", Title: "New Conversation", Aliases: []string{"clear"}},
	{ID: "sessions", Title: "Conversations"},
	{ID: "quit", Title: "Quit", Aliases: []string{"exit"}},
}

func keys(p *Picker, s string) {
	for _, r := range s {
		p.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestPickerChoose(t *testing.T) {
	t.Parallel()
	down := tea.KeyPressMsg{Code: tea.KeyDown}
	up := tea.KeyPressMsg{Code: tea.KeyUp}

	tests := []struct {
		name     string
		selected string
		filter   string
		moves    []tea.KeyPressMsg
		want     string // the ID chosen; "" for none
	}{
		{name: "first by default", want: "new"},
		{name: "starts on the selected entry", selected: "quit", want: "quit"},
		{name: "down moves", moves: []tea.KeyPressMsg{down}, want: "sessions"},
		{name: "up from the top wraps", moves: []tea.KeyPressMsg{up}, want: "quit"},
		{name: "filter by title", filter: "conv", want: "sessions"},
		{name: "filter by alias", filter: "exit", want: "quit"},
		{name: "nothing matches", filter: "zzz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := NewPicker(styles.New(true), "commands", "Commands", entries, tt.selected, "none")
			keys(p, tt.filter)
			for _, k := range tt.moves {
				p.HandleMsg(k)
			}

			got := p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

			if tt.want == "" {
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, Selected{Dialog: "commands", ID: tt.want}, got)
		})
	}
}

func TestPickerEscCloses(t *testing.T) {
	t.Parallel()
	p := NewPicker(styles.New(true), "x", "X", entries, "", "none")

	assert.Equal(t, Close{}, p.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

func TestOverlay(t *testing.T) {
	t.Parallel()
	sty := styles.New(true)
	var o Overlay
	a := NewPicker(sty, "a", "A", entries, "", "")
	b := NewPicker(sty, "b", "B", entries, "", "")

	o.Open(a)
	o.Open(b)
	o.Open(a) // already open: brought to the front, not opened twice

	require.Equal(t, a, o.Front())
	o.CloseFront()
	assert.Equal(t, b, o.Front())
	assert.False(t, o.Has("a"))
	o.Close("b")
	assert.False(t, o.Any())
}

func TestQuit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys []tea.KeyPressMsg
		want Action
	}{
		{name: "y quits", keys: []tea.KeyPressMsg{{Code: 'y', Text: "y"}}, want: Quit{}},
		{name: "enter on Yep quits", keys: []tea.KeyPressMsg{{Code: tea.KeyEnter}}, want: Quit{}},
		{name: "tab then enter is Nope", keys: []tea.KeyPressMsg{{Code: tea.KeyTab}, {Code: tea.KeyEnter}}, want: Close{}},
		{name: "esc closes", keys: []tea.KeyPressMsg{{Code: tea.KeyEscape}}, want: Close{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := NewQuit(styles.New(true), "")
			var got Action
			for _, k := range tt.keys {
				got = q.HandleMsg(k)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPickerDrawsInSmallTerminals(t *testing.T) {
	t.Parallel()
	p := NewPicker(styles.New(true), "x", "A long title for a narrow screen", entries, "", "none")
	scr := uv.NewScreenBuffer(30, 10)

	assert.NotPanics(t, func() { p.Draw(scr, scr.Bounds()) })
}
