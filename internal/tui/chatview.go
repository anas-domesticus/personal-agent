package tui

import (
	"github.com/anas-domesticus/personal-agent/internal/tui/chat"
	"github.com/anas-domesticus/personal-agent/internal/tui/list"
)

// chatView shows the transcript in a lazily rendered list. It follows new
// output while scrolled to the bottom and stops when the user scrolls up,
// as Crush's does.
type chatView struct {
	list   *list.List
	tr     *chat.Transcript
	shown  []chat.Item // what the list holds, to sync with the transcript
	follow bool
}

func newChatView(tr *chat.Transcript) *chatView {
	l := list.NewList()
	l.SetGap(1)
	c := &chatView{list: l, follow: true}
	l.RegisterRenderCallback(list.FocusedRenderCallback(l))
	c.setTranscript(tr)
	return c
}

// setTranscript shows a different transcript, scrolled to the bottom.
func (c *chatView) setTranscript(tr *chat.Transcript) {
	c.tr, c.shown = tr, nil
	c.list.SetItems()
	c.list.SetSelected(-1)
	c.follow = true
	c.sync()
}

// sync brings the list in line with the transcript. Items are only ever
// appended or dropped from the end, so the scroll position is kept.
func (c *chatView) sync() {
	items := c.tr.Items()
	common := 0
	for common < len(items) && common < len(c.shown) && items[common] == c.shown[common] {
		common++
	}
	for i := len(c.shown) - 1; i >= common; i-- {
		c.list.RemoveItem(i)
	}
	for _, it := range items[common:] {
		c.list.AppendItems(it)
	}
	c.shown = append(c.shown[:0], items...)
	// Dropping the selected item (a finished placeholder) clears the
	// selection; keep one while the transcript has focus.
	if c.list.Focused() && c.list.Selected() < 0 {
		c.list.SelectLast()
	}
	if c.follow {
		c.list.ScrollToBottom()
	}
}

func (c *chatView) setSize(w, h int) {
	c.list.SetSize(w, h)
	if c.follow {
		c.list.ScrollToBottom()
	}
}

// scroll moves the view by n lines; scrolling up stops following.
func (c *chatView) scroll(n int) {
	c.list.ScrollBy(n)
	c.follow = c.list.AtBottom()
	c.keepSelectionInView()
}

func (c *chatView) top() {
	c.list.ScrollToTop()
	c.follow = c.list.AtBottom()
	if c.list.Focused() {
		c.list.SelectFirst()
	}
}

func (c *chatView) bottom() {
	c.list.ScrollToBottom()
	c.follow = true
	if c.list.Focused() {
		c.list.SelectLast()
	}
}

// keepSelectionInView moves the selection onto the screen when scrolling
// takes it off.
func (c *chatView) keepSelectionInView() {
	if !c.list.Focused() || c.list.Selected() < 0 || c.list.SelectedItemInView() {
		return
	}
	start, _ := c.list.VisibleItemIndices()
	if c.list.Selected() < start {
		c.list.SelectFirstInView()
	} else {
		c.list.SelectLastInView()
	}
}

// selectBy moves the selection by one item and scrolls to it.
func (c *chatView) selectBy(n int) {
	switch {
	case c.list.Selected() < 0:
		c.list.SelectLastInView()
	case n < 0:
		c.list.SelectPrev()
	default:
		c.list.SelectNext()
	}
	c.list.ScrollToSelected()
	c.follow = c.list.AtBottom() && c.list.IsSelectedLast()
}

// focus gives the transcript focus, selecting the last item in view.
func (c *chatView) focus() {
	c.list.Focus()
	if c.list.Selected() < 0 || !c.list.SelectedItemInView() {
		c.list.SelectLastInView()
	}
}

func (c *chatView) blur() { c.list.Blur() }

// selected returns the selected item, if the transcript has focus.
func (c *chatView) selected() chat.Item {
	if !c.list.Focused() {
		return nil
	}
	it, _ := c.list.SelectedItem().(chat.Item)
	return it
}

// itemAt returns the index of the item at row y of the view, or -1.
func (c *chatView) itemAt(y int) int {
	i, _ := c.list.ItemIndexAtPosition(0, y)
	return i
}

// spinning reports whether any item is animating.
func (c *chatView) spinning() bool {
	for _, it := range c.tr.Items() {
		if a, ok := it.(chat.Animatable); ok && a.Spinning() {
			return true
		}
	}
	return false
}

// advance steps every spinner one frame.
func (c *chatView) advance() {
	for _, it := range c.tr.Items() {
		if a, ok := it.(chat.Animatable); ok && a.Spinning() {
			a.Advance()
		}
	}
	if c.follow {
		c.list.ScrollToBottom()
	}
}

func (c *chatView) render() string { return c.list.Render() }
