// Package ui runs the terminal book reader.
package ui

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/filepicker"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"cbzr/internal/book"
	"cbzr/internal/bookmarks"
	"cbzr/internal/native"
	"cbzr/internal/ocr"
	"cbzr/internal/progress"
	"cbzr/internal/render"
	"cbzr/internal/server"
)

type mode int

const (
	modeRead mode = iota
	modePick
	modeHelp
	modeMenu
	modeSearch
	modeMenuFilter
	modeMenuRename
)

const (
	zoomStep  = 1.25
	zoomMax   = 8.0
	targetFPS = 120
)

type pane struct {
	book    *book.Book
	page    int
	res     render.Result
	res2    render.Result // right page in spread mode
	err     error
	err2    error
	loading bool
	gen     int

	rot       int // quarter turns cw
	inverted  bool
	zoom      float64
	cx, cy    float64 // view center (fractions of rotated image)
	webOffset float64 // vertical position in the current webtoon page
	webScroll float64 // pending webtoon scroll in terminal rows
	webStep   float64 // scroll included in the in-flight frame
	textTop   int     // rows scrolled on a plain-text EPUB page
}

func newPane() *pane { return &pane{zoom: 1, cx: 0.5, cy: 0.5} }

func (p *pane) resetView() { p.zoom, p.cx, p.cy, p.textTop = 1, 0.5, 0.5, 0 }

// search holds OCR search state for the active book.
type search struct {
	gen     int
	pane    int
	term    string
	hits    []int
	scanned int
	skipped int // pages whose text could not be read
	total   int
	running bool
}

// Model is the root bubbletea model.
type Model struct {
	renderer render.Renderer
	srv      *server.Server
	marks    *bookmarks.Store
	progress *progress.Store

	panes   [2]*pane
	active  int
	split   bool
	spread  bool // two pages side by side in a single pane
	zen     bool // hide titles and the status bar; keep search input visible
	webtoon bool // continuous, width-fit vertical reading

	width, height   int
	mode            mode
	picker          filepicker.Model
	pickFor         int
	count           string
	status          string
	openBrowser     func(string) error
	nativeAvailable bool

	menu    menu
	input   string // search / filter / rename input buffer
	find    search
	ocrText map[string]string // "path\x00page" -> text

	resizeGen       int
	saveOnQuit      bool
	nativeRequested bool
}

type renderedMsg struct {
	target                *pane
	book                  *book.Book
	pane, slot, gen, page int
	cols, rows            int
	offset                float64
	scroll                float64
	res                   render.Result
	err                   error
}

type shotMsg struct {
	path string
	err  error
}

type ocrMsg struct {
	gen, page int
	book      *book.Book
	text      string
	err       error
}

type resizeSettledMsg struct{ gen int }

type frameReadyMsg struct {
	target          *pane
	book            *book.Book
	pane, slot, gen int
	rerender        bool
}

// New builds the model, opening up to two books given on the command line.
func New(r render.Renderer, srv *server.Server, marks *bookmarks.Store, positions *progress.Store, openBrowser func(string) error, nativeAvailable bool, paths []string) Model {
	m := Model{
		renderer:        r,
		srv:             srv,
		marks:           marks,
		progress:        positions,
		panes:           [2]*pane{newPane(), newPane()},
		openBrowser:     openBrowser,
		nativeAvailable: nativeAvailable,
		ocrText:         map[string]string{},
	}
	resumeWebtoon := false
	for i, path := range paths {
		if i > 1 {
			break
		}
		b, err := book.Open(path)
		if err != nil {
			m.panes[i].err = err
			continue
		}
		m.panes[i].book = b
		if pos, ok := positions.Get(b.Path); ok {
			m.panes[i].page = min(b.Len()-1, max(0, pos.Page))
			m.panes[i].webOffset = clamp(pos.Offset, 0, 1)
			m.panes[i].webScroll = pos.Scroll
			if i == 0 {
				resumeWebtoon = pos.Webtoon
			}
		}
	}
	if m.panes[1].book != nil {
		m.split = true
	} else {
		m.webtoon = resumeWebtoon
	}

	fp := filepicker.New()
	fp.AllowedTypes = []string{".cbz", ".zip", ".cbr", ".rar", ".epub"}
	if wd, err := os.Getwd(); err == nil {
		fp.CurrentDirectory = wd
	}
	fp.AutoHeight = false
	m.picker = fp
	return m
}

// Books returns the panes' books in pane order (nil for empty panes).
func (m Model) Books() []*book.Book {
	return []*book.Book{m.panes[0].book, m.panes[1].book}
}

// NativeState holds the active pane's reading state for the native window.
type NativeState = native.State

// NativeRequested reports whether f requested the native macOS reader.
func (m Model) NativeRequested() bool { return m.nativeRequested }

// NativeState returns the active pane's current reading state.
func (m Model) NativeState() NativeState {
	p := m.panes[m.active]
	state := NativeState{Page: p.page, Offset: p.webOffset, Scroll: p.webScroll, Webtoon: m.webtoon, Rotation: p.rot, Zoom: p.zoom, CenterX: p.cx, CenterY: p.cy, Spread: m.spread, Inverted: p.inverted}
	if p.book != nil {
		state.Path = p.book.Path
	}
	return state
}

// ApplyNativeState updates the active pane after the native window returns.
func (m Model) ApplyNativeState(state NativeState) Model {
	p := m.panes[m.active]
	p.page = state.Page
	p.webOffset = state.Offset
	p.webScroll = state.Scroll
	p.rot = state.Rotation
	p.inverted = state.Inverted
	p.zoom = state.Zoom
	p.cx, p.cy = state.CenterX, state.CenterY
	m.webtoon = state.Webtoon && !m.split
	m.spread = state.Spread && !m.split && !m.webtoon
	m.nativeRequested = false
	return m
}

// SaveNativeProgress saves both panes with the native position for the active book.
func (m Model) SaveNativeProgress(state NativeState) error {
	for i := range m.panes {
		m.rememberPane(i)
	}
	m.progress.Set(state.Path, progress.Position{Page: state.Page, Offset: state.Offset, Scroll: state.Scroll, Webtoon: state.Webtoon})
	return m.progress.Save()
}

// ClearProgress removes saved positions for every open book.
func (m Model) ClearProgress() error {
	for _, p := range m.panes {
		if p.book != nil {
			m.progress.Delete(p.book.Path)
		}
	}
	return m.progress.Save()
}

