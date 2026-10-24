package ui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"cbzr/internal/book"
)

func TestObsoleteRendersStopBeforeLoading(t *testing.T) {
	for _, action := range []string{"turn", "resize", "replace", "close", "native"} {
		t.Run(action, func(t *testing.T) {
			m := testModel()
			b, err := book.Open(testBookPath(t))
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			m.panes[0].book = b
			m.spread = true
			r := &recordingRenderer{Renderer: m.renderer}
			m.renderer = r
			old := m.renderPane(0)().(tea.BatchMsg)
			var updated tea.Model
			switch action {
			case "turn":
				updated, _ = m.goTo(0, 1)
			case "resize":
				updated, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
			case "replace":
				updated, _ = m.openBook(0, testBookPath(t), nil)
				defer m.panes[0].book.Close()
			case "close":
				updated, _ = m.closePane(0)
			case "native":
				m.nativeAvailable = true
				updated, _ = m.updateRead(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
			}
			m = updated.(Model)
			for _, cmd := range old {
				msg := cmd().(renderedMsg)
				if !errors.Is(msg.err, context.Canceled) {
					t.Fatalf("obsolete slot %d: %v", msg.slot, msg.err)
				}
			}
			if len(r.images) != 0 {
				t.Fatal("obsolete render reached the renderer")
			}
			if action == "turn" || action == "resize" || action == "replace" {
				for _, cmd := range m.renderPane(0)().(tea.BatchMsg) {
					if msg := cmd().(renderedMsg); msg.err != nil {
						t.Fatalf("replacement render: %v", msg.err)
					}
				}
				if len(r.images) != 2 {
					t.Fatal("replacement spread did not render both pages")
				}
			}
		})
	}
}

func TestRerenderDoesNotCancelOtherPane(t *testing.T) {
	m := testModel()
	m.split = true
	b, err := book.Open(testBookPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	m.panes[0].book, m.panes[1].book = b, b
	left := m.renderPane(0)
	right := m.renderPane(1)
	m.renderPane(0)
	if msg := left().(renderedMsg); !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("old left pane: %v", msg.err)
	}
	if msg := right().(renderedMsg); msg.err != nil {
		t.Fatalf("unchanged right pane: %v", msg.err)
	}
}
