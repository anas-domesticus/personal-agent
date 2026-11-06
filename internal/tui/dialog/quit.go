package dialog

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/anas-domesticus/personal-agent/internal/tui/styles"
)

// QuitID identifies the quit dialog.
const QuitID = "quit"

// QuitDialog asks before quitting.
type QuitDialog struct {
	sty  *styles.Styles
	note string // e.g. that a turn is still running
	yes  bool   // the selected button
}

// NewQuit returns the quit dialog. note, if set, is shown under the
// question.
func NewQuit(sty *styles.Styles, note string) *QuitDialog {
	return &QuitDialog{sty: sty, note: note, yes: true}
}

// ID implements Dialog.
func (q *QuitDialog) ID() string { return QuitID }

// HandleMsg implements Dialog.
func (q *QuitDialog) HandleMsg(msg tea.Msg) Action {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "y", "Y", "ctrl+c":
		return Quit{}
	case "n", "N", "esc", "alt+esc":
		return Close{}
	case "left", "right", "tab", "shift+tab", "h", "l":
		q.yes = !q.yes
	case "enter", "space":
		if q.yes {
			return Quit{}
		}
		return Close{}
	}
	return nil
}

// Draw implements Dialog.
func (q *QuitDialog) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	sty := q.sty
	yes, no := sty.Dialog.Button, sty.Dialog.ButtonSelected
	if q.yes {
		yes, no = no, yes
	}
	buttons := lipgloss.JoinHorizontal(lipgloss.Top, yes.Render("Yep!"), "  ", no.Render("Nope"))
	lines := []string{sty.Base.Render("Are you sure you want to quit?")}
	if q.note != "" {
		lines = append(lines, sty.MoreSubtle.Render(q.note))
	}
	body := strings.Join(lines, "\n")
	w := max(lipgloss.Width(body), lipgloss.Width(buttons))
	view := sty.Dialog.Frame.Padding(1, 3).Render(lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.PlaceHorizontal(w, lipgloss.Center, body), "", buttons))
	return drawCentered(scr, area, view, nil)
}
