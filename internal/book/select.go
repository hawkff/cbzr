package book

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/go-text/typesetting/di"
	"golang.org/x/image/math/fixed"
)

// Link is a hyperlink on a page: an external URL, or the page a reference
// inside the book leads to.
type Link struct {
	URL  string
	Page int // -1 for external links
}

// Box is a rectangle in fractions of the page width and height.
type Box struct{ X0, Y0, X1, Y1 float64 }

type epubLink struct {
	start, end int    // rune range
	href       string // URL, or the anchor key of an internal reference
	internal   bool
}

// epubLinkTarget resolves an href to an external URL or an anchor key. Only
// web and mail links leave the book; other schemes are dropped.
func epubLinkTarget(name, href string) (target string, internal, ok bool) {
	href = strings.TrimSpace(href)
	u, err := url.Parse(href)
	if err != nil || href == "" {
		return "", false, false
	}
	if u.IsAbs() {
		switch u.Scheme {
		case "http", "https", "mailto":
			return href, false, true
		}
		return "", false, false
	}
	doc, _, _ := strings.Cut(href, "#")
	if doc == "" {
		doc = name
	} else if doc, err = epubPath(name, doc); err != nil {
		return "", false, false
	}
	if u.Fragment != "" {
		doc += "#" + u.Fragment // decoded, as ids are
	}
	return doc, true, true
}

// textBox is a glyph or word on a line, in page fractions.
type textBox struct {
	x0, x1       float64
	index, count int // rune range in the line text
	rtl          bool
}

// textLine is one line of selectable text on a page, in page fractions.
type textLine struct {
	text        string
	top, bottom float64
	end         bool // last line of its paragraph
	boxes       []textBox
	links       []epubLink // rune ranges in text
}

// Selectable reports whether page i carries a text layer: a page laid out
// from book text, or a PDF page.
func (b *Book) Selectable(i int) bool {
	if i < 0 || i >= len(b.pages) {
		return false
	}
	switch p := b.pages[i].(type) {
	case epubTextPage:
		return true
	case toolPage:
		return !p.djvu
	}
	return false
}

const maxTextLayers = 64

// lines caches book glyphs on first use. PDF hits only read prepared layers;
// they must not wait for external tools on the input loop.
func (b *Book) lines(i int) []textLine {
	if b.IsTextPage(i) {
		b.PrepareText(i)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.layers[i]
}

// TextReady reports whether page i has a cached layer, including an empty
// layer after a failed extraction.
func (b *Book) TextReady(i int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.layers[i]
	return ok
}

// PrepareText loads a bounded text layer cache. Concurrent preparations of
// the same page share one extraction. Call it from a command for PDF pages.
func (b *Book) PrepareText(i int) {
	if !b.Selectable(i) {
		return
	}
	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return
		}
		if _, ok := b.layers[i]; ok {
			b.mu.Unlock()
			return
		}
		if done := b.layerLoading[i]; done != nil {
			b.mu.Unlock()
			<-done
			continue
		}
		if b.layerLoading == nil {
			b.layerLoading = make(map[int]chan struct{})
		}
		done := make(chan struct{})
		b.layerLoading[i] = done
		ctx := b.contextLocked()
		b.mu.Unlock()

		var lines []textLine
		switch p := b.pages[i].(type) {
		case epubTextPage:
			lines = p.textLines()
		case toolPage:
			lines = pdfTextLayer(ctx, p.path, p.page)
		}

		b.mu.Lock()
		if !b.closed {
			if b.layers == nil {
				b.layers = make(map[int][]textLine)
			}
			b.layers[i] = lines
			b.layerOrder = append(b.layerOrder, i)
			for len(b.layerOrder) > maxTextLayers {
				delete(b.layers, b.layerOrder[0])
				b.layerOrder = b.layerOrder[1:]
			}
		}
		delete(b.layerLoading, i)
		close(done)
		b.mu.Unlock()
		return
	}
}

func epubFraction(x fixed.Int26_6) float64 { return float64(x) / 64 / epubPageWidth }

// epubLineBox returns the vertical page extent of a line.
func epubLineBox(line epubLine) (top, bottom int) {
	ascent, descent := epubLineBounds(line)
	return line.y - ascent, line.y + descent
}

