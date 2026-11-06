package tui

// history is the prompts sent, browsed with ↑ and ↓ in the editor. The text
// being written when browsing starts is kept as a draft and comes back
// after the newest entry.
type history struct {
	entries []string
	pos     int // len(entries) when not browsing
	draft   string
}

// reset replaces the entries, e.g. with a conversation's messages.
func (h *history) reset(entries []string) {
	h.entries = append([]string(nil), entries...)
	h.pos, h.draft = len(h.entries), ""
}

// add records a sent prompt, skipping a repeat of the last one.
func (h *history) add(s string) {
	if n := len(h.entries); n == 0 || h.entries[n-1] != s {
		h.entries = append(h.entries, s)
	}
	h.pos, h.draft = len(h.entries), ""
}

func (h *history) browsing() bool { return h.pos < len(h.entries) }

// prev returns the previous entry, given the editor's current text.
func (h *history) prev(current string) (string, bool) {
	if h.pos == 0 {
		return "", false
	}
	if !h.browsing() {
		h.draft = current
	}
	h.pos--
	return h.entries[h.pos], true
}

// next returns the next entry, or the draft after the newest.
func (h *history) next() (string, bool) {
	if !h.browsing() {
		return "", false
	}
	h.pos++
	if h.pos == len(h.entries) {
		return h.draft, true
	}
	return h.entries[h.pos], true
}

// leave stops browsing, keeping whatever is in the editor.
func (h *history) leave() { h.pos, h.draft = len(h.entries), "" }
