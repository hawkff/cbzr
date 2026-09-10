package ui

import (
	"archive/zip"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"cbzr/internal/book"
	"cbzr/internal/bookmarks"
	"cbzr/internal/progress"
	"cbzr/internal/render"
	"cbzr/internal/server"
)

func TestStatusViewKeepsOriginalShortcutTips(t *testing.T) {
	m := testModel()
	m.width = 200
	want := "j/k page e in-browser s spread R rotate tab chapters  ? help  q quit "
	if !strings.Contains(m.statusView(), want) {
		t.Fatal("bottom shortcut tips changed")
	}
}

func TestPaneHeaderShowsCurrentChapter(t *testing.T) {
	metadata := `<ComicInfo><Pages><Page Image="1" Bookmark="First chapter"/><Page Image="2" Bookmark="Second&#xA; chapter"/></Pages></ComicInfo>`
	for _, tc := range []struct {
		name, metadata, chapter string
		page                    int
		spread                  bool
	}{
		{"before first chapter", metadata, "", 0, false},
		{"first chapter", metadata, "First chapter", 1, false},
		{"next chapter", metadata, "Second chapter", 2, false},
		{"spread uses left page", metadata, "First chapter", 1, true},
		{"no metadata", "", "", 1, false},
		{"invalid metadata", "<ComicInfo>", "", 1, false},
		{"blank chapter", `<ComicInfo><Pages><Page Image="0" Bookmark=" &#x9; "/></Pages></ComicInfo>`, "", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var metadata []string
			if tc.metadata != "" {
				metadata = append(metadata, tc.metadata)
			}
			b, err := book.Open(testBookPath(t, metadata...))
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			m := testModel()
			m.width, m.spread = 200, tc.spread
			p := m.panes[0]
			p.book, p.page = b, tc.page
			for _, inverted := range []bool{false, true} {
				p.inverted = inverted
				header := m.paneLines(0)[0]
				if strings.Contains(header, "inverted") || strings.ContainsAny(header, "\n\t") {
					t.Fatalf("unexpected header: %q", header)
				}
				if tc.chapter == "" {
					if strings.Contains(header, "chapter") || strings.Contains(header, "  []") {
						t.Fatalf("missing chapter produced a label: %q", header)
					}
				} else if !strings.Contains(header, "["+tc.chapter+"]") {
					t.Fatalf("header %q lacks chapter %q", header, tc.chapter)
				}
			}
			p.rot, p.zoom = 1, 1.5
			if header := m.paneLines(0)[0]; !strings.Contains(header, "90° 1.5x") {
				t.Fatalf("chapter label lost view modifiers: %q", header)
			}
		})
	}
}

