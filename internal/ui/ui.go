// Package ui is the bubbletea model: two side-by-side reader panes with
// vim keybindings, a file picker, and a browser hand-off.
package ui

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/filepicker"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"cbzr/internal/book"
	"cbzr/internal/render"
	"cbzr/internal/server"
)

type mode int

const (
	modeRead mode = iota
	modePick
	modeHelp
)

type pane struct {
	book    *book.Book
	page    int
	res     render.Result
	err     error
	loading bool
	gen     int
}

// Model is the root bubbletea model.
type Model struct {
	renderer render.Renderer
	tty      io.Writer
	srv      *server.Server

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
}

type renderedMsg struct {
	pane, gen, page int
	res             render.Result
	err             error
}

type prefetchedMsg struct{}

// New builds the model, opening up to two books given on the command line.
func New(r render.Renderer, tty io.Writer, srv *server.Server, openBrowser func(string) error, paths []string) Model {
	m := Model{
		renderer:    r,
		tty:         tty,
		srv:         srv,
		panes:       [2]*pane{{}, {}},
		openBrowser: openBrowser,
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
		return m, m.rerenderAll()

	case renderedMsg:
		p := m.panes[msg.pane]
		if msg.gen != p.gen {
			return m, nil // stale
		}
		p.loading = false
		p.err = msg.err
		if msg.err == nil {
			p.res = msg.res
			if len(msg.res.Transmit) > 0 && m.tty != nil {
				m.tty.Write(msg.res.Transmit) //nolint:errcheck
			}
		}
		return m, m.prefetch(msg.pane, msg.page+1)

	case prefetchedMsg:
		return m, nil

	case tea.MouseMsg:
		return m.updateMouse(msg)

	case tea.KeyMsg:
		switch m.mode {
		case modePick:
			return m.updatePicker(msg)
		case modeHelp:
			m.mode = modeRead
			return m, nil
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

	case "l", "right", " ", "n", "j", "down":
		return m.turn(m.active, m.takeCount())

	case "h", "left", "p", "k", "up":
		return m.turn(m.active, -m.takeCount())

	case "g", "home":
		return m.goTo(m.active, m.takeCountOr(1)-1)

	case "G", "end":
		p := m.panes[m.active]
		if p.book == nil {
			return m, nil
		}
		n := m.takeCountOr(p.book.Len())
		return m.goTo(m.active, n-1)

	case "tab", "w", "ctrl+w":
		if m.split {
			m.active = 1 - m.active
		}
		return m, nil

	case "s", "v":
		return m.toggleSplit()

	case "x":
		return m.closePane(m.active)

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

	case "b":
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
	return m, m.renderPane(i)
}

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
	p.book = b
	p.page = 0
	p.res = render.Result{}
	p.err = nil
	m.syncServer()
	return m, tea.Batch(prev, m.renderPane(i))
}

func (m Model) toggleSplit() (tea.Model, tea.Cmd) {
	if m.split {
		// :only — keep the active pane's book in pane 0.
		if m.active == 1 {
			m.closeBook(0)
			m.panes[0], m.panes[1] = m.panes[1], &pane{}
		} else {
			m.closeBook(1)
			m.panes[1] = &pane{}
		}
		m.split = false
		m.active = 0
		m.deleteImage(2)
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
	m.panes[i] = &pane{}
	if m.split {
		// Keep the surviving book in pane 0.
		if i == 0 {
			m.panes[0], m.panes[1] = m.panes[1], &pane{}
		}
		m.split = false
		m.active = 0
		m.deleteImage(2)
	}
	m.syncServer()
	return m, m.rerenderAll()
}

func (m *Model) closeBook(i int) {
	if m.panes[i].book != nil {
		m.panes[i].book.Close() //nolint:errcheck
	}
}

func (m *Model) deleteImage(id uint32) {
	if b := m.renderer.Delete(id); len(b) > 0 && m.tty != nil {
		m.tty.Write(b) //nolint:errcheck
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

// ---- rendering commands ------------------------------------------------------

func (m *Model) rerenderAll() tea.Cmd {
	var cmds []tea.Cmd
	for i := 0; i < m.paneCount(); i++ {
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
	cols, rows := m.imgBox(i)
	r := m.renderer
	id := uint32(i + 1)
	return func() tea.Msg {
		img, err := b.Page(page)
		if err != nil {
			return renderedMsg{pane: i, gen: gen, page: page, err: err}
		}
		res, err := r.Render(img, id, cols, rows)
		return renderedMsg{pane: i, gen: gen, page: page, res: res, err: err}
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
	return body + "\n" + m.statusView()
}

func (m Model) paneView(i int) string {
	p := m.panes[i]
	w, h := m.paneBox(i)

	title := "[empty]  press o to open"
	if p.book != nil {
		title = fmt.Sprintf("%s  %d/%d", p.book.Title, p.page+1, p.book.Len())
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
	left := " " + m.renderer.Name()
	if m.count != "" {
		left += "  ·  " + m.count
	}
	if port := m.srv.Port(); port > 0 {
		left += fmt.Sprintf("  ·  http://127.0.0.1:%d/", port)
	}
	if m.status != "" {
		left += "  ·  " + m.status
	}
	right := "h/l pages  s split  tab pane  o open  b browser  ? help  q quit "
	gap := m.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return dim.Render(ansi.Truncate(left, m.width, "…"))
	}
	return dim.Render(left + strings.Repeat(" ", gap) + right)
}

func (m Model) helpView() string {
	help := `cbzr — terminal .cbz reader

  h l ← →        prev / next page      (counts work: 5l)
  j k ↓ ↑        next / prev page
  space, n / p   next / prev page
  g / G          first / last page     (42G → page 42)
  tab, w         switch pane
  s, v           toggle split          (keeps the active pane)
  o / O          open file in pane / in split
  x              close pane
  b              open current book in browser (localhost:5xxxx)
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