// SaveOnQuit reports whether Q selected a saved exit.
func (m Model) SaveOnQuit() bool { return m.saveOnQuit }

// SaveProgress writes each open book's current position.
func (m Model) SaveProgress() error {
	for i := range m.panes {
		m.rememberPane(i)
	}
	return m.progress.Save()
}

func (m Model) rememberPane(i int) {
	p := m.panes[i]
	if p.book == nil {
		return
	}
	m.progress.Set(p.book.Path, progress.Position{
		Page:    p.page,
		Offset:  p.webOffset,
		Scroll:  p.webScroll,
		Webtoon: m.webtoon,
	})
}

func (m Model) Init() tea.Cmd {
	return tea.SetWindowTitle("cbzr")
}

// ---- layout ----------------------------------------------------------------

func (m Model) paneCount() int {
	if m.split {
		return 2
	}
	return 1
}

// paneBox returns the outer size of pane i.
func (m Model) paneBox(i int) (w, h int) {
	h = max(1, m.height-1)
	if m.zen && m.mode != modeSearch {
		h = max(1, m.height)
	}
	if !m.split {
		return max(1, m.width), h
	}
	left := (m.width - 1) / 2
	if i == 0 {
		return max(1, left), h
	}
	return max(1, m.width-1-left), h
}

// spreadActive reports whether two-page spread rendering is in effect.
func (m Model) spreadActive() bool { return m.spread && !m.split }

// imgBox returns the image area for one page slot inside pane i.
func (m Model) imgBox(i int) (cols, rows int) {
	w, h := m.paneBox(i)
	if m.spreadActive() {
		w = (w - 1) / 2
	}
	if m.zen {
		return max(1, w), max(1, h)
	}
	return max(1, w-2), max(1, h-2)
}

// ---- update ----------------------------------------------------------------

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.renderer.SetCellSize(render.CellSize())
		m.picker.SetHeight(max(1, m.height-4))
		m.resizeGen++
		gen := m.resizeGen
		// Multiplexers (cmux, tmux) can settle geometry after the first
		// resize event; render again once things go quiet.
		settle := tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg {
			return resizeSettledMsg{gen: gen}
		})
		return m, tea.Batch(m.rerenderAll(), settle)

	case resizeSettledMsg:
		if msg.gen != m.resizeGen {
			return m, nil
		}
		m.renderer.SetCellSize(render.CellSize())
		return m, m.rerenderAll()

	case renderedMsg:
		p := m.panes[msg.pane]
		if msg.gen != p.gen || msg.target != p || msg.book != p.book {
			return m, nil // stale
		}
		if msg.slot == 1 {
			p.res2 = render.Result{}
			p.err2 = msg.err
			if msg.err == nil {
				p.res2 = msg.res
				return m, frameReadyAfterPaint(p, msg.pane, msg.slot, msg.gen, false)
			}
			return m, nil
		}
		p.err = msg.err
		if msg.err != nil {
			p.loading = false
			return m, nil
		}
		stalled := m.webtoon && msg.scroll != 0 && p.page == msg.page && math.Abs(p.webOffset-msg.offset) < 1e-9
		p.page = msg.page
		p.webOffset = msg.offset
		p.webScroll -= msg.scroll
		p.webStep = 0
		if stalled && p.webScroll*msg.scroll > 0 {
			p.webScroll = 0
		}
		if math.Abs(p.webScroll) < 0.001 {
			p.webScroll = 0
		}
		// Transmit bytes are emitted inside View so that bubbletea's
		// renderer stays the only writer to the terminal.
		p.res = msg.res
		rerender := false
		if cols, rows := m.imgBox(msg.pane); cols != msg.cols || rows != msg.rows {
			rerender = true
		}
		if m.webtoon && p.webScroll != 0 {
			rerender = true
		}
		ready := frameReadyAfterPaint(p, msg.pane, msg.slot, msg.gen, rerender)
		step := 1
		if m.spreadActive() {
			step = 2
		}
		return m, tea.Batch(ready, m.prefetch(msg.pane, msg.page+step))

	case frameReadyMsg:
		p := m.panes[msg.pane]
		if msg.gen != p.gen || msg.target != p || msg.book != p.book {
			return m, nil
		}
		if msg.slot == 1 {
			return m, nil
		}
		p.loading = false
		if msg.rerender || m.webtoon && p.webScroll != 0 {
			return m, m.renderPane(msg.pane)
		}
		return m, nil

	case shotMsg:
		if msg.err != nil {
			m.status = "screenshot failed: " + msg.err.Error()
		} else {
			m.status = "saved " + msg.path
		}
		return m, nil

	case ocrMsg:
		return m.updateOCR(msg)

	case tea.MouseMsg:
		return m.updateMouse(msg)

	case tea.KeyMsg:
		// Terminals can batch fast typing (or paste) into one rune message;
		// replay it as individual keys so counts like "2j" still work.
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste {
			var mdl tea.Model = m
			var cmds []tea.Cmd
			for _, r := range msg.Runes {
				var cmd tea.Cmd
				mdl, cmd = mdl.(Model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			return mdl, tea.Batch(cmds...)
		}
		switch m.mode {
		case modePick:
			return m.updatePicker(msg)
		case modeHelp:
			m.mode = modeRead
			return m, nil
		case modeMenu:
			return m.updateMenu(msg)
		case modeMenuFilter:
			return m.updateMenuFilter(msg)
		case modeMenuRename:
			return m.updateMenuRename(msg)
		case modeSearch:
			return m.updateSearch(msg)
		default:
			return m.updateRead(msg)
		}
	}

	if m.mode == modePick {
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || m.mode != modeRead {
		return m, nil
	}
	target := 0
	if m.split && msg.X > (m.width-1)/2 {
		target = 1
	}
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		if m.webtoon {
			return m.scrollWebtoon(target, 1)
		}
		return m.turn(target, 1)
	case tea.MouseButtonWheelUp:
		if m.webtoon {
			return m.scrollWebtoon(target, -1)
		}
		return m.turn(target, -1)
	case tea.MouseButtonLeft:
		if m.split {
			m.active = target
		}
	}
	return m, nil
}

