// Package dialog draws the TUI's overlays: the command palette, the
// conversation and agent pickers, and the quit confirmation.
//
// The design follows Crush (github.com/charmbracelet/crush,
// internal/ui/dialog): dialogs sit on a stack drawn over the screen, only
// the front one gets input, and a dialog reports what the user chose as an
// Action for the main model to carry out rather than doing it itself.
package dialog

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// Action is what a dialog asks the UI to do after a message; nil means
// nothing.
type Action any

// Close asks the UI to close the front dialog.
type Close struct{}

// Selected reports the entry chosen in a picker.
type Selected struct {
	Dialog string // the picker's ID
	ID     string // the entry's ID
}

// Quit confirms quitting.
type Quit struct{}

// Dialog is an overlay.
type Dialog interface {
	// ID identifies the dialog, so it is opened at most once.
	ID() string
	// HandleMsg handles input and returns what the UI should do.
	HandleMsg(msg tea.Msg) Action
	// Draw draws the dialog within area and returns where the cursor goes,
	// if anywhere.
	Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor
}

// Overlay is the stack of open dialogs; the last is in front.
type Overlay struct {
	dialogs []Dialog
}

// Open puts d in front. A dialog already open is brought to the front
// instead of opened twice.
func (o *Overlay) Open(d Dialog) {
	o.Close(d.ID())
	o.dialogs = append(o.dialogs, d)
}

// Close closes the dialog with that ID, if open.
func (o *Overlay) Close(id string) {
	for i, d := range o.dialogs {
		if d.ID() == id {
			o.dialogs = append(o.dialogs[:i], o.dialogs[i+1:]...)
			return
		}
	}
}

// CloseFront closes the front dialog.
func (o *Overlay) CloseFront() {
	if n := len(o.dialogs); n > 0 {
		o.dialogs = o.dialogs[:n-1]
	}
}

// Any reports whether any dialog is open.
func (o *Overlay) Any() bool { return len(o.dialogs) > 0 }

// Has reports whether the dialog with that ID is open.
func (o *Overlay) Has(id string) bool {
	for _, d := range o.dialogs {
		if d.ID() == id {
			return true
		}
	}
	return false
}

// Front returns the front dialog, or nil.
func (o *Overlay) Front() Dialog {
	if n := len(o.dialogs); n > 0 {
		return o.dialogs[n-1]
	}
	return nil
}

// Update sends msg to the front dialog.
func (o *Overlay) Update(msg tea.Msg) Action {
	if d := o.Front(); d != nil {
		return d.HandleMsg(msg)
	}
	return nil
}

// Draw draws every dialog, back to front, and returns the front one's
// cursor.
func (o *Overlay) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	var cur *tea.Cursor
	for _, d := range o.dialogs {
		cur = d.Draw(scr, area)
	}
	return cur
}

// drawCentered draws view in the middle of area and moves the cursor, which
// is relative to view, along with it.
func drawCentered(scr uv.Screen, area uv.Rectangle, view string, cur *tea.Cursor) *tea.Cursor {
	w, h := lipgloss.Size(view)
	w, h = min(w, area.Dx()), min(h, area.Dy())
	x := area.Min.X + (area.Dx()-w)/2
	y := area.Min.Y + (area.Dy()-h)/2
	uv.NewStyledString(view).Draw(scr, uv.Rect(x, y, w, h))
	if cur != nil {
		cur.X += x
		cur.Y += y
	}
	return cur
}
