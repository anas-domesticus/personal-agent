package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHistory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		steps []string // "prev" or "next"
		want  []string // the editor text after each step; "-" for no change
	}{
		{name: "back through the entries", steps: []string{"prev", "prev", "prev"}, want: []string{"two", "one", "-"}},
		{name: "forward returns the draft", steps: []string{"prev", "prev", "next", "next"}, want: []string{"two", "one", "two", "draft"}},
		{name: "next without browsing does nothing", steps: []string{"next"}, want: []string{"-"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var h history
			h.reset([]string{"one", "two"})
			text := "draft"

			for i, step := range tt.steps {
				var s string
				var ok bool
				if step == "prev" {
					s, ok = h.prev(text)
				} else {
					s, ok = h.next()
				}
				if tt.want[i] == "-" {
					assert.False(t, ok, "step %d", i)
					continue
				}
				assert.True(t, ok, "step %d", i)
				assert.Equal(t, tt.want[i], s, "step %d", i)
				text = s
			}
		})
	}
}

func TestHistorySkipsRepeats(t *testing.T) {
	t.Parallel()
	var h history

	h.add("same")
	h.add("same")

	assert.Equal(t, []string{"same"}, h.entries)
}