func (m Model) updateRead(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	m.status = ""

	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && !(key == "0" && m.count == "") {
		m.count += key
		return m, nil
	}

	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "Q":
		m.saveOnQuit = true
		return m, tea.Quit

	case "?":
		m.mode = modeHelp
		return m, nil

	// -- pages / webtoon viewport scrolling --
	case "j":
		return m.turn(m.active, m.takeCount())
	case "k":
		return m.turn(m.active, -m.takeCount())
	case "J":
		return m.turnHalf(m.active, m.takeCount())
	case "K":
		return m.turnHalf(m.active, -m.takeCount())
	case "g":
		return m.goTo(m.active, m.takeCountOr(1)-1)
	case "G":
		p := m.panes[m.active]
		if p.book == nil {
			return m, nil
		}
		if m.webtoon && m.count == "" {
			p.page = p.book.Len() - 1
			p.webOffset = 1
			p.webScroll = 0
			return m, m.renderPane(m.active)
		}
		return m.goTo(m.active, m.takeCountOr(p.book.Len())-1)

	// -- panes --
	case "w":
		if m.split {
			m.active = 1 - m.active
		}
		return m, nil
	case "v":
		if m.webtoon {
			m.status = "split is unavailable in webtoon mode (t to leave)"
			return m, nil
		}
		return m.toggleSplit()
	case "s":
		if m.webtoon {
			m.status = "spread is unavailable in webtoon mode (t to leave)"
			return m, nil
		}
		if m.split {
			m.status = "spread needs a single pane (v to unsplit)"
			return m, nil
		}
		m.spread = !m.spread
		return m, m.rerenderAll()
	case "x":
		return m.closePane(m.active)
	case "z":
		m.zen = !m.zen
		return m, m.rerenderAll()
	case "f":
		if !m.nativeAvailable {
			m.status = "native reader is unavailable in this build"
			return m, nil
		}
		if m.panes[m.active].book == nil {
			m.status = "no book in this pane"
			return m, nil
		}
		m.cancelSearch()
		for _, p := range m.panes {
			p.gen++
			p.loading = false
		}
		m.nativeRequested = true
		return m, tea.Quit
	case "t":
		if m.split {
			m.status = "webtoon mode needs a single pane (v to unsplit)"
			return m, nil
		}
		m.webtoon = !m.webtoon
		m.spread = false
		p := m.panes[m.active]
		p.webScroll = 0
		p.resetView()
		return m, m.rerenderAll()

	// -- menus --
	case "tab":
		return m.openChapters()
	case "F":
		return m.openBookmarks()

	// -- bookmark toggle --
	case "b":
		p := m.panes[m.active]
		if p.book == nil {
			return m, nil
		}
		added, err := m.marks.Toggle(p.book.Path, p.book.Title, p.page)
		if err != nil {
			m.zen = false
			m.status = "bookmark failed: " + err.Error()
			return m, m.rerenderAll()
		}
		if added {
			m.status = fmt.Sprintf("bookmarked %s p.%d", p.book.Title, p.page+1)
		} else {
			m.status = fmt.Sprintf("removed bookmark %s p.%d", p.book.Title, p.page+1)
		}
		return m, nil

	// -- view transforms --
	case "i":
		p := m.panes[m.active]
		if p.book == nil {
			return m, nil
		}
		p.inverted = !p.inverted
		return m, m.renderPane(m.active)
	case "R":
		p := m.panes[m.active]
		if p.book == nil {
			return m, nil
		}
		p.rot = (p.rot + 1) & 3
		return m, m.renderPane(m.active)
	case "+", "=":
		if m.webtoon {
			m.status = "webtoon mode fits pages to the viewport width"
			return m, nil
		}
		return m.zoomBy(zoomStep)
	case "-":
		if m.webtoon {
			m.status = "webtoon mode fits pages to the viewport width"
			return m, nil
		}
		return m.zoomBy(1 / zoomStep)
	case "0":
		p := m.panes[m.active]
		if p.book == nil {
			return m, nil
		}
		p.resetView()
		return m, m.renderPane(m.active)
	case "up", "down", "left", "right":
		return m.pan(key)

	// -- OCR search --
	case "/":
		p := m.panes[m.active]
		if p.book == nil {
			return m, nil
		}
		if !ocr.Available() && !p.book.HasText() {
			m.status = "OCR needs tesseract on PATH"
			return m, nil
		}
		m.mode = modeSearch
		m.input = ""
		return m, m.rerenderAll()
	case "n":
		return m.gotoHit(1)
	case "p":
		return m.gotoHit(-1)

	// -- files / misc --
	case "S":
		return m.screenshot()
	case "o":
		m.mode = modePick
		m.pickFor = m.active
		return m, m.picker.Init()
	case "O":
		if m.webtoon {
			m.status = "split is unavailable in webtoon mode (t to leave)"
			return m, nil
		}
		var layout tea.Cmd
		if !m.split {
			m.cancelSearch()
			m.split = true
			layout = m.rerenderAll()
		}
		m.mode = modePick
		m.pickFor = 1
		return m, tea.Batch(layout, m.picker.Init())
	case "e":
		return m.browse()
	case "r":
		return m, m.rerenderAll()
	case "esc":
		m.count = ""
		return m, nil
	}
	return m, nil
}

func (m Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" || msg.String() == "ctrl+c" {
		m.mode = modeRead
		return m, nil
	}
	var cmd tea.Cmd
	m.picker, cmd = m.picker.Update(msg)
	if ok, path := m.picker.DidSelectFile(msg); ok {
		m.mode = modeRead
		return m.openBook(m.pickFor, path, cmd)
	}
	return m, cmd
}

func (m Model) updateMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "tab", "F":
		if m.menu.filter != "" && msg.String() == "esc" {
			m.menu.filter = ""
			m.menu.applyFilter()
			return m, nil
		}
		m.mode = modeRead
		return m, nil
	case "j", "down":
		m.menu.move(1, m.menuHeight())
		return m, nil
	case "k", "up":
		m.menu.move(-1, m.menuHeight())
		return m, nil
	case "g":
		m.menu.move(-len(m.menu.items), m.menuHeight())
		return m, nil
	case "G":
		m.menu.move(len(m.menu.items), m.menuHeight())
		return m, nil
	case "/":
		m.mode = modeMenuFilter
		return m, nil
	case "r":
		if m.menu.kind != menuBookmarks {
			return m, nil
		}
		it, ok := m.menu.selected()
		if !ok {
			return m, nil
		}
		m.input = ""
		for _, mk := range m.marks.Marks {
			if mk.Book == it.path && mk.Page == it.page {
				m.input = mk.Name
				break
			}
		}
		m.mode = modeMenuRename
		return m, nil
	case "d":
		if m.menu.kind != menuBookmarks {
			return m, nil
		}
		it, ok := m.menu.selected()
		if !ok {
			return m, nil
		}
		if err := m.marks.Remove(it.path, it.page); err != nil {
			m.zen = false
			m.status = "bookmark failed: " + err.Error()
			m.mode = modeRead
			return m, m.rerenderAll()
		}
		m.reloadBookmarkMenu()
		return m, nil
	case "enter", "l":
		it, ok := m.menu.selected()
		if !ok {
			m.mode = modeRead
			return m, nil
		}
		m.mode = modeRead
		p := m.panes[m.active]
		if it.path != "" && (p.book == nil || p.book.Path != it.path) {
			mm, cmd := m.openBook(m.active, it.path, nil)
			mdl := mm.(Model)
			mdl2, cmd2 := mdl.goTo(mdl.active, it.page)
			return mdl2, tea.Batch(cmd, cmd2)
		}
		return m.goTo(m.active, it.page)
	}
	return m, nil
}