// textLines converts the laid-out glyphs to the page-fraction model.
func (p epubTextPage) textLines() []textLine {
	lines := make([]textLine, 0, len(p.lines))
	for _, line := range p.lines {
		top, bottom := epubLineBox(line)
		out := textLine{text: line.text, top: float64(top) / epubPageHeight, bottom: float64(bottom) / epubPageHeight, end: line.end}
		for _, run := range line.runs {
			x := fixed.I(epubMargin) + run.x
			rtl := run.Direction.Progression() == di.TowardTopLeft
			for _, glyph := range run.Glyphs {
				out.boxes = append(out.boxes, textBox{epubFraction(x), epubFraction(x + glyph.Advance), glyph.TextIndex() - line.offset, glyph.RunesCount(), rtl})
				x += glyph.Advance
			}
		}
		for _, link := range line.links {
			out.links = append(out.links, epubLink{link.start - line.offset, link.end - line.offset, link.href, link.internal})
		}
		lines = append(lines, out)
	}
	return lines
}

// lineAt picks the first line that ends at or below y, else the last.
func lineAt(lines []textLine, y float64) int {
	for i, line := range lines {
		if y <= line.bottom {
			return i
		}
	}
	return len(lines) - 1
}

// span returns the horizontal extent of the boxes in a rune range.
func span(line textLine, from, to int) (x0, x1 float64, ok bool) {
	for _, box := range line.boxes {
		if box.index >= to || box.index+box.count <= from {
			continue
		}
		if !ok || box.x0 < x0 {
			x0 = box.x0
		}
		if !ok || box.x1 > x1 {
			x1 = box.x1
		}
		ok = true
	}
	return
}

type caretPos struct{ line, index int }

// caretAt locates the rune boundary nearest to a point: a line and a rune
// index in its text.
func caretAt(lines []textLine, x, y float64) caretPos {
	n := lineAt(lines, y)
	index, best := 0, -1.0
	for _, box := range lines[n].boxes {
		d := max(box.x0-x, x-box.x1, 0)
		if best >= 0 && d >= best {
			continue
		}
		best = d
		after := x > (box.x0+box.x1)/2
		if box.rtl {
			after = !after
		}
		index = box.index
		if after {
			index += box.count
		}
	}
	return caretPos{n, index}
}

// LinkAt returns the link under a point of a page, given as fractions of
// the page width and height. PDF pages need PrepareText first.
func (b *Book) LinkAt(i int, x, y float64) (Link, bool) {
	lines := b.lines(i)
	if len(lines) == 0 {
		return Link{}, false
	}
	line := lines[lineAt(lines, y)]
	if y < line.top || y > line.bottom {
		return Link{}, false
	}
	index := -1
	for _, box := range line.boxes {
		if x >= box.x0 && x < box.x1 {
			index = box.index
		}
	}
	for _, link := range line.links {
		if index >= link.start && index < link.end {
			return b.resolve(link)
		}
	}
	return Link{}, false
}

func (b *Book) resolve(link epubLink) (Link, bool) {
	if !link.internal {
		return Link{URL: link.href, Page: -1}, true
	}
	if page, ok := b.anchors[link.href]; ok {
		return Link{Page: page}, true
	}
	doc, fragment, _ := strings.Cut(link.href, "#")
	if page, ok := b.anchors[doc]; ok {
		return Link{Page: page}, true
	}
	// PDF references name their page.
	if n, err := strconv.Atoi(fragment); err == nil && doc == "" && n >= 1 && n <= len(b.pages) {
		return Link{Page: n - 1}, true
	}
	return Link{}, false
}

// Select returns the text between two points of a page, given as page
// fractions, in reading order, with the boxes it covers. PDF pages need
// PrepareText first.
func (b *Book) Select(i int, x0, y0, x1, y1 float64) (string, []Box) {
	lines := b.lines(i)
	if len(lines) == 0 {
		return "", nil
	}
	from, to := caretAt(lines, x0, y0), caretAt(lines, x1, y1)
	if to.line < from.line || to.line == from.line && to.index < from.index {
		from, to = to, from
	}
	var text strings.Builder
	var boxes []Box
	for n := from.line; n <= to.line; n++ {
		line := lines[n]
		runes := []rune(line.text)
		lo, hi := 0, len(runes)
		if n == from.line {
			lo = max(lo, from.index)
		}
		if n == to.line {
			hi = min(hi, to.index)
		}
		if lo >= hi {
			continue
		}
		text.WriteString(string(runes[lo:hi]))
		if line.end && n < to.line {
			text.WriteString("\n")
		}
		if sx0, sx1, ok := span(line, lo, hi); ok {
			boxes = append(boxes, Box{sx0, line.top, sx1, line.bottom})
		}
	}
	return strings.Trim(text.String(), " "), boxes
}
