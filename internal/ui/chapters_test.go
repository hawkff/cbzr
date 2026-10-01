package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"cbzr/internal/book"
)

func TestChapterMenuLoadsWithoutBlocking(t *testing.T) {
	b, err := book.Open(testBookPath(t, `<ComicInfo><Pages><Page Image="0" Bookmark="First"/><Page Image="2" Bookmark="Last"/></Pages></ComicInfo>`))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	m := testModel()
	p := m.panes[0]
	p.book = b
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if cmd == nil || p.chaptersReady || !p.chaptersLoading || !strings.Contains(m.View(), "loading chapters") {
		t.Fatal("Tab did not defer loading the chapters")
	}
	if m.loadChapters(p) != nil {
		t.Fatal("a second request started another extraction")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Last")})
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if !p.chaptersReady || p.chaptersLoading || m.mode != modeMenuFilter || m.menu.filter != "Last" || len(m.menu.items) != 1 || m.menu.items[0].page != 2 {
		t.Fatalf("loaded menu = %#v", m.menu)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated, _ = updated.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if p.page != 2 || m.mode != modeRead || !strings.Contains(m.paneLines(0)[0], "[Last]") {
		t.Fatal("chapter selection did not update the page and header")
	}
	if _, cmd := m.openChapters(); cmd != nil {
		t.Fatal("reopening a cached menu started work")
	}
}

func TestChapterResultsIgnoreClosedMenusAndReplacedBooks(t *testing.T) {
	for _, action := range []string{"close menu", "replace book", "remap pane"} {
		t.Run(action, func(t *testing.T) {
			b, err := book.Open(testBookPath(t))
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			m := testModel()
			p := m.panes[0]
			p.book = b
			updated, _ := m.openChapters()
			m = updated.(Model)
			msg := chaptersMsg{target: p, book: b, chapters: []book.Chapter{{Title: "Loaded", Page: 1}}}
			switch action {
			case "close menu":
				updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
				m = updated.(Model)
			case "replace book":
				p.book = &book.Book{}
			case "remap pane":
				m.panes[0] = newPane()
			}
			updated, _ = m.Update(msg)
			m = updated.(Model)
			if action == "close menu" {
				if m.mode != modeRead || !p.chaptersReady {
					t.Fatal("late chapters reopened a closed menu or lost the cache")
				}
			} else if m.panes[0].chaptersReady || len(m.menu.items) != 0 {
				t.Fatal("stale chapters reached another book")
			}
		})
	}
}

func TestChapterMenuPreservesOutlineOrderAndDepth(t *testing.T) {
	m := testModel()
	p := m.panes[0]
	p.book = &book.Book{Title: "Outline"}
	p.page, p.chaptersReady = 1, true
	p.chapters = []book.Chapter{{Title: "Later", Page: 2}, {Title: "Earlier", Page: 0}, {Title: "Nested\x1b[31m title", Page: 1, Depth: 2}, {Title: "Appendix", Page: 0}}
	updated, _ := m.openChapters()
	m = updated.(Model)
	if m.menu.cursor != 2 || m.menu.items[0].page != 2 || !strings.HasPrefix(m.menu.items[2].label, "    Nested title") {
		t.Fatalf("outline menu = %#v", m.menu)
	}
	if !strings.Contains(m.paneLines(0)[0], "[Nested title]") {
		t.Fatal("header assumed the outline was sorted by page")
	}
}

func TestChapterLoadingErrorsAndEmptyOutlines(t *testing.T) {
	for _, err := range []error{nil, errors.New("pdftohtml is not on PATH")} {
		m := testModel()
		p := m.panes[0]
		p.book = &book.Book{}
		m.zen = true
		updated, _ := m.openChapters()
		m = updated.(Model)
		updated, _ = m.Update(chaptersMsg{target: p, book: p.book, err: err})
		m = updated.(Model)
		if m.mode != modeRead || m.zen || m.status == "" {
			t.Fatal("empty or failed outline left an empty loading menu")
		}
		if err != nil && !strings.Contains(m.status, err.Error()) {
			t.Fatalf("error = %q", m.status)
		}
	}
}
