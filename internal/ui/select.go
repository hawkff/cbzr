package ui

import (
	"encoding/base64"
	"fmt"
	"image"
	"math"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"cbzr/internal/book"
)

// selection spans two points of one page, as page fractions.
type selection struct {
	page           int
	x0, y0, x1, y1 float64
}

// hit is a terminal cell resolved to a point on the page shown there.
type hit struct {
	pane, slot, page int
	x, y             float64 // page fractions
}

type clipDoneMsg struct{ gen int }
type hoverMsg string

// FilterMouse drops unchanged hover events before Bubble Tea rebuilds the view.
func FilterMouse(model tea.Model, msg tea.Msg) tea.Msg {
	m, ok := model.(Model)
	mouse, motion := msg.(tea.MouseMsg)
	if !ok || !motion || mouse.Action != tea.MouseActionMotion || mouse.Button != tea.MouseButtonNone {
		return msg
	}
	if m.mode != modeRead {
		return nil
	}
	hover := m.hoverAt(mouse.X, mouse.Y)
	if hover == m.hover {
		return nil
	}
	return hoverMsg(hover)
}

func (m Model) hoverAt(x, y int) string {
	if _, link, ok := m.linkAt(x, y); ok {
		return linkLabel(link)
	}
	return ""
}

// prepareTextMsg asks for the text layer of a page that stayed on screen.
type prepareTextMsg struct {
	target          *pane
	book            *book.Book
	page, slot, gen int
}

// prepareText delays PDF extraction until a page stays in either spread slot.
func (m Model) prepareText(i, slot int) tea.Cmd {
	p := m.panes[i]
	page := p.shown[slot]
	if p.book == nil || m.webtoon || p.rot != 0 || p.book.IsTextPage(page) || !p.book.Selectable(page) || p.book.TextReady(page) {
		return nil
	}
	msg := prepareTextMsg{target: p, book: p.book, page: page, slot: slot, gen: p.gen}
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return msg })
}

// pixelRects converts page boxes to pixels of an image of the page.
func pixelRects(boxes []book.Box, bounds image.Rectangle) []image.Rectangle {
	w, h := float64(bounds.Dx()), float64(bounds.Dy())
	rects := make([]image.Rectangle, len(boxes))
	for i, b := range boxes {
		rects[i] = image.Rect(int(b.X0*w), int(b.Y0*h), int(math.Ceil(b.X1*w)), int(math.Ceil(b.Y1*h))).Add(bounds.Min)
	}
	return rects
}

func (m Model) slotCount() int {
	if m.spreadActive() {
		return 2
	}
	return 1
}

// slotBox returns the cell rectangle that centers one page of pane i: the
// pane body below its title, halved in spread mode.
func (m Model) slotBox(i, slot int) image.Rectangle {
	w, h := m.paneBox(i)
	x0, y0 := 0, 0
	if i == 1 {
		x0 = (m.width-1)/2 + 1
	}
	if !m.zen {
		y0, h = 1, h-1
	}
	if m.spreadActive() {
		hw := (w - 1) / 2
		if slot == 1 {
			x0, w = x0+hw+1, w-1-hw
		} else {
			w = hw
		}
	}
	return image.Rect(x0, y0, x0+w, y0+h)
}

// pageFraction maps a cell to fractions of the page shown in a slot when it
// has a text layer. With snap, cells outside the image move to its edge so a
// drag can run past it. Rotated, webtoon and plain-text pages have no mapping.
func (m Model) pageFraction(i, slot, x, y int, snap bool) (float64, float64, bool) {
	p := m.panes[i]
	res, page := p.res, p.shown[slot]
	if slot == 1 {
		res = p.res2
	}
	if p.book == nil || m.webtoon || p.rot != 0 || res.Rows == 0 || !p.book.Selectable(page) || p.book.IsTextPage(page) && m.renderer.Name() != "kitty" {
		return 0, 0, false
	}
	box := m.slotBox(i, slot)
	if res.Cols > box.Dx() || res.Rows > box.Dy() {
		return 0, 0, false
	}
	img := image.Rect(0, 0, res.Cols, res.Rows).Add(box.Min.Add(image.Pt((box.Dx()-res.Cols)/2, (box.Dy()-res.Rows)/2)))
	if snap {
		x = min(max(x, img.Min.X), img.Max.X-1)
		y = min(max(y, img.Min.Y), img.Max.Y-1)
	} else if !image.Pt(x, y).In(img) {
		return 0, 0, false
	}
	fx := (float64(x-img.Min.X) + 0.5) / float64(res.Cols)
	fy := (float64(y-img.Min.Y) + 0.5) / float64(res.Rows)
	// Undo the zoom crop of render.Transform.
	view := 1 / p.zoom
	fx = clamp(p.cx-view/2, 0, 1-view) + fx*view
	fy = clamp(p.cy-view/2, 0, 1-view) + fy*view
	return fx, fy, true
}

// hitAt resolves a cell to the text page under it.
func (m Model) hitAt(x, y int) (hit, bool) {
	for i := 0; i < m.paneCount(); i++ {
		for slot := 0; slot < m.slotCount(); slot++ {
			if !image.Pt(x, y).In(m.slotBox(i, slot)) {
				continue
			}
			fx, fy, ok := m.pageFraction(i, slot, x, y, false)
			return hit{pane: i, slot: slot, page: m.panes[i].shown[slot], x: fx, y: fy}, ok
		}
	}
	return hit{}, false
}