func (m Model) updateMenuFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.menu.filter = ""
		m.menu.applyFilter()
		m.mode = modeMenu
		return m, nil
	case "enter":
		m.mode = modeMenu
		return m, nil
	case "backspace":
		if len(m.menu.filter) > 0 {
			m.menu.filter = m.menu.filter[:len(m.menu.filter)-1]
			m.menu.applyFilter()
		}
		return m, nil
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			if msg.Type == tea.KeySpace {
				m.menu.filter += " "
			} else {
				m.menu.filter += string(msg.Runes)
			}
			m.menu.cursor, m.menu.top = 0, 0
			m.menu.applyFilter()
		}
		return m, nil
	}
}

func (m Model) updateMenuRename(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeMenu
		return m, nil
	case "enter":
		if it, ok := m.menu.selected(); ok {
			if err := m.marks.Rename(it.path, it.page, m.input); err != nil {
				m.zen = false
				m.status = "bookmark failed: " + err.Error()
				m.mode = modeRead
				return m, m.rerenderAll()
			}
			m.reloadBookmarkMenu()
		}
		m.mode = modeMenu
		return m, nil
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	default:
		if msg.Type == tea.KeyRunes {
			m.input += string(msg.Runes)
		} else if msg.Type == tea.KeySpace {
			m.input += " "
		}
		return m, nil
	}
}

// reloadBookmarkMenu rebuilds menu rows from the store, preserving cursor
// position and the active filter.
func (m *Model) reloadBookmarkMenu() {
	cursor, top, filter := m.menu.cursor, m.menu.top, m.menu.filter
	m.menu = m.buildBookmarkMenu()
	m.menu.filter = filter
	m.menu.applyFilter()
	m.menu.cursor = max(0, min(cursor, len(m.menu.items)-1))
	m.menu.top = max(0, min(top, m.menu.cursor))
}

func (m Model) menuHeight() int { return max(1, m.height-3) }

func (m Model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeRead
		return m, m.rerenderAll()
	case "enter":
		m.mode = modeRead
		term := strings.TrimSpace(m.input)
		if term == "" {
			return m, m.rerenderAll()
		}
		updated, cmd := m.startSearch(term)
		return updated, tea.Batch(cmd, m.rerenderAll())
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	default:
		if msg.Type == tea.KeyRunes {
			m.input += string(msg.Runes)
		} else if msg.Type == tea.KeySpace {
			m.input += " "
		}
		return m, nil
	}
}

// ---- actions ---------------------------------------------------------------

func (m *Model) takeCount() int { return m.takeCountOr(1) }

func (m *Model) takeCountOr(def int) int {
	if m.count == "" {
		return def
	}
	n, err := strconv.Atoi(m.count)
	m.count = ""
	if err != nil || n < 1 {
		return def
	}
	return n
}

func (m Model) turn(i, delta int) (tea.Model, tea.Cmd) {
	p := m.panes[i]
	if p.book == nil {
		return m, nil
	}
	if m.webtoon {
		_, rows := m.imgBox(i)
		return m.scrollWebtoon(i, float64(delta)*webtoonScrollDistance(rows, 1))
	}
	step := 1
	if m.spreadActive() {
		step = 2 // one keypress flips a whole spread
	}
	return m.goTo(i, p.page+delta*step)
}

func (m Model) turnHalf(i, delta int) (tea.Model, tea.Cmd) {
	if !m.webtoon {
		return m.turn(i, delta)
	}
	_, rows := m.imgBox(i)
	return m.scrollWebtoon(i, float64(delta)*webtoonScrollDistance(rows, 0.5))
}

func (m Model) scrollWebtoon(i int, rows float64) (tea.Model, tea.Cmd) {
	p := m.panes[i]
	if p.book == nil {
		return m, nil
	}
	inFlight := 0.0
	if p.loading {
		inFlight = p.webStep
	}
	if (p.webScroll-inFlight)*rows < 0 {
		p.webScroll = inFlight
	}
	p.webScroll += rows
	if p.loading {
		return m, nil
	}
	return m, m.renderPane(i)
}

func (m Model) goTo(i, page int) (tea.Model, tea.Cmd) {
	p := m.panes[i]
	if p.book == nil {
		return m, nil
	}
	page = max(0, min(p.book.Len()-1, page))
	if page == p.page && p.res.Rows > 0 && (!m.webtoon || p.webOffset == 0 && p.webScroll == 0) {
		return m, nil
	}
	p.page = page
	p.webOffset = 0
	p.webScroll = 0
	p.resetView()
	return m, m.renderPane(i)
}

func (m Model) zoomBy(f float64) (tea.Model, tea.Cmd) {
	p := m.panes[m.active]
	if p.book == nil {
		return m, nil
	}
	p.zoom = clamp(p.zoom*f, 1, zoomMax)
	if p.zoom <= 1.001 {
		p.resetView()
	}
	return m, m.renderPane(m.active)
}

func (m Model) pan(key string) (tea.Model, tea.Cmd) {
	p := m.panes[m.active]
	if p.book == nil {
		return m, nil
	}
	if m.renderer.Name() == "halfblock" && !m.webtoon && p.book.IsTextPage(p.page) {
		cols, rows := m.imgBox(m.active)
		total := len(textLines(p.book.PageText(p.page), textWidth(cols)))
		step := max(1, rows/2)
		switch key {
		case "up":
			p.textTop = max(0, p.textTop-step)
		case "down":
			p.textTop = max(0, min(p.textTop+step, total-rows))
		default:
			return m, nil
		}
		return m, m.renderPane(m.active)
	}
	if p.zoom <= 1.001 {
		return m, nil
	}
	step := 0.15 / p.zoom
	switch key {
	case "up":
		p.cy -= step
	case "down":
		p.cy += step
	case "left":
		p.cx -= step
	case "right":
		p.cx += step
	}
	half := 0.5 / p.zoom
	p.cx = clamp(p.cx, half, 1-half)
	p.cy = clamp(p.cy, half, 1-half)
	return m, m.renderPane(m.active)
}

