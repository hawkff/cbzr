package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"cbzr/internal/book"
)

type chaptersMsg struct {
	target   *pane
	book     *book.Book
	chapters []book.Chapter
	err      error
}

func (m Model) loadChapters(p *pane) tea.Cmd {
	if p.book == nil || p.chaptersReady || p.chaptersLoading {
		return nil
	}
	p.chaptersLoading = true
	b := p.book
	return func() tea.Msg {
		chapters, err := b.Chapters()
		return chaptersMsg{target: p, book: b, chapters: chapters, err: err}
	}
}

func (m Model) updateChapters(msg chaptersMsg) (tea.Model, tea.Cmd) {
	for _, p := range m.panes[:m.paneCount()] {
		if p != msg.target || p.book != msg.book {
			continue
		}
		p.chapters, p.chapterErr = msg.chapters, msg.err
		p.chaptersReady, p.chaptersLoading = true, false
		if p == m.panes[m.active] && m.menu.kind == menuChapters && (m.mode == modeMenu || m.mode == modeMenuFilter) {
			filter, mode := m.menu.filter, m.mode
			updated, cmd := m.openChapters()
			m = updated.(Model)
			if m.mode == modeMenu {
				m.menu.filter = filter
				m.menu.applyFilter()
				m.menu.move(0, m.menuHeight())
				m.mode = mode
			}
			return m, cmd
		}
		break
	}
	return m, nil
}
