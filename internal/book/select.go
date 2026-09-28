package book

import (
	"image"
	"net/url"
	"strings"

	"github.com/go-text/typesetting/di"
	"golang.org/x/image/math/fixed"
)

// Link is a hyperlink on a text page: an external URL, or the page a
// reference inside the book leads to.
type Link struct {
	URL  string
	Page int // -1 for external links
}

type epubLink struct {
	start, end int    // paragraph rune range
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

func (b *Book) textLines(i int) []epubLine {
	if !b.IsTextPage(i) {
		return nil
	}
	return b.pages[i].(epubTextPage).lines
}

func epubPageX(x float64) fixed.Int26_6 { return fixed.Int26_6(x * epubPageWidth * 64) }

// epubLineBox returns the vertical page extent of a line.
func epubLineBox(line epubLine) (top, bottom int) {
	ascent, descent := epubLineBounds(line)
	return line.y - ascent, line.y + descent
}

// epubLineAt picks the first line that ends at or below y, else the last.
func epubLineAt(lines []epubLine, y int) int {
	for i, line := range lines {
		if _, bottom := epubLineBox(line); y <= bottom {
			return i
		}
	}
	return len(lines) - 1
}

// epubGlyphs visits the glyphs of a line with their horizontal page extent
// and paragraph rune range.
func epubGlyphs(line epubLine, visit func(x0, x1 fixed.Int26_6, index, count int, rtl bool)) {
	for _, run := range line.runs {
		x := fixed.I(epubMargin) + run.x
		rtl := run.Direction.Progression() == di.TowardTopLeft
		for _, glyph := range run.Glyphs {
			visit(x, x+glyph.Advance, glyph.TextIndex(), glyph.RunesCount(), rtl)
			x += glyph.Advance
		}
	}
}

// epubSpan returns the horizontal extent of the glyphs in a rune range.
func epubSpan(line epubLine, from, to int) (x0, x1 fixed.Int26_6, ok bool) {
	epubGlyphs(line, func(gx0, gx1 fixed.Int26_6, index, count int, _ bool) {
		if index >= to || index+count <= from {
			return
		}
		if !ok || gx0 < x0 {
			x0 = gx0
		}
		if !ok || gx1 > x1 {
			x1 = gx1
		}
		ok = true
	})
	return
}

type epubCaretPos struct{ line, index int }

// epubCaret locates the rune boundary nearest to a point given as page
// fractions: a line and a paragraph rune index.
func epubCaret(lines []epubLine, x, y float64) epubCaretPos {
	n := epubLineAt(lines, int(y*epubPageHeight))
	line := lines[n]
	px := epubPageX(x)
	index, best := line.offset, fixed.Int26_6(-1)
	epubGlyphs(line, func(x0, x1 fixed.Int26_6, at, count int, rtl bool) {
		d := max(x0-px, px-x1, 0)
		if best >= 0 && d >= best {
			return
		}
		best = d
		after := px > (x0+x1)/2
		if rtl {
			after = !after
		}
		index = at
		if after {
			index += count
		}
	})
	return epubCaretPos{n, index}
}

// LinkAt returns the link under a point of a text page, given as fractions
// of the page width and height.
func (b *Book) LinkAt(i int, x, y float64) (Link, bool) {
	lines := b.textLines(i)
	if len(lines) == 0 {
		return Link{}, false
	}
	py := int(y * epubPageHeight)
	line := lines[epubLineAt(lines, py)]
	if top, bottom := epubLineBox(line); py < top || py > bottom {
		return Link{}, false
	}
	px := epubPageX(x)
	index := -1
	epubGlyphs(line, func(x0, x1 fixed.Int26_6, at, _ int, _ bool) {
		if px >= x0 && px < x1 {
			index = at
		}
	})
	for _, link := range line.links {
		if index < link.start || index >= link.end {
			continue
		}
		if !link.internal {
			return Link{URL: link.href, Page: -1}, true
		}
		if page, ok := b.anchors[link.href]; ok {
			return Link{Page: page}, true
		}
		doc, _, _ := strings.Cut(link.href, "#")
		page, ok := b.anchors[doc]
		return Link{Page: page}, ok
	}
	return Link{}, false
}

// Select returns the text between two points of a text page, given as page
// fractions, in reading order, with the page rectangles it covers.
func (b *Book) Select(i int, x0, y0, x1, y1 float64) (string, []image.Rectangle) {
	lines := b.textLines(i)
	if len(lines) == 0 {
		return "", nil
	}
	from, to := epubCaret(lines, x0, y0), epubCaret(lines, x1, y1)
	if to.line < from.line || to.line == from.line && to.index < from.index {
		from, to = to, from
	}
	var text strings.Builder
	var rects []image.Rectangle
	for n := from.line; n <= to.line; n++ {
		line := lines[n]
		runes := []rune(line.text)
		lo, hi := line.offset, line.offset+len(runes)
		if n == from.line {
			lo = max(lo, from.index)
		}
		if n == to.line {
			hi = min(hi, to.index)
		}
		if lo >= hi {
			continue
		}
		text.WriteString(string(runes[lo-line.offset : hi-line.offset]))
		if line.end && n < to.line {
			text.WriteString("\n")
		}
		if sx0, sx1, ok := epubSpan(line, lo, hi); ok {
			top, bottom := epubLineBox(line)
			rects = append(rects, image.Rect(sx0.Floor(), top, sx1.Ceil(), bottom))
		}
	}
	return strings.Trim(text.String(), " "), rects
}
