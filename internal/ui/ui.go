// Package ui is the bubbletea model: two side-by-side reader panes with
// vim keybindings, chapter/bookmark menus, OCR search, and a browser
// hand-off.
package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/filepicker"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"cbzr/internal/book"
	"cbzr/internal/bookmarks"
	"cbzr/internal/ocr"
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
	zoomStep = 1.25
	zoomMax  = 8.0
)

type pane struct {
	book    *book.Book
	page    int
	res     render.Result
	err     error
	loading bool
	gen     int

	rot    int // quarter turns cw
	zoom   float64
	cx, cy float64 // view center (fractions of rotated image)
}

func newPane() *pane { return &pane{zoom: 1, cx: 0.5, cy: 0.5} }

func (p *pane) resetView() { p.zoom, p.cx, p.cy = 1, 0.5, 0.5 }

// search holds OCR search state for the active book.
type search struct {
	gen     int
	pane    int
	term    string
	hits    []int
	scanned int
	total   int
	running bool
}

// Model is the root bubbletea model.
type Model struct {
	renderer render.Renderer
	srv      *server.Server
	marks    *bookmarks.Store

	panes  [2]*pane
	active int
	split  bool

	width, height int
	mode          mode
	picker        filepicker.Model
	pickFor       int
	count         string
	status        string
	openBrowser   func(string) error

	menu    menu
	input   string // search / filter / rename input buffer
	find    search
	ocrText map[string]string // "path\x00page" -> text

	resizeGen int
}

type renderedMsg struct {
	pane, gen, page int
	cols, rows      int
	res             render.Result
	err             error
}

type prefetchedMsg struct{}

type shotMsg struct {
	path string
	err  error
}

type ocrMsg struct {
	gen, page int
	text      string
	err       error
}

type resizeSettledMsg struct{ gen int }