func clamp(v, lo, hi float64) float64 { return min(hi, max(lo, v)) }

func webtoonScrollDistance(viewportRows int, fraction float64) float64 {
	return max(1, float64(viewportRows)*fraction)
}

func smoothWebtoonScroll(pending float64) float64 {
	if math.Abs(pending) < 0.02 {
		return pending
	}
	return clamp(pending*0.3, -3, 3)
}

func (m Model) openBook(i int, path string, prev tea.Cmd) (tea.Model, tea.Cmd) {
	b, err := book.Open(path)
	p := m.panes[i]
	if err != nil {
		p.err = err
		return m, prev
	}
	m.cancelSearch()
	if p.book != nil {
		p.book.Close() //nolint:errcheck
	}
	*p = *newPane()
	p.book = b
	if pos, ok := m.progress.Get(b.Path); ok {
		p.page = min(b.Len()-1, max(0, pos.Page))
		p.webOffset = clamp(pos.Offset, 0, 1)
		p.webScroll = pos.Scroll
		if !m.split {
			m.webtoon = pos.Webtoon
			if m.webtoon {
				m.spread = false
			}
		}
	}
	m.syncServer()
	return m, tea.Batch(prev, m.renderPane(i))
}

func (m Model) toggleSplit() (tea.Model, tea.Cmd) {
	m.cancelSearch()
	if m.split {
		// keep the active pane's book in pane 0.
		if m.active == 1 {
			m.closeBook(0)
			m.panes[0], m.panes[1] = m.panes[1], newPane()
		} else {
			m.closeBook(1)
			m.panes[1] = newPane()
		}
		m.split = false
		m.active = 0
		m.syncServer()
		return m, m.rerenderAll()
	}
	m.split = true
	m.active = 1
	if m.panes[1].book == nil {
		m.mode = modePick
		m.pickFor = 1
		return m, tea.Batch(m.rerenderAll(), m.picker.Init())
	}
	return m, m.rerenderAll()
}

func (m Model) closePane(i int) (tea.Model, tea.Cmd) {
	m.cancelSearch()
	m.closeBook(i)
	m.panes[i] = newPane()
	if m.split {
		if i == 0 {
			m.panes[0], m.panes[1] = m.panes[1], newPane()
		}
		m.split = false
		m.active = 0
	}
	m.syncServer()
	return m, m.rerenderAll()
}

func (m *Model) closeBook(i int) {
	if m.panes[i].book != nil {
		m.panes[i].book.Close() //nolint:errcheck
	}
}

func (m *Model) syncServer() {
	if m.srv != nil {
		m.srv.SetBooks(m.Books())
	}
}

func (m Model) browse() (tea.Model, tea.Cmd) {
	p := m.panes[m.active]
	if p.book == nil {
		m.status = "no book in this pane"
		return m, nil
	}
	m.syncServer()
	port, err := m.srv.Start()
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/b/%d/?p=%d", port, m.active, p.page)
	if p.inverted {
		url += "&invert=1"
	}
	m.status = url
	if m.openBrowser != nil {
		if err := m.openBrowser(url); err != nil {
			m.status = url + "  (open failed: " + err.Error() + ")"
		}
	}
	return m, nil
}

// ---- menus -------------------------------------------------------------------

func (m Model) openChapters() (tea.Model, tea.Cmd) {
	p := m.panes[m.active]
	if p.book == nil {
		return m, nil
	}
	chs, err := p.book.Chapters()
	if err != nil {
		m.zen = false
		m.status = "chapters: " + err.Error()
		return m, m.rerenderAll()
	}
	if len(chs) == 0 {
		m.status = "no chapter info in this book"
		return m, nil
	}
	items := make([]menuItem, len(chs))
	cursor := 0
	for i, c := range chs {
		items[i] = menuItem{
			label: fmt.Sprintf("%-40s p.%d", ansi.Truncate(safeText(c.Title), 40, "…"), c.Page+1),
			page:  c.Page,
		}
		if c.Page <= p.page {
			cursor = i
		}
	}
	m.menu = menu{kind: menuChapters, title: "Chapters — " + safeText(p.book.Title), all: items, cursor: cursor}
	m.menu.applyFilter()
	m.menu.move(0, m.menuHeight())
	m.mode = modeMenu
	return m, nil
}

func (m Model) buildBookmarkMenu() menu {
	items := make([]menuItem, 0, len(m.marks.Marks))
	for _, mk := range m.marks.Marks {
		items = append(items, menuItem{
			label: fmt.Sprintf("%-36s p.%-5d %s",
				ansi.Truncate(safeText(mk.Label()), 36, "…"), mk.Page+1, mk.Added.Format("2006-01-02")),
			page: mk.Page,
			path: mk.Book,
		})
	}
	mn := menu{kind: menuBookmarks, title: "Bookmarks", all: items}
	mn.applyFilter()
	return mn
}

func (m Model) openBookmarks() (tea.Model, tea.Cmd) {
	m.menu = m.buildBookmarkMenu()
	m.mode = modeMenu
	return m, nil
}

// ---- screenshot ----------------------------------------------------------------

func (m Model) screenshot() (tea.Model, tea.Cmd) {
	p := m.panes[m.active]
	if p.book == nil {
		return m, nil
	}
	b, page, rot, zoom, cx, cy := p.book, p.page, p.rot, p.zoom, p.cx, p.cy
	inverted := p.inverted
	return m, func() tea.Msg {
		img, err := b.Page(page)
		if err != nil {
			return shotMsg{err: err}
		}
		img = render.Transform(img, rot, zoom, cx, cy)
		if inverted && b.CanInvertPage(page) {
			img = render.Invert(img)
		}
		dir := os.Getenv("CBZR_SHOT_DIR")
		if dir == "" {
			dir, _ = os.Getwd()
		}
		name := fmt.Sprintf("cbzr-%s-p%03d.png", sanitize(b.Title), page+1)
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		if err != nil {
			return shotMsg{err: err}
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			return shotMsg{err: err}
		}
		return shotMsg{path: path}
	}
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case r == ' ':
			return '_'
		}
		return -1
	}, s)
}

