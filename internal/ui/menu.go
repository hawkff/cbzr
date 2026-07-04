package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// menuItem is one row in a menu overlay.
type menuItem struct {
	label string
	page  int    // target page
	path  string // book path (bookmarks menu); empty = active pane's book
}

// menu is a modal list with vim navigation.
type menu struct {
	title  string
	items  []menuItem
	cursor int
	top    int
}

func (mn *menu) move(delta, height int) {
	if len(mn.items) == 0 {
		return
	}
	mn.cursor = max(0, min(len(mn.items)-1, mn.cursor+delta))
	if mn.cursor < mn.top {
		mn.top = mn.cursor
	}
	if mn.cursor >= mn.top+height {
		mn.top = mn.cursor - height + 1
	}
}

func (mn *menu) view(width, height int) string {
	rows := max(1, height-3)
	var sb strings.Builder
	sb.WriteString(titleActive.Render(" " + mn.title))
	sb.WriteString("\n")
	if len(mn.items) == 0 {
		sb.WriteString(dim.Render("  (empty)"))
	}
	end := min(len(mn.items), mn.top+rows)
	for i := mn.top; i < end; i++ {
		line := fmt.Sprintf("  %s", mn.items[i].label)
		line = ansi.Truncate(line, width-2, "…")
		if i == mn.cursor {
			line = titleActive.Render("> " + strings.TrimPrefix(line, "  "))
		}
		sb.WriteString(line)
		if i < end-1 {
			sb.WriteString("\n")
		}
	}
	body := sb.String()
	hint := dim.Render(" j/k move · enter go · esc close")
	return lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, body+"\n\n"+hint)
}