func (m Model) linkAt(x, y int) (int, book.Link, bool) {
	h, ok := m.hitAt(x, y)
	if !ok {
		return 0, book.Link{}, false
	}
	link, ok := m.panes[h.pane].book.LinkAt(h.page, h.x, h.y)
	return h.pane, link, ok
}

func linkLabel(link book.Link) string {
	if link.URL == "" {
		return fmt.Sprintf("p.%d", link.Page+1)
	}
	return book.CleanURL(link.URL)
}

// press anchors a selection on the page under the cursor and clears the
// previous highlight.
func (m Model) press(x, y int) (tea.Model, tea.Cmd) {
	m.pressX, m.pressY, m.drag = x, y, nil
	h, ok := m.hitAt(x, y)
	if !ok {
		return m, nil
	}
	m.drag = &h
	p := m.panes[h.pane]
	had := p.sel != nil
	p.sel = &selection{page: h.page, x0: h.x, y0: h.y, x1: h.x, y1: h.y}
	if had {
		return m, m.renderPane(h.pane)
	}
	return m, nil
}

// dragTo extends the selection and redraws it once the current frame is out.
func (m Model) dragTo(x, y int) (tea.Model, tea.Cmd) {
	h := *m.drag
	fx, fy, ok := m.pageFraction(h.pane, h.slot, x, y, true)
	p := m.panes[h.pane]
	if !ok || p.sel == nil || p.sel.x1 == fx && p.sel.y1 == fy {
		return m, nil
	}
	p.sel.x1, p.sel.y1 = fx, fy
	if p.loading {
		p.selDirty = true
		return m, nil
	}
	return m, m.renderPane(h.pane)
}

// release copies the selection, or follows the link under a plain click.
func (m Model) release(x, y int) (tea.Model, tea.Cmd) {
	h := *m.drag
	m.drag = nil
	p := m.panes[h.pane]
	if p.book == nil {
		return m, nil
	}
	if x == m.pressX && y == m.pressY {
		p.sel = nil
		return m.textAction(h, nil, false)
	}
	if p.sel == nil {
		return m, nil
	}
	return m.textAction(h, p.sel, false)
}

// textActionMsg keeps one click or selection while its PDF layer loads.
type textActionMsg struct {
	hit      hit
	target   *pane
	book     *book.Book
	gen      int
	sel      *selection
	copyLink bool
	prepared bool
}

func (m Model) textAction(h hit, sel *selection, copyLink bool) (tea.Model, tea.Cmd) {
	p := m.panes[h.pane]
	msg := textActionMsg{hit: h, target: p, book: p.book, gen: m.actionGen, copyLink: copyLink}
	if sel != nil {
		snapshot := *sel
		msg.sel = &snapshot
	}
	if !p.book.IsTextPage(h.page) && !p.book.TextReady(h.page) {
		msg.prepared = true
		m.status = "loading text…"
		return m, func() tea.Msg {
			msg.book.PrepareText(h.page)
			return msg
		}
	}
	return m.finishTextAction(msg)
}

func (m Model) finishTextAction(msg textActionMsg) (tea.Model, tea.Cmd) {
	h := msg.hit
	p := m.panes[h.pane]
	if msg.gen != m.actionGen || h.pane >= m.paneCount() || p != msg.target || p.book != msg.book || h.slot >= m.slotCount() || p.shown[h.slot] != h.page {
		return m, nil
	}
	m.status = ""
	if s := msg.sel; s != nil {
		text, _ := msg.book.Select(h.page, s.x0, s.y0, s.x1, s.y1)
		if text == "" {
			return m, nil
		}
		updated, cmd := m.copyText(text, fmt.Sprintf("copied %d characters", utf8.RuneCountInString(text)))
		if msg.prepared {
			if p.loading {
				p.selDirty = true
			} else {
				cmd = tea.Batch(cmd, m.renderPane(h.pane))
			}
		}
		return updated, cmd
	}
	link, ok := msg.book.LinkAt(h.page, h.x, h.y)
	if !ok {
		return m, nil
	}
	if !msg.copyLink {
		return m.follow(h.pane, link)
	}
	if link.URL == "" {
		return m, nil
	}
	u := book.CleanURL(link.URL)
	return m.copyText(u, "copied "+u)
}

func (m Model) follow(i int, link book.Link) (tea.Model, tea.Cmd) {
	if link.URL == "" {
		return m.goTo(i, link.Page)
	}
	u := book.CleanURL(link.URL)
	m.status = "→ " + u
	if m.openBrowser != nil {
		if err := m.openBrowser(u); err != nil {
			m.status = u + "  (open failed: " + err.Error() + ")"
		}
	}
	return m, nil
}

// copyLink puts the cleaned URL under the cursor on the clipboard.
func (m Model) copyLink(x, y int) (tea.Model, tea.Cmd) {
	h, ok := m.hitAt(x, y)
	if !ok {
		return m, nil
	}
	return m.textAction(h, nil, true)
}

// copyText sets the clipboard through OSC 52. The sequence rides on the next
// frame like the graphics transmissions and drops out again shortly after,
// so a later repaint of that line cannot overwrite a newer clipboard.
func (m Model) copyText(text, status string) (tea.Model, tea.Cmd) {
	m.clip = []byte("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07")
	m.clipGen++
	m.status = status
	gen := m.clipGen
	return m, tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return clipDoneMsg{gen} })
}