// ---- OCR search -----------------------------------------------------------------

func (m *Model) cancelSearch() { m.find = search{gen: m.find.gen + 1} }

func (m Model) startSearch(term string) (tea.Model, tea.Cmd) {
	p := m.panes[m.active]
	if p.book == nil {
		return m, nil
	}
	m.find = search{
		gen:     m.find.gen + 1,
		pane:    m.active,
		term:    strings.ToLower(term),
		total:   p.book.Len(),
		running: true,
	}
	m.status = fmt.Sprintf("OCR search %q…", term)
	return m, m.ocrPage(m.find.gen, 0)
}

// ocrPage OCRs one page (cache-aside) and reports back; the update chains
// the next page, which keeps cancellation (gen bump) cheap.
func (m Model) ocrPage(gen, page int) tea.Cmd {
	p := m.panes[m.find.pane]
	if p.book == nil || page >= p.book.Len() {
		return nil
	}
	b := p.book
	if b.IsTextPage(page) {
		text := strings.Join(b.PageText(page), "\n")
		return func() tea.Msg { return ocrMsg{gen: gen, page: page, book: b, text: text} }
	}
	key := b.Path + "\x00" + strconv.Itoa(page)
	if text, ok := m.ocrText[key]; ok {
		return func() tea.Msg { return ocrMsg{gen: gen, page: page, book: b, text: text} }
	}
	if !ocr.Available() {
		return func() tea.Msg {
			return ocrMsg{gen: gen, page: page, book: b, err: fmt.Errorf("OCR needs tesseract on PATH")}
		}
	}
	return func() tea.Msg {
		img, err := b.Page(page)
		if err != nil {
			return ocrMsg{gen: gen, page: page, book: b, err: err}
		}
		text, err := ocr.Text(img)
		return ocrMsg{gen: gen, page: page, book: b, text: text, err: err}
	}
}

func (m Model) updateOCR(msg ocrMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.find.gen || !m.find.running {
		return m, nil
	}
	p := m.panes[m.find.pane]
	if p.book == nil || p.book != msg.book {
		m.find.running = false
		return m, nil
	}
	if msg.err == nil {
		key := p.book.Path + "\x00" + strconv.Itoa(msg.page)
		m.ocrText[key] = msg.text
		if strings.Contains(strings.ToLower(msg.text), m.find.term) {
			m.find.hits = append(m.find.hits, msg.page)
		}
	} else {
		m.find.skipped++
	}
	m.find.scanned = msg.page + 1
	if m.find.scanned >= m.find.total {
		m.find.running = false
		m.status = fmt.Sprintf("search done: %d hit(s) for %q · n/p to jump", len(m.find.hits), m.find.term)
		if m.find.skipped > 0 {
			m.status += fmt.Sprintf(" · %d page(s) not searched", m.find.skipped)
		}
		if len(m.find.hits) > 0 && m.active == m.find.pane {
			return m.gotoHit(1)
		}
		return m, nil
	}
	return m, m.ocrPage(msg.gen, msg.page+1)
}

func (m Model) gotoHit(dir int) (tea.Model, tea.Cmd) {
	if m.find.term == "" {
		m.status = "no search — press / first"
		return m, nil
	}
	if len(m.find.hits) == 0 {
		if m.find.running {
			m.status = fmt.Sprintf("OCR %d/%d · no hits yet", m.find.scanned, m.find.total)
		} else {
			m.status = fmt.Sprintf("no hits for %q", m.find.term)
		}
		return m, nil
	}
	p := m.panes[m.find.pane]
	if p.book == nil {
		return m, nil
	}
	cur := p.page
	next := -1
	if dir > 0 {
		for _, h := range m.find.hits {
			if h > cur {
				next = h
				break
			}
		}
		if next < 0 {
			next = m.find.hits[0] // wrap
		}
	} else {
		for i := len(m.find.hits) - 1; i >= 0; i-- {
			if m.find.hits[i] < cur {
				next = m.find.hits[i]
				break
			}
		}
		if next < 0 {
			next = m.find.hits[len(m.find.hits)-1] // wrap
		}
	}
	m.active = m.find.pane
	m.status = fmt.Sprintf("hit %s p.%d (%d total)", m.find.term, next+1, len(m.find.hits))
	return m.goTo(m.find.pane, next)
}

// ---- rendering commands ------------------------------------------------------

func frameReadyAfterPaint(target *pane, pane, slot, gen int, rerender bool) tea.Cmd {
	// Pace animation without assuming the terminal has painted the frame.
	book := target.book
	return tea.Tick(time.Second/targetFPS, func(time.Time) tea.Msg {
		return frameReadyMsg{target: target, book: book, pane: pane, slot: slot, gen: gen, rerender: rerender}
	})
}

func renderImageID(pane, slot, gen int) uint32 {
	id := uint32(pane + 1)
	if slot == 1 {
		id = 3
	}
	if gen%2 == 0 {
		id += 3
	}
	return id
}

