package tui

import "charm.land/bubbles/v2/key"

// keyMap is every binding outside dialogs, after Crush's: global keys work
// everywhere, editor keys while typing, chat keys while the transcript has
// focus (tab switches between them).
type keyMap struct {
	// Global.
	Quit, Help, Commands, Agents, Sessions, New key.Binding
	Sidebar, Details, Suspend, Tab, Cancel      key.Binding

	Editor struct {
		Send, Newline, OpenEditor key.Binding
		HistoryPrev, HistoryNext  key.Binding
	}

	Chat struct {
		Up, Down, PrevItem, NextItem           key.Binding
		HalfUp, HalfDown, PageUp, PageDown     key.Binding
		Top, Bottom, Expand, Copy, ClearSelect key.Binding
	}
}

func newKeyMap() keyMap {
	b := func(keys []string, k, desc string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(k, desc))
	}
	var km keyMap
	km.Quit = b([]string{"ctrl+c"}, "ctrl+c", "quit")
	km.Help = b([]string{"ctrl+g"}, "ctrl+g", "more")
	km.Commands = b([]string{"ctrl+p"}, "ctrl+p", "commands")
	km.Agents = b([]string{"ctrl+l"}, "ctrl+l", "agents")
	km.Sessions = b([]string{"ctrl+s"}, "ctrl+s", "conversations")
	km.New = b([]string{"ctrl+n"}, "ctrl+n", "new")
	km.Sidebar = b([]string{"ctrl+b"}, "ctrl+b", "sidebar")
	km.Details = b([]string{"ctrl+d"}, "ctrl+d", "details")
	km.Suspend = b([]string{"ctrl+z"}, "ctrl+z", "suspend")
	km.Tab = b([]string{"tab"}, "tab", "focus chat")
	km.Cancel = b([]string{"esc", "alt+esc"}, "esc", "interrupt")

	km.Editor.Send = b([]string{"enter"}, "enter", "send")
	km.Editor.Newline = b([]string{"shift+enter", "alt+enter", "ctrl+j"}, "shift+enter", "newline")
	km.Editor.OpenEditor = b([]string{"ctrl+o"}, "ctrl+o", "open editor")
	km.Editor.HistoryPrev = b([]string{"up"}, "↑", "history")
	km.Editor.HistoryNext = b([]string{"down"}, "↓", "history")

	c := &km.Chat
	c.Up = b([]string{"up", "k", "ctrl+k"}, "↑/k", "scroll up")
	c.Down = b([]string{"down", "j", "ctrl+j"}, "↓/j", "scroll down")
	c.PrevItem = b([]string{"shift+up", "K"}, "shift+↑", "previous item")
	c.NextItem = b([]string{"shift+down", "J"}, "shift+↓", "next item")
	c.HalfUp = b([]string{"u"}, "u", "half page up")
	c.HalfDown = b([]string{"d"}, "d", "half page down")
	c.PageUp = b([]string{"pgup", "b"}, "b/pgup", "page up")
	c.PageDown = b([]string{"pgdown", "f"}, "f/pgdn", "page down")
	c.Top = b([]string{"g", "home"}, "g", "top")
	c.Bottom = b([]string{"G", "end", "ctrl+end"}, "G", "bottom")
	c.Expand = b([]string{"space"}, "space", "expand")
	c.Copy = b([]string{"c", "y", "C", "Y"}, "c/y", "copy")
	c.ClearSelect = b([]string{"esc", "alt+esc"}, "esc", "clear selection")
	return km
}