func TestQuitSavePolicy(t *testing.T) {
	plain, _ := (Model{}).updateRead(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if plain.(Model).SaveOnQuit() {
		t.Fatal("q must not request a saved exit")
	}

	saved, _ := (Model{}).updateRead(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Q'}})
	if !saved.(Model).SaveOnQuit() {
		t.Fatal("Q must request saved progress")
	}
}

func TestZenModeUsesTerminalChromeRows(t *testing.T) {
	model := Model{width: 80, height: 24, panes: [2]*pane{newPane(), newPane()}}
	updated, _ := model.updateRead(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	got := updated.(Model)
	cols, rows := got.imgBox(0)
	if !got.zen || cols != 80 || rows != 24 {
		t.Fatalf("zen image box = %dx%d, zen=%v; want 80x24, true", cols, rows, got.zen)
	}
}

func TestWebtoonScrollDistances(t *testing.T) {
	if got := webtoonScrollDistance(41, 1); got != 41 {
		t.Fatalf("full webtoon scroll distance = %v, want 41", got)
	}
	if got := webtoonScrollDistance(41, 0.5); got != 20.5 {
		t.Fatalf("half webtoon scroll distance = %v, want 20.5", got)
	}
	if got := webtoonScrollDistance(1, 0.5); got != 1 {
		t.Fatalf("small viewport scroll distance = %v, want 1", got)
	}
}

func TestWebtoonKeyboardScrollBindings(t *testing.T) {
	newModel := func() Model {
		model := Model{webtoon: true, width: 80, height: 41, panes: [2]*pane{newPane(), newPane()}}
		model.panes[0].book = &book.Book{}
		model.panes[0].loading = true
		return model
	}
	tests := []struct {
		name string
		keys []rune
		want float64
	}{
		{name: "j full viewport", keys: []rune{'j'}, want: 38},
		{name: "k full viewport up", keys: []rune{'k'}, want: -38},
		{name: "J half viewport", keys: []rune{'J'}, want: 19},
		{name: "K half viewport up", keys: []rune{'K'}, want: -19},
		{name: "counted full viewports", keys: []rune{'2', 'j'}, want: 76},
		{name: "counted half viewports", keys: []rune{'3', 'K'}, want: -57},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := newModel()
			for _, key := range tt.keys {
				updated, _ := model.updateRead(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
				model = updated.(Model)
			}
			if got := model.panes[0].webScroll; got != tt.want {
				t.Fatalf("webtoon scroll = %v rows, want %v", got, tt.want)
			}
		})
	}
}

func TestWebtoonMouseWheelRemainsPrecise(t *testing.T) {
	model := Model{webtoon: true, width: 80, height: 40, panes: [2]*pane{newPane(), newPane()}}
	model.panes[0].book = &book.Book{}
	model.panes[0].loading = true

	updated, _ := model.updateMouse(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	got := updated.(Model)
	if got.panes[0].webScroll != 1 {
		t.Fatalf("wheel scroll = %v rows, want 1", got.panes[0].webScroll)
	}
}

func TestSmoothWebtoonScrollMovesViewportPromptly(t *testing.T) {
	if got := smoothWebtoonScroll(20); got != 3 {
		t.Fatalf("smooth scroll step = %v, want capped step 3", got)
	}
	if got := smoothWebtoonScroll(-20); got != -3 {
		t.Fatalf("smooth reverse step = %v, want capped step -3", got)
	}
}

func TestNativeUnavailableStaysInTerminal(t *testing.T) {
	model := Model{panes: [2]*pane{newPane(), newPane()}}
	model.panes[0].book = &book.Book{Path: "/tmp/comic.cbz"}

	updated, cmd := model.updateRead(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	got := updated.(Model)
	if got.NativeRequested() || cmd != nil {
		t.Fatal("f must leave the terminal reader running when native mode is unavailable")
	}
}

func TestApplyNativeStateReturnsToTerminal(t *testing.T) {
	model := Model{panes: [2]*pane{newPane(), newPane()}, nativeRequested: true}
	state := NativeState{Page: 7, Offset: 0.4, Webtoon: true, Rotation: 1, Zoom: 1.25, CenterX: 0.4, CenterY: 0.6, Inverted: true}
	got := model.ApplyNativeState(state)

	if got.NativeRequested() {
		t.Fatal("returned native state must resume the terminal reader")
	}
	pane := got.panes[0]
	if pane.page != 7 || pane.webOffset != 0.4 || !got.webtoon || pane.rot != 1 || pane.zoom != 1.25 || pane.cx != 0.4 || pane.cy != 0.6 || !pane.inverted || !got.NativeState().Inverted {
		t.Fatalf("applied native state = %#v, pane = %#v", got.NativeState(), pane)
	}
}

func TestNativeRequestUsesActiveState(t *testing.T) {
	model := Model{panes: [2]*pane{newPane(), newPane()}, nativeAvailable: true}
	model.panes[0].book = &book.Book{Path: "/tmp/comic.cbz"}
	model.panes[0].page = 4
	model.panes[0].webOffset = 0.25
	model.panes[0].inverted = true
	model.webtoon = true

	updated, _ := model.updateRead(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	got := updated.(Model)
	if !got.NativeRequested() {
		t.Fatal("f must request the native reader")
	}
	state := got.NativeState()
	if state.Path != "/tmp/comic.cbz" || state.Page != 4 || state.Offset != 0.25 || !state.Webtoon || !state.Inverted {
		t.Fatalf("native state = %#v", state)
	}
}

func testBookPath(t *testing.T, metadata ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "comic.cbz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for _, name := range []string{"1.png", "2.png", "3.png"} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(w, image.NewGray(image.Rect(0, 0, 40, 40))); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range metadata {
		w, err := z.Create("ComicInfo.xml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func testModel() Model {
	return Model{panes: [2]*pane{newPane(), newPane()}, width: 80, height: 24,
		marks: &bookmarks.Store{}, progress: &progress.Store{}, srv: new(server.Server),
		renderer: render.NewHalfBlock(), ocrText: make(map[string]string)}
}

func TestOverlayKeepsKittyTransmission(t *testing.T) {
	m := testModel()
	m.mode = modeHelp
	p := m.panes[0]
	p.gen = 1
	cols, rows := m.imgBox(0)
	updated, _ := m.Update(renderedMsg{target: p, gen: 1, cols: cols, rows: rows, res: render.Result{Transmit: []byte("payload")}})
	m = updated.(Model)
	updated, _ = m.Update(frameReadyMsg{target: p, gen: 1})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if !strings.Contains(m.View(), "payload") {
		t.Fatal("overlay lost unpainted transmission")
	}
}

func TestNativeExitUpdatesAllBooks(t *testing.T) {
	m := testModel()
	m.split = true
	m.active = 1
	m.panes[0].book = &book.Book{Path: "a.cbz"}
	m.panes[0].page = 2
	m.panes[1].book = &book.Book{Path: "b.cbz"}
	m.panes[1].page = 3
	state := NativeState{Path: "b.cbz", Page: 8, Offset: .5, Webtoon: true}
	if err := m.SaveNativeProgress(state); err != nil {
		t.Fatal(err)
	}
	a, _ := m.progress.Get("a.cbz")
	b, _ := m.progress.Get("b.cbz")
	if a.Page != 2 || a.Webtoon || b.Page != 8 || !b.Webtoon || b.Offset != .5 {
		t.Fatalf("positions: %#v, %#v", a, b)
	}
	if err := m.ClearProgress(); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.progress.Get("a.cbz"); ok {
		t.Fatal("clear retained inactive book")
	}
	if _, ok := m.progress.Get("b.cbz"); ok {
		t.Fatal("clear retained active book")
	}
}

func TestRestoredWebtoonNormalizesLayout(t *testing.T) {
	m := testModel()
	m.split = true
	m = m.ApplyNativeState(NativeState{Webtoon: true, Spread: true})
	if m.webtoon || m.spread || !m.split {
		t.Fatal("native return created conflicting split modes")
	}
	m.split = false
	m.spread = true
	path := testBookPath(t)
	m.progress.Set(path, progress.Position{Webtoon: true})
	updated, _ := m.openBook(0, path, nil)
	m = updated.(Model)
	defer m.panes[0].book.Close()
	if !m.webtoon || m.spread {
		t.Fatal("saved webtoon retained spread")
	}
}

func TestWebtoonDropsOutwardBacklog(t *testing.T) {
	for _, pending := range []float64{3800, -38} {
		m := testModel()
		m.webtoon = true
		p := m.panes[0]
		p.gen = 1
		p.webOffset = .9
		p.webScroll = pending
		cols, rows := m.imgBox(0)
		updated, _ := m.Update(renderedMsg{target: p, gen: 1, offset: .9, scroll: 3, cols: cols, rows: rows})
		m = updated.(Model)
		if pending > 0 && p.webScroll != 0 {
			t.Fatal("outward backlog remained")
		}
		if pending < 0 && p.webScroll >= 0 {
			t.Fatal("reverse input was discarded")
		}
	}
}

func TestFailedSpreadClearsOldRightPage(t *testing.T) {
	m := testModel()
	m.spread = true
	p := m.panes[0]
	p.gen = 1
	p.res2 = render.Result{Rows: 1, Lines: []string{"old"}}
	updated, _ := m.Update(renderedMsg{target: p, gen: 1, slot: 1, err: errors.New("corrupt page")})
	m = updated.(Model)
	if p.res2.Rows != 0 || p.err2 == nil {
		t.Fatal("failed replacement retained old spread page")
	}
	// A later left-slot result must not erase the right-slot error.
	cols, rows := m.imgBox(0)
	updated, _ = m.Update(renderedMsg{target: p, gen: 1, cols: cols, rows: rows})
	m = updated.(Model)
	if p.err2 == nil {
		t.Fatal("left result erased right error")
	}
}

func TestZenSearchShowsInput(t *testing.T) {
	m := testModel()
	m.zen = true
	m.mode = modeSearch
	m.input = "needle"
	if !strings.Contains(m.View(), "/needle") {
		t.Fatal("zen search input is hidden")
	}
	_, rows := m.imgBox(0)
	if rows != 23 {
		t.Fatalf("image rows = %d, want 23", rows)
	}
}

func TestNativeRequestInvalidatesPendingOCR(t *testing.T) {
	m := testModel()
	m.nativeAvailable = true
	m.panes[0].book = &book.Book{Path: "comic.cbz"}
	m.find = search{gen: 4, running: true, term: "needle", total: 3}
	updated, _ := m.updateRead(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	m = updated.(Model)
	m = m.ApplyNativeState(m.NativeState())
	updated, _ = m.updateOCR(ocrMsg{gen: 4, book: m.panes[0].book, text: "needle"})
	m = updated.(Model)
	if m.find.running || len(m.ocrText) != 0 {
		t.Fatal("native round trip retained pending search")
	}
}

func TestReplacedBookRejectsRenderAndOCR(t *testing.T) {
	m := testModel()
	updated, _ := m.openBook(0, testBookPath(t), nil)
	m = updated.(Model)
	p := m.panes[0]
	oldBook, oldGen := p.book, p.gen
	m.find = search{gen: 4, running: true, term: "needle", total: 3}
	updated, _ = m.openBook(0, testBookPath(t), nil)
	m = updated.(Model)
	defer m.panes[0].book.Close()
	updated, _ = m.Update(renderedMsg{target: p, book: oldBook, gen: oldGen, page: 80})
	m = updated.(Model)
	updated, _ = m.Update(frameReadyMsg{target: p, book: oldBook, gen: oldGen})
	m = updated.(Model)
	updated, _ = m.updateOCR(ocrMsg{gen: 4, book: oldBook, text: "needle"})
	m = updated.(Model)
	if p.page != 0 || !p.loading || len(m.ocrText) != 0 {
		t.Fatal("replaced book accepted stale work")
	}
	// Even a matching search generation cannot cache another book's text.
	m.find = search{gen: 4, running: true}
	updated, _ = m.updateOCR(ocrMsg{gen: 4, book: oldBook, text: "needle"})
	m = updated.(Model)
	if len(m.ocrText) != 0 {
		t.Fatal("OCR ignored book identity")
	}
}

func TestPaneRemapRejectsOldRender(t *testing.T) {
	m := testModel()
	m.split = true
	m.active = 1
	updated, _ := m.openBook(1, testBookPath(t), nil)
	m = updated.(Model)
	p := m.panes[1]
	gen := p.gen
	updated, _ = m.toggleSplit()
	m = updated.(Model)
	defer m.panes[0].book.Close()
	updated, _ = m.Update(renderedMsg{pane: 1, target: p, book: p.book, gen: gen, page: 80})
	m = updated.(Model)
	if m.panes[0].page != 0 || m.panes[1].page != 0 {
		t.Fatal("remapped pane accepted old render")
	}
}

func TestSafeTextRemovesArchiveControls(t *testing.T) {
	if got := safeText("title\x1b]2;spoof\a\x1b[31mred\x1b[0m\n\t"); got != "titlered" {
		t.Fatalf("safe text = %q", got)
	}
}

func TestReverseScrollReplacesOutwardQueue(t *testing.T) {
	m := testModel()
	m.webtoon = true
	p := m.panes[0]
	p.book = &book.Book{}
	p.loading = true
	p.gen = 1
	p.webScroll = 3800
	p.webStep = 3
	p.webOffset = .9
	for range 2 {
		updated, _ := m.scrollWebtoon(0, -1)
		m = updated.(Model)
	}
	cols, rows := m.imgBox(0)
	updated, _ := m.Update(renderedMsg{target: p, book: p.book, gen: 1, offset: .9, scroll: 3, cols: cols, rows: rows})
	m = updated.(Model)
	if p.webScroll != -2 {
		t.Fatalf("reverse queue = %v, want -2", p.webScroll)
	}
}

func TestZenErrorsScheduleReplacementFrame(t *testing.T) {
	for _, action := range []string{"toggle", "remove", "rename", "chapters"} {
		t.Run(action, func(t *testing.T) {
			m := testModel()
			b, err := book.Open(testBookPath(t, "<ComicInfo>"))
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			p := m.panes[0]
			p.book = b
			m.zen = true
			p.res = render.Result{Cols: m.width, Rows: m.height}
			m.menu = menu{kind: menuBookmarks, items: []menuItem{{path: b.Path}}}
			var updated tea.Model
			var cmd tea.Cmd
			switch action {
			case "toggle":
				updated, cmd = m.updateRead(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
			case "remove":
				m.mode = modeMenu
				updated, cmd = m.updateMenu(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
			case "rename":
				m.mode = modeMenuRename
				updated, cmd = m.updateMenuRename(tea.KeyMsg{Type: tea.KeyEnter})
			case "chapters":
				updated, cmd = m.openChapters()
			}
			m = updated.(Model)
			if m.zen || m.mode != modeRead || m.status == "" || cmd == nil || !p.loading {
				t.Fatal("zen error did not restore chrome and schedule a frame")
			}
			msg := cmd().(renderedMsg)
			cols, rows := m.imgBox(0)
			if msg.err != nil || msg.cols != cols || msg.rows != rows || rows >= m.height {
				t.Fatalf("replacement geometry: %#v", msg)
			}
		})
	}
}

func TestOpenSplitRendersOriginalPaneAndCancelsSearch(t *testing.T) {
	for _, selectBook := range []bool{false, true} {
		m := testModel()
		updated, _ := m.openBook(0, testBookPath(t), nil)
		m = updated.(Model)
		p := m.panes[0]
		defer p.book.Close()
		p.loading = false
		p.res = render.Result{Cols: 78, Rows: 21}
		gen := p.gen
		m.find = search{gen: 4, running: true}
		updated, cmd := m.updateRead(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})
		m = updated.(Model)
		if !m.split || m.active != 0 || m.pickFor != 1 || m.find.running || m.find.gen == 4 || p.gen == gen || !p.loading || cmd == nil {
			t.Fatal("O did not rerender the original pane and cancel search")
		}
		var frame renderedMsg
		for _, c := range cmd().(tea.BatchMsg) {
			if msg, ok := c().(renderedMsg); ok {
				frame = msg
			}
		}
		if frame.target != p || frame.cols >= p.res.Cols || frame.err != nil {
			t.Fatalf("split replacement: %#v", frame)
		}
		if selectBook {
			m.mode = modeRead
			updated, _ = m.openBook(m.pickFor, testBookPath(t), nil)
			m = updated.(Model)
			defer m.panes[1].book.Close()
		} else {
			updated, _ = m.updatePicker(tea.KeyMsg{Type: tea.KeyEsc})
			m = updated.(Model)
		}
		updated, _ = m.Update(frame)
		m = updated.(Model)
		if m.mode != modeRead || m.panes[0].res.Cols != frame.res.Cols || frame.res.Rows == 0 {
			t.Fatal("picker exit lost the original pane replacement")
		}
	}
}

func testEPUBPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "text.epub")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, text := range map[string]string{
		"META-INF/container.xml": `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf"><manifest><item id="text" href="text.xhtml" media-type="application/xhtml+xml"/><item id="image" href="page.png" media-type="image/png"/></manifest><spine><itemref idref="text"/><itemref idref="image"/><itemref idref="text"/></spine></package>`,
		"text.xhtml":             `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Readable text.</p></body></html>`,
	} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	w, err := z.Create("page.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(w, image.NewGray(image.Rect(0, 0, 40, 40))); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEPUBHalfblockGuidanceKeepsBookOpen(t *testing.T) {
	path := testEPUBPath(t)
	m := New(render.NewHalfBlock(), new(server.Server), &bookmarks.Store{}, &progress.Store{}, nil, true, []string{path})
	if m.panes[0].book == nil {
		t.Fatalf("open EPUB: %v", m.panes[0].err)
	}
	defer m.panes[0].book.Close()
	m.width, m.height = 100, 30
	allowed := false
	for _, ext := range m.picker.AllowedTypes {
		if ext == ".epub" {
			allowed = true
		}
	}
	if !allowed {
		t.Fatal("picker excludes EPUB")
	}
	msg := m.renderPane(0)().(renderedMsg)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "use e for browser, f for native") || m.panes[0].book == nil {
		t.Fatalf("halfblock guidance: %v", msg.err)
	}
	if _, err := m.panes[0].book.Page(0); err != nil {
		t.Fatalf("native/browser text unavailable: %v", err)
	}
	m.panes[0].page = 1
	if msg := m.renderPane(0)().(renderedMsg); msg.err != nil {
		t.Fatalf("comic image blocked: %v", msg.err)
	}
	m.spread = true
	for _, page := range []int{0, 1} {
		for _, reverse := range []bool{false, true} {
			m.panes[0].page = page
			cmds := m.renderPane(0)().(tea.BatchMsg)
			if len(cmds) != 2 {
				t.Fatalf("spread commands = %d", len(cmds))
			}
			if reverse {
				cmds[0], cmds[1] = cmds[1], cmds[0]
			}
			for _, cmd := range cmds {
				updated, _ := m.Update(cmd())
				m = updated.(Model)
			}
			view := m.View()
			if !strings.Contains(view, "EPUB text needs pixel rendering") || !strings.Contains(view, "▀") {
				t.Fatalf("spread page %d, reverse %v hid guidance or image: %q", page, reverse, view)
			}
		}
	}
	m.spread = false
	m.webtoon = true
	m.panes[0].page = 0
	if msg := m.renderPane(0)().(renderedMsg); msg.err == nil {
		t.Fatal("webtoon silently used halfblock text")
	}
}

func TestInversionRendersEveryViewMode(t *testing.T) {
	b, err := book.Open(testBookPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, mode := range []string{"single", "split", "spread", "webtoon"} {
		t.Run(mode, func(t *testing.T) {
			m := testModel()
			m.split, m.spread, m.webtoon = mode == "split", mode == "spread", mode == "webtoon"
			if m.split {
				m.active = 1
			}
			p := m.panes[m.active]
			p.book = b
			for _, inverted := range []bool{false, true, false} {
				cmd := m.renderPane(m.active)
				if inverted != p.inverted {
					updated, toggle := m.updateRead(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
					m, cmd = updated.(Model), toggle
				}
				if p.inverted != inverted || m.panes[1-m.active].inverted {
					t.Fatal("i did not toggle only the active pane")
				}
				var frames []renderedMsg
				switch msg := cmd().(type) {
				case renderedMsg:
					frames = append(frames, msg)
				case tea.BatchMsg:
					for _, c := range msg {
						frames = append(frames, c().(renderedMsg))
					}
				}
				wantFrames := 1
				if m.spread {
					wantFrames = 2
				}
				if len(frames) != wantFrames {
					t.Fatalf("frames = %d, want %d", len(frames), wantFrames)
				}
				for _, frame := range frames {
					white := strings.Contains(strings.Join(frame.res.Lines, ""), "38;2;255;255;255m")
					if frame.err != nil || white != inverted {
						t.Fatalf("inverted=%v: white=%v, error=%v", inverted, white, frame.err)
					}
				}
			}
		})
	}
	img, err := b.Page(0)
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, a := img.At(0, 0).RGBA(); r != 0 || a != 0xffff {
		t.Fatal("inversion changed the decoded page cache")
	}
}

func TestInversionSurvivesNavigationAndScreenshot(t *testing.T) {
	m := testModel()
	updated, _ := m.openBook(0, testBookPath(t), nil)
	m = updated.(Model)
	defer m.panes[0].book.Close()
	updated, _ = m.updateRead(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	m = updated.(Model)
	updated, _ = m.goTo(0, 1)
	m = updated.(Model)
	if !m.panes[0].inverted || !m.NativeState().Inverted {
		t.Fatal("page navigation lost inversion")
	}
	t.Setenv("CBZR_SHOT_DIR", t.TempDir())
	_, cmd := m.screenshot()
	m.panes[0].inverted = false
	shot := cmd().(shotMsg)
	if shot.err != nil {
		t.Fatal(shot.err)
	}
	f, err := os.Open(shot.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := img.At(0, 0).RGBA(); r != 0xffff {
		t.Fatal("screenshot lost its captured inversion setting")
	}
}

type recordingRenderer struct {
	render.Renderer
	images []image.Image
}

func (r *recordingRenderer) Name() string { return "recording" }
func (r *recordingRenderer) Render(img image.Image, _ uint32, _, _ int) (render.Result, error) {
	r.images = append(r.images, img)
	return render.Result{}, nil
}

func TestEPUBInversionLeavesIllustrationsUnchanged(t *testing.T) {
	b, err := book.Open(testEPUBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, mode := range []string{"text", "image", "spread", "webtoon"} {
		t.Run(mode, func(t *testing.T) {
			m := testModel()
			m.height = 12
			m.spread, m.webtoon = mode == "spread", mode == "webtoon"
			r := &recordingRenderer{Renderer: m.renderer}
			m.renderer = r
			p := m.panes[0]
			p.book = b
			if mode == "image" || m.webtoon {
				p.page = 1
			}
			for _, inverted := range []bool{false, true, false} {
				p.inverted = inverted
				r.images = nil
				switch msg := m.renderPane(0)().(type) {
				case renderedMsg:
					if msg.err != nil {
						t.Fatal(msg.err)
					}
				case tea.BatchMsg:
					for _, cmd := range msg {
						if msg := cmd().(renderedMsg); msg.err != nil {
							t.Fatal(msg.err)
						}
					}
				}
				wantCount := 1
				if m.spread {
					wantCount = 2
				}
				if len(r.images) != wantCount {
					t.Fatalf("rendered %d images, want %d", len(r.images), wantCount)
				}
				textColor := uint32(0xffff)
				if inverted {
					textColor = 0
				}
				for i, img := range r.images {
					want, point := textColor, image.Pt(0, 0)
					if mode == "image" || i == 1 || m.webtoon {
						want = 0
					}
					if m.webtoon {
						point.X = img.Bounds().Dx() / 2
						if red, _, _, _ := img.At(point.X, 41).RGBA(); red != textColor {
							t.Fatalf("mixed strip text = %d, want %d", red, textColor)
						}
					}
					if red, _, _, _ := img.At(point.X, point.Y).RGBA(); red != want {
						t.Fatalf("inverted=%v, slot=%d: pixel = %d, want %d", inverted, i, red, want)
					}
				}
			}
		})
	}
	m := testModel()
	m.panes[0].book, m.panes[0].page, m.panes[0].inverted = b, 1, true
	t.Setenv("CBZR_SHOT_DIR", t.TempDir())
	_, cmd := m.screenshot()
	shot := cmd().(shotMsg)
	if shot.err != nil {
		t.Fatal(shot.err)
	}
	f, err := os.Open(shot.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if red, _, _, _ := img.At(0, 0).RGBA(); red != 0 {
		t.Fatal("screenshot inverted the EPUB illustration")
	}
}