func (m *Model) rerenderAll() tea.Cmd {
	var cmds []tea.Cmd
	for i := 0; i < m.paneCount(); i++ {
		// Keep the previous frame visible until its replacement is ready.
		if c := m.renderPane(i); c != nil {
			cmds = append(cmds, c)
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) renderPane(i int) tea.Cmd {
	p := m.panes[i]
	if p.book == nil || m.width == 0 {
		return nil
	}
	p.gen++
	p.loading = true
	gen, page, b := p.gen, p.page, p.book
	rot, zoom, cx, cy := p.rot, p.zoom, p.cx, p.cy
	inverted := p.inverted
	offset, scroll := p.webOffset, p.webScroll
	if m.webtoon {
		scroll = smoothWebtoonScroll(scroll)
	}
	p.webStep = scroll
	cols, rows := m.imgBox(i)
	top := p.textTop
	r := m.renderer
	webtoon := m.webtoon
	load := func(pg int) (image.Image, error) {
		if r.Name() == "halfblock" && b.IsTextPage(pg) {
			hint := "t to leave webtoon, e for browser, or -renderer=kitty in Kitty/Ghostty"
			if m.nativeAvailable {
				hint = "t to leave webtoon, e for browser, f for native, or -renderer=kitty in Kitty/Ghostty"
			}
			return nil, fmt.Errorf("EPUB text in webtoon needs pixel rendering: %s", hint)
		}
		img, err := b.Page(pg)
		if err == nil && webtoon && inverted && b.CanInvertPage(pg) {
			img = render.Invert(img)
		}
		return img, err
	}
	mk := func(slot, pg int) tea.Cmd {
		id := renderImageID(i, slot, gen)
		return func() tea.Msg {
			if !webtoon && r.Name() == "halfblock" && b.IsTextPage(pg) {
				res := textResult(b.PageText(pg), top, cols, rows)
				return renderedMsg{target: p, book: b, pane: i, slot: slot, gen: gen, page: pg, cols: cols, rows: rows, scroll: scroll, res: res}
			}
			var img image.Image
			var err error
			outPage, outOffset := pg, 0.0
			if webtoon {
				cw, ch := render.CellSize()
				img, outPage, outOffset, err = render.ComposeWebtoon(load, b.Len(), pg, offset, scroll, cols, rows, rot, cw, ch)
			} else {
				img, err = load(pg)
				if err == nil {
					img = render.Transform(img, rot, zoom, cx, cy)
				}
			}
			if err != nil {
				return renderedMsg{target: p, book: b, pane: i, slot: slot, gen: gen, page: outPage, cols: cols, rows: rows, offset: outOffset, scroll: scroll, err: err}
			}
			if !webtoon && inverted && b.CanInvertPage(pg) {
				img = render.Invert(img)
			}
			res, err := r.Render(img, id, cols, rows)
			return renderedMsg{target: p, book: b, pane: i, slot: slot, gen: gen, page: outPage, cols: cols, rows: rows, offset: outOffset, scroll: scroll, res: res, err: err}
		}
	}
	cmds := []tea.Cmd{mk(0, page)}
	if m.spreadActive() && page+1 < b.Len() {
		cmds = append(cmds, mk(1, page+1))
	} else {
		p.res2 = render.Result{}
		p.err2 = nil
	}
	return tea.Batch(cmds...)
}

// textWidth caps plain-text lines at 72 cells for readability.
func textWidth(cols int) int { return max(1, min(cols, 72)) }

func textLines(paragraphs []string, width int) []string {
	var lines []string
	for _, paragraph := range paragraphs {
		lines = append(lines, strings.Split(ansi.Wrap(safeText(paragraph), width, ""), "\n")...)
	}
	return lines
}

// textResult lays EPUB paragraphs out as terminal rows for renderers without
// pixel graphics, starting top rows down. The last row notes any overflow.
func textResult(paragraphs []string, top, cols, rows int) render.Result {
	width := textWidth(cols)
	all := textLines(paragraphs, width)
	top = max(0, min(top, len(all)-rows))
	lines := all[top:min(len(all), top+rows)]
	if rest := len(all) - top - len(lines); rest > 0 && rows > 1 {
		lines[len(lines)-1] = fmt.Sprintf("\u2193 %d more \u00b7 arrows scroll", rest+1)
	}
	for i, line := range lines {
		line = ansi.Truncate(line, width, "")
		lines[i] = line + strings.Repeat(" ", width-ansi.StringWidth(line))
	}
	return render.Result{Cols: width, Rows: len(lines), Lines: lines}
}

func (m Model) prefetch(i, page int) tea.Cmd {
	p := m.panes[i]
	if p.book == nil || page >= p.book.Len() || m.renderer.Name() == "halfblock" && p.book.IsTextPage(page) {
		return nil
	}
	b := p.book
	return func() tea.Msg {
		b.Page(page) //nolint:errcheck // warm the decode cache
		return nil
	}
}

// ---- view --------------------------------------------------------------------

var (
	titleActive   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	titleInactive = lipgloss.NewStyle().Faint(true)
	dim           = lipgloss.NewStyle().Faint(true)
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}
	switch m.mode {
	case modePick:
		head := titleActive.Render(fmt.Sprintf(" Open book → pane %d ", m.pickFor+1))
		hint := dim.Render(" enter select · h/l dirs · esc cancel")
		return head + "\n\n" + m.picker.View() + "\n" + hint
	case modeHelp:
		return m.helpView()
	case modeMenu:
		return m.menu.view(m.width, m.height, "")
	case modeMenuFilter:
		footer := titleActive.Render(" /"+safeText(m.menu.filter)+"▏") + dim.Render("  fuzzy filter · enter keep · esc clear")
		return m.menu.view(m.width, m.height, footer)
	case modeMenuRename:
		footer := titleActive.Render(" rename: "+safeText(m.input)+"▏") + dim.Render("  enter save · empty reverts to title · esc cancel")
		return m.menu.view(m.width, m.height, footer)
	}

	// Compose the frame manually: width libraries miscount kitty
	// placeholder runes, so panes are padded by known cell counts and
	// zipped row by row.
	var body string
	if m.split {
		left, right := m.paneLines(0), m.paneLines(1)
		sep := dim.Render("│")
		rows := make([]string, len(left))
		for r := range left {
			rows[r] = left[r] + sep + right[r]
		}
		body = strings.Join(rows, "\n")
	} else {
		body = strings.Join(m.paneLines(0), "\n")
	}
	// Graphics transmissions ride on line 0 as zero-width escapes: one
	// writer, one frame, no torn APC sequences.
	var oob strings.Builder
	for i := 0; i < m.paneCount(); i++ {
		oob.Write(m.panes[i].res.Transmit)
		oob.Write(m.panes[i].res2.Transmit)
	}
	if m.zen && m.mode != modeSearch {
		return oob.String() + body
	}
	return oob.String() + body + "\n" + m.statusView()
}

// centerCells centers a block of lines, each exactly cols terminal cells
// wide, in a w x h box. Padding is computed from cols, never from string
// measurement, so placeholder runes cannot skew it.
func centerCells(lines []string, cols, w, h int) []string {
	out := make([]string, h)
	gap := max(0, w-cols)
	lp := strings.Repeat(" ", gap/2)
	rp := strings.Repeat(" ", gap-gap/2)
	blank := strings.Repeat(" ", w)
	top := max(0, (h-len(lines))/2)
	for i := range out {
		j := i - top
		if j >= 0 && j < len(lines) {
			out[i] = lp + lines[j] + rp
		} else {
			out[i] = blank
		}
	}
	return out
}

// centerText centers a styled single-line string (safe to measure).
func centerText(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return centerCells([]string{s}, ansi.StringWidth(s), w, 1)[0]
}

// imgLines yields a rendered page centered in a w x h box, or nil when the
// result does not fit the box (a re-render is already in flight).
func imgLines(res render.Result, w, h int) []string {
	if res.Rows == 0 || res.Cols > w || res.Rows > h {
		return nil
	}
	return centerCells(res.Lines, res.Cols, w, h)
}

func (m Model) paneLines(i int) []string {
	p := m.panes[i]
	w, h := m.paneBox(i)

	title := "[empty]  press o to open"
	if p.book != nil {
		pages := fmt.Sprintf("%d/%d", p.page+1, p.book.Len())
		if m.spreadActive() && p.page+1 < p.book.Len() {
			pages = fmt.Sprintf("%d-%d/%d", p.page+1, p.page+2, p.book.Len())
		}
		title = fmt.Sprintf("%s  %s", safeText(p.book.Title), pages)
		if m.marks.Has(p.book.Path, p.page) {
			title = "🔖 " + title
		}
		var mods []string
		if chapters, err := p.book.Chapters(); err == nil {
			for j := len(chapters) - 1; j >= 0; j-- {
				if chapters[j].Page <= p.page {
					if name := strings.TrimSpace(safeText(chapters[j].Title)); name != "" {
						mods = append(mods, name)
					}
					break
				}
			}
		}
		if p.rot != 0 {
			mods = append(mods, fmt.Sprintf("%d°", p.rot*90))
		}
		if p.zoom > 1.001 {
			mods = append(mods, fmt.Sprintf("%.2gx", p.zoom))
		}
		if len(mods) > 0 {
			title += "  [" + strings.Join(mods, " ") + "]"
		}
		if p.loading {
			title += " …"
		}
	}
	ts := titleInactive
	if i == m.active {
		ts = titleActive
	}

	lines := make([]string, 0, h)
	body := h
	if !m.zen {
		lines = append(lines, centerText(ts.Render(ansi.Truncate(title, w, "…")), w))
		body--
	}

	msg := ""
	switch {
	case p.err != nil && (!m.spreadActive() || p.book == nil):
		msg = errStyle.Render(ansi.Truncate(safeText(p.err.Error()), max(1, w-2), "…"))
	case p.book == nil:
		msg = dim.Render("no book")
	}
	if msg != "" {
		return append(lines, centerCells([]string{msg}, ansi.StringWidth(msg), w, body)...)
	}

	if m.spreadActive() {
		hw := (w - 1) / 2
		wr := w - 1 - hw
		left := imgLines(p.res, hw, body)
		right := imgLines(p.res2, wr, body)
		if p.err != nil {
			text := errStyle.Render(ansi.Truncate(safeText(p.err.Error()), hw, "…"))
			left = centerCells([]string{text}, ansi.StringWidth(text), hw, body)
		} else if left == nil {
			left = centerCells([]string{dim.Render("loading…")}, 8, hw, body)
		}
		if p.err2 != nil {
			text := errStyle.Render(ansi.Truncate(safeText(p.err2.Error()), wr, "…"))
			right = centerCells([]string{text}, ansi.StringWidth(text), wr, body)
		} else if right == nil {
			right = centerCells([]string{""}, 0, wr, body)
		}
		for r := 0; r < body; r++ {
			lines = append(lines, left[r]+" "+right[r])
		}
		return lines
	}

	img := imgLines(p.res, w, body)
	if img == nil {
		img = centerCells([]string{dim.Render("loading…")}, 8, w, body)
	}
	return append(lines, img...)
}

func (m Model) statusView() string {
	if m.mode == modeSearch {
		return titleActive.Render(" /"+safeText(m.input)+"▏") + dim.Render("  OCR search · enter run · esc cancel")
	}
	left := " " + m.renderer.Name()
	if m.webtoon {
		left += "  ·  webtoon"
	}
	if m.count != "" {
		left += "  ·  " + m.count
	}
	if m.find.running {
		left += fmt.Sprintf("  ·  OCR %d/%d (%d hits)", m.find.scanned, m.find.total, len(m.find.hits))
	}
	if port := m.srv.Port(); port > 0 {
		left += fmt.Sprintf("  ·  :%d", port)
	}
	if m.status != "" {
		left += "  ·  " + safeText(m.status)
	}
	right := "j/k page e in-browser s spread R rotate tab chapters  ? help  q quit "
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return dim.Render(ansi.Truncate(left, m.width, "…"))
	}
	return dim.Render(left + strings.Repeat(" ", gap) + right)
}

