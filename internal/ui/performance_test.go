package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"cbzr/internal/book"
	"cbzr/internal/render"
)

func TestChromeChangesKeepImageTransmissionLineStable(t *testing.T) {
	for _, layout := range []string{"single", "split", "spread", "zen"} {
		t.Run(layout, func(t *testing.T) {
			m := testModel()
			m.split, m.spread, m.zen = layout == "split", layout == "spread", layout == "zen"
			for _, p := range m.panes[:m.paneCount()] {
				p.book = &book.Book{Title: "Synthetic"}
				p.res = render.Result{Cols: 1, Rows: 1, Lines: []string{"x"}, Transmit: []byte("\x1b_Gpayload\x1b\\")}
				if m.spread {
					p.res2 = p.res
				}
			}
			before := strings.Split(m.View(), "\n")
			for _, p := range m.panes {
				p.loading = true
			}
			m.status, m.hover = "copied text", "https://example.com/"
			m.clip = []byte("\x1b]52;c;eA==\a")
			after := strings.Split(m.View(), "\n")
			if len(before) != len(after) {
				t.Fatal("chrome update changed frame height")
			}
			found := false
			for i, line := range before {
				if strings.Contains(line, "\x1b_G") {
					found = true
					if line != after[i] {
						t.Fatal("chrome update would replay the image upload")
					}
				}
			}
			if !found {
				t.Fatal("frame lost its image transmission")
			}
			m.mode = modeHelp
			if strings.Contains(m.View(), "\x1b_G") {
				t.Fatal("help unexpectedly uploaded an image")
			}
			m.mode = modeRead
			if !strings.Contains(m.View(), "\x1b_G") {
				t.Fatal("leaving help lost the image transmission")
			}
		})
	}
}

func TestPDFTextActionsWaitWithoutBlockingInput(t *testing.T) {
	path := testPDFPath(t)
	for _, action := range []string{"select", "follow", "copy link"} {
		t.Run(action, func(t *testing.T) {
			b, err := book.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			m := testModel()
			p := m.panes[0]
			p.book = b
			opened := ""
			m.openBrowser = func(url string) error { opened = url; return nil }
			var sel *selection
			if action == "select" {
				sel = &selection{page: 0, x1: 1, y1: 1}
				p.sel = sel
			}
			h := hit{pane: 0, page: 0, x: 0.1, y: 0.5}
			updated, cmd := m.textAction(h, sel, action == "copy link")
			m = updated.(Model)
			if cmd == nil || b.TextReady(0) || m.status != "loading text…" {
				t.Fatal("cold PDF action did not defer its extraction")
			}
			if sel != nil {
				sel.x1 = 0 // a later frame cannot change the queued selection
			}
			msg := cmd().(textActionMsg)
			p.gen++ // finishing a render must not cancel the queued input
			updated, _ = m.Update(msg)
			m = updated.(Model)
			switch action {
			case "follow":
				if opened != "https://example.com/read" {
					t.Fatalf("queued link opened %q", opened)
				}
			case "select":
				if got := clipboardText(t, m.View()); got != "Hello reader" {
					t.Fatalf("queued selection copied %q", got)
				}
			case "copy link":
				if got := clipboardText(t, m.View()); got != "https://example.com/read" {
					t.Fatalf("queued link copied %q", got)
				}
			}
			opened, m.clip = "", nil
			updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = updated.(Model)
			updated, _ = m.Update(msg)
			m = updated.(Model)
			if opened != "" || m.clip != nil {
				t.Fatal("newer input did not cancel a deferred action")
			}
		})
	}
}

func TestFilterMouseSkipsUnchangedHoverOnly(t *testing.T) {
	m := testModel()
	motion := tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonNone}
	if FilterMouse(m, motion) != nil {
		t.Fatal("unchanged hover should not rebuild the view")
	}
	m.hover = "https://example.com/"
	msg := FilterMouse(m, motion)
	if msg != hoverMsg("") {
		t.Fatal("leaving a link did not clear its label")
	}
	updated, _ := m.Update(msg)
	if updated.(Model).hover != "" {
		t.Fatal("hover update was ignored")
	}
	for _, msg := range []tea.Msg{
		tea.MouseMsg{Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft},
		tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown},
		tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft},
	} {
		if FilterMouse(m, msg) != msg {
			t.Fatal("filter dropped selection or scrolling input")
		}
	}
	m.mode = modeHelp
	if FilterMouse(m, motion) != nil {
		t.Fatal("hovering over help rebuilt the view")
	}
}
