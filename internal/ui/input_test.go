package ui

import (
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBackspaceRemovesWholeRune(t *testing.T) {
	for _, mode := range []mode{modeSearch, modeMenuRename, modeMenuFilter} {
		m := testModel()
		m.mode = mode
		m.input, m.menu.filter = "aé漢🙂", "aé漢🙂"
		m.menu.all = []menuItem{{label: "aé漢 chapter"}, {label: "other"}}
		for _, want := range []string{"aé漢", "aé", "a", "", ""} {
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
			m = updated.(Model)
			got := m.input
			if mode == modeMenuFilter {
				got = m.menu.filter
				if want != "" && len(m.menu.items) != 1 {
					t.Fatalf("filter %q lost its matching row", got)
				}
			}
			if got != want || !utf8.ValidString(got) {
				t.Fatalf("mode %d: backspace = %q, want %q", mode, got, want)
			}
		}
	}
}