func (m Model) helpView() string {
	help := `cbzr: terminal reader (.cbz/.cbr/.epub)
EPUB text: images in Kitty/Ghostty, plain text elsewhere (webtoon needs Kitty/Ghostty).

  j / k          next / prev page or spread (webtoon: one viewport; 2j for two)
  J / K          next / prev page or spread (webtoon: half a viewport)
  g / G          first / last page     (42G → page 42)
  f              open active book in native macOS fullscreen (f returns)
  z              toggle terminal focus mode (hide titles and status bar)
  t              toggle continuous webtoon mode (single pane)
  w              switch pane
  v              toggle split          (keeps the active pane)
  s              toggle two-page spread (single pane)
  tab            chapter menu          (EPUB headings/titles, ComicInfo.xml or folders)
  b              toggle bookmark on this page
  F              bookmarks menu
  S              screenshot page → PNG (CBZR_SHOT_DIR or cwd)
  R              rotate 90° cw
  i              toggle inversion (EPUB: text only; comics: whole page)
  + / -          zoom in / out
  0              reset zoom
  arrows         pan while zoomed · scroll plain-text EPUB pages
  /              search: EPUB text, OCR (tesseract) for images · n / p next / prev hit
  o / O          open file in pane / in split
  x              close pane
  e              open current book in browser (127.0.0.1:5xxxx)
  r              re-render
  ?              this help · any key to close
  q / ctrl+c     clear saved positions and quit
  Q              save reading positions and quit`
	// lipgloss.Place aligns every line separately; pad the block to one
	// width first so the columns stay put.
	lines := strings.Split(help, "\n")
	widest := 0
	for _, l := range lines {
		widest = max(widest, ansi.StringWidth(l))
	}
	for i, l := range lines {
		lines[i] = l + strings.Repeat(" ", widest-ansi.StringWidth(l))
	}
	block := strings.Join(lines, "\n")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
}

// safeText removes terminal controls from labels before styling.
func safeText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}