// New builds the model, opening up to two books given on the command line.
func New(r render.Renderer, srv *server.Server, marks *bookmarks.Store, openBrowser func(string) error, paths []string) Model {
	m := Model{
		renderer:    r,
		srv:         srv,
		marks:       marks,
		panes:       [2]*pane{newPane(), newPane()},
		openBrowser: openBrowser,
		ocrText:     map[string]string{},
	}
	for i, p := range paths {
		if i > 1 {
			break
		}
		b, err := book.Open(p)
		if err != nil {
			m.panes[i].err = err
			continue
		}
		m.panes[i].book = b
	}
	if m.panes[1].book != nil {
		m.split = true
	}

	fp := filepicker.New()
	fp.AllowedTypes = []string{".cbz", ".zip"}
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

// paneBox returns the outer size of pane i (excluding the status bar).
func (m Model) paneBox(i int) (w, h int) {
	h = max(1, m.height-1)
	if !m.split {
		return max(1, m.width), h
	}
	left := (m.width - 1) / 2
	if i == 0 {
		return max(1, left), h
	}
	return max(1, m.width-1-left), h
}

// imgBox returns the image area inside pane i.
func (m Model) imgBox(i int) (cols, rows int) {
	w, h := m.paneBox(i)
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
		if msg.gen != p.gen {
			return m, nil // stale
		}
		p.loading = false
		p.err = msg.err
		if msg.err == nil {
			// Transmit bytes are emitted inside View so that bubbletea's
			// renderer stays the only writer to the terminal.
			p.res = msg.res
		}
		// The box changed while this render was in flight (e.g. a resize
		// storm): render once more for the current geometry.
		if cols, rows := m.imgBox(msg.pane); cols != msg.cols || rows != msg.rows {
			return m, m.renderPane(msg.pane)
		}
		return m, m.prefetch(msg.pane, msg.page+1)

	case prefetchedMsg:
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
		return m.turn(target, 1)
	case tea.MouseButtonWheelUp:
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

	case "?":
		m.mode = modeHelp
		return m, nil

	// -- pages: j/k only --
	case "j":
		return m.turn(m.active, m.takeCount())
	case "k":
		return m.turn(m.active, -m.takeCount())
	case "g":
		return m.goTo(m.active, m.takeCountOr(1)-1)
	case "G":
		p := m.panes[m.active]
		if p.book == nil {
			return m, nil
		}
		return m.goTo(m.active, m.takeCountOr(p.book.Len())-1)

	// -- panes --
	case "w":
		if m.split {
			m.active = 1 - m.active
		}
		return m, nil
	case "v":
		return m.toggleSplit()
	case "x":
		return m.closePane(m.active)

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
		if m.marks.Toggle(p.book.Path, p.book.Title, p.page) {
			m.status = fmt.Sprintf("bookmarked %s p.%d", p.book.Title, p.page+1)
		} else {
			m.status = fmt.Sprintf("removed bookmark %s p.%d", p.book.Title, p.page+1)
		}
		return m, nil

	// -- view transforms --
	case "R":
		p := m.panes[m.active]
		if p.book == nil {
			return m, nil
		}
		p.rot = (p.rot + 1) & 3
		return m, m.renderPane(m.active)
	case "+", "=":
		return m.zoomBy(zoomStep)
	case "-":
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
		if !ocr.Available() {
			m.status = "OCR needs tesseract on PATH"
			return m, nil
		}
		m.mode = modeSearch
		m.input = ""
		return m, nil
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
		if !m.split {
			m.split = true
		}
		m.mode = modePick
		m.pickFor = 1
		return m, m.picker.Init()
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
		m.marks.Remove(it.path, it.page)
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
			m.marks.Rename(it.path, it.page, m.input)
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
		return m, nil
	case "enter":
		m.mode = modeRead
		term := strings.TrimSpace(m.input)
		if term == "" {
			return m, nil
		}
		return m.startSearch(term)
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
	return m.goTo(i, p.page+delta)
}

func (m Model) goTo(i, page int) (tea.Model, tea.Cmd) {
	p := m.panes[i]
	if p.book == nil {
		return m, nil
	}
	page = max(0, min(p.book.Len()-1, page))
	if page == p.page && p.res.Rows > 0 {
		return m, nil
	}
	p.page = page
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
	if p.book == nil || p.zoom <= 1.001 {
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

func (m Model) openBook(i int, path string, prev tea.Cmd) (tea.Model, tea.Cmd) {
	b, err := book.Open(path)
	p := m.panes[i]
	if err != nil {
		p.err = err
		return m, prev
	}
	if p.book != nil {
		p.book.Close() //nolint:errcheck
	}
	*p = *newPane()
	p.book = b
	m.syncServer()
	return m, tea.Batch(prev, m.renderPane(i))
}

func (m Model) toggleSplit() (tea.Model, tea.Cmd) {
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
	chs := p.book.Chapters()
	if len(chs) == 0 {
		m.status = "no chapter info in this book"
		return m, nil
	}
	items := make([]menuItem, len(chs))
	cursor := 0
	for i, c := range chs {
		items[i] = menuItem{
			label: fmt.Sprintf("%-40s p.%d", ansi.Truncate(c.Title, 40, "…"), c.Page+1),
			page:  c.Page,
		}
		if c.Page <= p.page {
			cursor = i
		}
	}
	m.menu = menu{kind: menuChapters, title: "Chapters — " + p.book.Title, all: items, cursor: cursor}
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
				ansi.Truncate(mk.Label(), 36, "…"), mk.Page+1, mk.Added.Format("2006-01-02")),
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
	return m, func() tea.Msg {
		img, err := b.Page(page)
		if err != nil {
			return shotMsg{err: err}
		}
		img = render.Transform(img, rot, zoom, cx, cy)
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
	key := b.Path + "\x00" + strconv.Itoa(page)
	if text, ok := m.ocrText[key]; ok {
		return func() tea.Msg { return ocrMsg{gen: gen, page: page, text: text} }
	}
	return func() tea.Msg {
		img, err := b.Page(page)
		if err != nil {
			return ocrMsg{gen: gen, page: page, err: err}
		}
		text, err := ocr.Text(img)
		return ocrMsg{gen: gen, page: page, text: text, err: err}
	}
}

func (m Model) updateOCR(msg ocrMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.find.gen || !m.find.running {
		return m, nil
	}
	p := m.panes[m.find.pane]
	if p.book == nil {
		m.find.running = false
		return m, nil
	}
	if msg.err == nil {
		key := p.book.Path + "\x00" + strconv.Itoa(msg.page)
		m.ocrText[key] = msg.text
		if strings.Contains(strings.ToLower(msg.text), m.find.term) {
			m.find.hits = append(m.find.hits, msg.page)
		}
	}
	m.find.scanned = msg.page + 1
	if m.find.scanned >= m.find.total {
		m.find.running = false
		m.status = fmt.Sprintf("OCR done: %d hit(s) for %q · n/p to jump", len(m.find.hits), m.find.term)
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

func (m *Model) rerenderAll() tea.Cmd {
	var cmds []tea.Cmd
	for i := 0; i < m.paneCount(); i++ {
		// Drop stale results so old placeholder grids never linger and new
		// transmit bytes always differ from the previous frame.
		m.panes[i].res = render.Result{}
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
	cols, rows := m.imgBox(i)
	r := m.renderer
	id := uint32(i + 1)
	return func() tea.Msg {
		img, err := b.Page(page)
		if err != nil {
			return renderedMsg{pane: i, gen: gen, page: page, cols: cols, rows: rows, err: err}
		}
		img = render.Transform(img, rot, zoom, cx, cy)
		res, err := r.Render(img, id, cols, rows)
		return renderedMsg{pane: i, gen: gen, page: page, cols: cols, rows: rows, res: res, err: err}
	}
}

func (m Model) prefetch(i, page int) tea.Cmd {
	p := m.panes[i]
	if p.book == nil || page >= p.book.Len() {
		return nil
	}
	b := p.book
	return func() tea.Msg {
		b.Page(page) //nolint:errcheck // warm the decode cache
		return prefetchedMsg{}
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
		head := titleActive.Render(fmt.Sprintf(" Open .cbz → pane %d ", m.pickFor+1))
		hint := dim.Render(" enter select · h/l dirs · esc cancel")
		return head + "\n\n" + m.picker.View() + "\n" + hint
	case modeHelp:
		return m.helpView()
	case modeMenu:
		return m.menu.view(m.width, m.height, "")
	case modeMenuFilter:
		footer := titleActive.Render(" /"+m.menu.filter+"▏") + dim.Render("  fuzzy filter · enter keep · esc clear")
		return m.menu.view(m.width, m.height, footer)
	case modeMenuRename:
		footer := titleActive.Render(" rename: "+m.input+"▏") + dim.Render("  enter save · empty reverts to title · esc cancel")
		return m.menu.view(m.width, m.height, footer)
	}

	paneViews := make([]string, 0, 3)
	for i := 0; i < m.paneCount(); i++ {
		paneViews = append(paneViews, m.paneView(i))
		if m.split && i == 0 {
			_, h := m.paneBox(0)
			sep := strings.TrimSuffix(strings.Repeat("│\n", h), "\n")
			paneViews = append(paneViews, dim.Render(sep))
		}
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, paneViews...)
	// Graphics transmissions ride on line 0 as zero-width escapes: one
	// writer, one frame, no torn APC sequences.
	var oob strings.Builder
	for i := 0; i < m.paneCount(); i++ {
		oob.Write(m.panes[i].res.Transmit)
	}
	return oob.String() + body + "\n" + m.statusView()
}

func (m Model) paneView(i int) string {
	p := m.panes[i]
	w, h := m.paneBox(i)

	title := "[empty]  press o to open"
	if p.book != nil {
		title = fmt.Sprintf("%s  %d/%d", p.book.Title, p.page+1, p.book.Len())
		if m.marks.Has(p.book.Path, p.page) {
			title = "🔖 " + title
		}
		var mods []string
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
	titleLine := lipgloss.PlaceHorizontal(w, lipgloss.Center, ts.Render(ansi.Truncate(title, w, "…")))

	var content string
	switch {
	case p.err != nil:
		content = errStyle.Render(ansi.Truncate(p.err.Error(), max(1, w-2), "…"))
	case p.book == nil:
		content = dim.Render("no book")
	case p.res.Rows == 0:
		content = dim.Render("loading…")
	default:
		content = strings.Join(p.res.Lines, "\n")
	}
	img := lipgloss.Place(w, h-1, lipgloss.Center, lipgloss.Center, content)
	return titleLine + "\n" + img
}

func (m Model) statusView() string {
	if m.mode == modeSearch {
		return titleActive.Render(" /" + m.input + "▏") + dim.Render("  OCR search · enter run · esc cancel")
	}
	left := " " + m.renderer.Name()
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
		left += "  ·  " + m.status
	}
	right := "j/k pages  v split  w pane  tab chapters  / find  ? help  q quit "
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return dim.Render(ansi.Truncate(left, m.width, "…"))
	}
	return dim.Render(left + strings.Repeat(" ", gap) + right)
}

func (m Model) helpView() string {
	help := `cbzr — terminal .cbz reader

  j / k          next / prev page      (counts work: 5j)
  g / G          first / last page     (42G → page 42)
  w              switch pane
  v              toggle split          (keeps the active pane)
  tab            chapter menu          (ComicInfo.xml or folders)
  b              toggle bookmark on this page
  F              bookmarks menu
  S              screenshot page → PNG (CBZR_SHOT_DIR or cwd)
  R              rotate 90° cw
  + / -          zoom in / out
  0              reset zoom
  arrows         pan while zoomed
  /              OCR search (tesseract) · n / p next / prev hit
  o / O          open file in pane / in split
  x              close pane
  e              open current book in browser (localhost:5xxxx)
  r              re-render
  ?              this help · any key to close
  q              quit`
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
