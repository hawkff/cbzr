package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type menuKind int

const (
	menuChapters menuKind = iota
	menuBookmarks
)

// menuItem is one row in a menu overlay.
type menuItem struct {
	label string
	page  int    // target page
	path  string // book path (bookmarks menu); empty = active pane's book
}

// menu is a modal list with vim navigation and fuzzy filtering.
type menu struct {
	kind   menuKind
	title  string
	all    []menuItem // unfiltered
	items  []menuItem // filtered view
	filter string
	cursor int
	top    int
}

// fuzzyScore matches query as a case-insensitive subsequence of s.
// Lower scores rank higher: compact matches first, earlier matches next.
func fuzzyScore(query, s string) (int, bool) {
	q, t := strings.ToLower(query), strings.ToLower(s)
	if q == "" {
		return 0, true
	}
	first, last, qi := -1, -1, 0
	for i := 0; i < len(t) && qi < len(q); i++ {
		if t[i] == q[qi] {
			if first < 0 {
				first = i
			}
			last = i
			qi++
		}
	}
	if qi < len(q) {
		return 0, false
	}
	return (last-first-len(q)+1)*4 + first, true
}

func (mn *menu) applyFilter() {
	if mn.filter == "" {
		mn.items = mn.all
	} else {
		type scored struct {
			it menuItem
			sc int
		}
		var out []scored
		for _, it := range mn.all {
			if sc, ok := fuzzyScore(mn.filter, it.label); ok {
				out = append(out, scored{it, sc})
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].sc < out[j].sc })
		mn.items = make([]menuItem, len(out))
		for i, s := range out {
			mn.items[i] = s.it
		}
	}
	mn.cursor = max(0, min(mn.cursor, len(mn.items)-1))
	mn.top = max(0, min(mn.top, mn.cursor))
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

func (mn *menu) selected() (menuItem, bool) {
	if len(mn.items) == 0 || mn.cursor >= len(mn.items) {
		return menuItem{}, false
	}
	return mn.items[mn.cursor], true
}

func (mn *menu) view(width, height int, footer string) string {
	rows := max(1, height-3)
	var sb strings.Builder
	head := " " + mn.title
	if mn.filter != "" {
		head += dim.Render(fmt.Sprintf("  (%d/%d)", len(mn.items), len(mn.all)))
	}
	sb.WriteString(titleActive.Render(head))
	sb.WriteString("\n")
	if len(mn.items) == 0 {
		sb.WriteString(dim.Render("  (no matches)"))
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
	if footer == "" {
		switch mn.kind {
		case menuBookmarks:
			footer = dim.Render(" j/k move · enter go · / filter · r rename · d delete · esc close")
		default:
			footer = dim.Render(" j/k move · enter go · / filter · esc close")
		}
	}
	return lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, sb.String()+"\n\n"+footer)
}
