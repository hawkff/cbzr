package ui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
			var next tea.Cmd
			switch action {
			case "turn":
				updated, next = m.goTo(0, 1)
			case "resize":
				updated, next = m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
			case "replace":
				updated, next = m.openBook(0, testBookPath(t), nil)
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
				if next == nil {
					t.Fatal("action did not schedule its replacement render")
				}
				pending := []tea.Cmd{next}
				for len(pending) > 0 {
					cmd := pending[0]
					pending = pending[1:]
					switch msg := cmd().(type) {
					case tea.BatchMsg:
						pending = append(pending, msg...)
					case renderedMsg:
						if msg.err != nil {
							t.Fatalf("replacement render: %v", msg.err)
						}
					}
				}
				if len(r.images) != 2 {
					t.Fatal("replacement spread did not render both pages")
				}
			}
		})
	}
}

func TestPageTurnCancelsRunningConverter(t *testing.T) {
	path := testPDFPath(t)
	converter, err := exec.LookPath("pdftoppm")
	if err != nil {
		t.Fatal(err)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("slow converter helper needs sh")
	}
	dir := t.TempDir()
	started, release := filepath.Join(dir, "started"), filepath.Join(dir, "release")
	t.Setenv("CBZR_TEST_STARTED", started)
	t.Setenv("CBZR_TEST_RELEASE", release)
	t.Setenv("CBZR_TEST_CONVERTER", converter)
	script := "#!" + sh + "\nprevious=; page=\n" +
		"for arg in \"$@\"; do if [ \"$previous\" = -f ]; then page=\"$arg\"; fi; previous=\"$arg\"; done\n" +
		"if [ \"$page\" = 1 ]; then\n" +
		"printf started > \"$CBZR_TEST_STARTED\"\n" +
		"while [ ! -e \"$CBZR_TEST_RELEASE\" ]; do sleep 0.01; done\nfi\n" +
		"exec \"$CBZR_TEST_CONVERTER\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pdftoppm"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	b, err := book.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.panes[0].book = b
	done := make(chan renderedMsg, 2)
	var work sync.WaitGroup
	t.Cleanup(func() {
		os.WriteFile(release, nil, 0o600)
		b.Close()
		work.Wait()
	})
	start := func(cmd tea.Cmd) {
		work.Go(func() { done <- cmd().(renderedMsg) })
	}
	start(m.renderPane(0))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("converter did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	updated, next := m.goTo(0, 1)
	m = updated.(Model)
	select {
	case old := <-done:
		if !errors.Is(old.err, context.Canceled) {
			t.Fatalf("running converter was not canceled: %v", old.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("page turn left the obsolete converter running")
	}
	if next == nil {
		t.Fatal("page turn did not schedule its replacement")
	}
	start(next)
	select {
	case msg := <-done:
		if msg.err != nil || msg.page != 1 || msg.res.Rows == 0 {
			t.Fatalf("replacement page: %d, %v", msg.page, msg.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("replacement render did not finish")
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
