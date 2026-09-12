package book

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"math"
	"strings"

	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

const (
	epubPageWidth  = 900
	epubPageHeight = 1200
	epubMargin     = 60
)

type epubRun struct {
	shaping.Output
	font *font.Font
	x    fixed.Int26_6
}

type epubLine struct {
	text        string
	y           int
	end         bool // last line of its paragraph
	runs        []epubRun
	annotations []epubLine
}
type epubTextPage struct{ lines []epubLine }

func (p epubTextPage) Name() string { return "epub-text.png" }
func (p epubTextPage) Open() (io.ReadCloser, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, epubPageWidth, epubPageHeight))
	draw.Draw(canvas, canvas.Bounds(), image.White, image.Point{}, draw.Src)
	// Retain glyph IDs and positions from pagination; only raster caches are private.
	faces := map[*font.Font]*font.Face{}
	raster := vector.NewRasterizer(0, 0)
	var paint func(epubLine) error
	paint = func(line epubLine) error {
		for _, run := range line.runs {
			face := faces[run.font]
			if face == nil {
				face = font.NewFace(run.font)
				faces[run.font] = face
			}
			scale := float32(run.Size) / 64 / float32(face.Upem())
			x := float32(epubMargin) + float32(run.x)/64
			for _, glyph := range run.Glyphs {
				if glyph.GlyphID != font.EmptyGlyph {
					outline, ok := face.GlyphDataOutline(glyph.GlyphID)
					ox := x + float32(glyph.XOffset)/64
					oy := float32(line.y) - float32(glyph.YOffset)/64
					bounds := image.Rect(
						int(math.Floor(float64(ox+float32(glyph.XBearing)/64)))-1,
						int(math.Floor(float64(oy-float32(glyph.YBearing)/64)))-1,
						int(math.Ceil(float64(ox+float32(glyph.XBearing+glyph.Width)/64)))+1,
						int(math.Ceil(float64(oy-float32(glyph.YBearing+glyph.Height)/64)))+1,
					).Intersect(canvas.Bounds())
					if !ok || len(outline.Segments) == 0 || bounds.Empty() {
						x += float32(glyph.Advance) / 64
						continue
					}
					ox -= float32(bounds.Min.X)
					oy -= float32(bounds.Min.Y)
					raster.Reset(bounds.Dx(), bounds.Dy())
					for _, segment := range outline.Segments {
						points := segment.Args
						for i := range points {
							points[i].X = ox + points[i].X*scale
							points[i].Y = oy - points[i].Y*scale
						}
						switch segment.Op {
						case opentype.SegmentOpMoveTo:
							raster.ClosePath()
							raster.MoveTo(points[0].X, points[0].Y)
						case opentype.SegmentOpLineTo:
							raster.LineTo(points[0].X, points[0].Y)
						case opentype.SegmentOpQuadTo:
							raster.QuadTo(points[0].X, points[0].Y, points[1].X, points[1].Y)
						case opentype.SegmentOpCubeTo:
							raster.CubeTo(points[0].X, points[0].Y, points[1].X, points[1].Y, points[2].X, points[2].Y)
						}
					}
					raster.ClosePath()
					raster.Draw(canvas, bounds, image.Black, image.Point{})
				}
				x += float32(glyph.Advance) / 64
			}
		}
		for _, annotation := range line.annotations {
			if err := paint(annotation); err != nil {
				return err
			}
		}
		return nil
	}
	for _, line := range p.lines {
		if err := paint(line); err != nil {
			return nil, err
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, canvas); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data.Bytes())), nil
}

// IsTextPage identifies EPUB text pages, which render as images or plain text.
func (b *Book) IsTextPage(i int) bool {
	if i < 0 || i >= len(b.pages) {
		return false
	}
	_, ok := b.pages[i].(epubTextPage)
	return ok
}

// HasText reports whether the book has EPUB text pages.
func (b *Book) HasText() bool {
	for i := range b.pages {
		if b.IsTextPage(i) {
			return true
		}
	}
	return false
}

// PageText returns the paragraphs of an EPUB text page, nil for image pages.
func (b *Book) PageText(i int) []string {
	if !b.IsTextPage(i) {
		return nil
	}
	var paragraphs []string
	var current strings.Builder
	for _, line := range b.pages[i].(epubTextPage).lines {
		current.WriteString(line.text)
		if line.end {
			paragraphs = append(paragraphs, strings.Trim(current.String(), " "))
			current.Reset()
		}
	}
	if current.Len() > 0 {
		paragraphs = append(paragraphs, strings.Trim(current.String(), " "))
	}
	return paragraphs
}

// epubIndent keeps pre indentation as no-break spaces, which the paragraph
// writer and the fonts preserve.
var epubIndent = strings.NewReplacer("\t", "\u00a0\u00a0\u00a0\u00a0", " ", "\u00a0")

// epubPreLine prepares one line of a pre block; interior blank lines stay blank.
func epubPreLine(line string, interior bool) string {
	if strings.TrimSpace(line) == "" {
		if interior {
			return "\u00a0"
		}
		return ""
	}
	lead := len(line) - len(strings.TrimLeft(line, " \t"))
	return epubIndent.Replace(line[:lead]) + line[lead:]
}

type epubLayout struct {
	book                              *Book
	regular, bold, italic, boldItalic []*font.Face
	shaper                            shaping.HarfbuzzShaper
	segmenter                         shaping.Segmenter
	lines                             []epubLine
	y                                 int
	activeImages                      map[string]bool
}

func newEPUBLayout(b *Book) (*epubLayout, error) {
	faces, err := epubFaces()
	if err != nil {
		return nil, err
	}
	return &epubLayout{book: b, regular: faces[0], bold: faces[1], italic: faces[2], boldItalic: faces[3], y: epubMargin, activeImages: map[string]bool{}}, nil
}
func (l *epubLayout) addPage(e entry) error {
	if len(l.book.pages) >= maxEPUBPages {
		return fmt.Errorf("EPUB exceeds %d pages", maxEPUBPages)
	}
	l.book.pages = append(l.book.pages, e)
	return nil
}
func (l *epubLayout) flushPage() error {
	if len(l.lines) == 0 {
		l.y = epubMargin
		return nil
	}
	if err := l.addPage(epubTextPage{lines: l.lines}); err != nil {
		return err
	}
	l.lines = nil
	l.y = epubMargin
	return nil
}
func (l *epubLayout) addImage(e *epubPackage, name string, resources map[string]string) error {
	media := resources[name]
	if media == "" {
		return fmt.Errorf("EPUB image is not in manifest: %s", name)
	}
	if media == "image/svg+xml" {
		if l.activeImages[name] || len(l.activeImages) >= maxEPUBXMLDepth {
			return fmt.Errorf("EPUB SVG resource cycle or traversal limit: %s", name)
		}
		l.activeImages[name] = true
		defer delete(l.activeImages, name)
		doc, err := e.document(name, maxEPUBDocumentBytes)
		if err != nil {
			return err
		}
		if doc.name.Local != "svg" || doc.name.Space != "http://www.w3.org/2000/svg" {
			return fmt.Errorf("EPUB image is not an SVG wrapper: %s", name)
		}
		if err := validateEPUBReferences(name, doc); err != nil {
			return err
		}
		return l.svg(e, name, doc, resources)
	}
	switch media {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp":
	default:
		return fmt.Errorf("EPUB unsupported image resource: %s", name)
	}
	if e.files[name] == nil {
		return fmt.Errorf("EPUB missing resource: %s", name)
	}
	if e.encrypted[name] {
		return fmt.Errorf("EPUB required resource is encrypted: %s", name)
	}
	if err := l.flushPage(); err != nil {
		return err
	}
	return l.addPage(e.files[name])
}
func epubBlock(name string) bool {
	switch name {
	case "p", "div", "section", "article", "blockquote", "li", "ul", "ol", "dl", "dt", "dd", "pre", "h1", "h2", "h3", "h4", "h5", "h6", "tr", "td", "th", "figure", "figcaption", "hr":
		return true
	}
	return false
}
func (l *epubLayout) document(e *epubPackage, name string, doc *epubNode, resources map[string]string) error {
	if err := validateEPUBReferences(name, doc); err != nil {
		return err
	}
	if doc.name.Local == "svg" && doc.name.Space == "http://www.w3.org/2000/svg" {
		return l.svg(e, name, doc, resources)
	}
	if doc.name.Local != "html" || doc.name.Space != "http://www.w3.org/1999/xhtml" {
		return fmt.Errorf("unsupported content root or namespace: %s", doc.name.Local)
	}
	body := doc.child("body")
	if body.name.Local == "" {
		return fmt.Errorf("XHTML lacks body")
	}
	title := doc.child("head").child("title").allText()
	first, firstChapter := len(l.book.pages), len(l.book.chaps)
	var text epubParagraph
	heading := false
	italic, bold, pre := 0, 0, 0
	bullet := false
	pendingChapter := -1
	flush := func() error {
		paragraph := text
		text = epubParagraph{}
		chapter := -1
		if heading && pendingChapter >= 0 && strings.TrimSpace(paragraph.String()) != "" {
			chapter, pendingChapter = pendingChapter, -1
		}
		if err := l.paragraph(paragraph, heading); err != nil {
			return err
		}
		if chapter >= 0 {
			// Layout may have moved the heading to a fresh page.
			l.book.chaps[chapter].Page = len(l.book.pages)
		}
		return nil
	}
	var walk func(*epubNode) error
	walk = func(n *epubNode) error {
		if n.name.Local == "" {
			lines := []string{n.text}
			if pre > 0 {
				lines = strings.Split(n.text, "\n")
			}
			for i, line := range lines {
				if i > 0 {
					if err := flush(); err != nil {
						return err
					}
				}
				if pre > 0 {
					line = epubPreLine(line, i > 0 && i < len(lines)-1)
				}
				if strings.TrimSpace(line) != "" {
					if bullet {
						text.WriteString("\u2022 ")
						bullet = false
					}
					text.style(italic > 0, bold > 0)
				}
				text.WriteString(line)
			}
			return nil
		}
		switch n.name.Local {
		case "script", "iframe", "object", "embed", "audio", "video", "math", "switch", "base":
			return fmt.Errorf("unsupported EPUB element: %s", n.name.Local)
		case "style":
			return nil
		case "i", "em", "cite", "dfn", "var":
			italic++
			defer func() { italic-- }()
		case "b", "strong":
			bold++
			defer func() { bold-- }()
		case "pre":
			pre++
			defer func() { pre-- }()
		}
		if n.name.Space != "http://www.w3.org/1999/xhtml" && n.name.Local != "svg" {
			return fmt.Errorf("EPUB unsupported element namespace: %s", n.name.Space)
		}
		isHeading := len(n.name.Local) == 2 && n.name.Local[0] == 'h' && n.name.Local[1] >= '1' && n.name.Local[1] <= '6'
		block := epubBlock(n.name.Local)
		if block || n.name.Local == "br" || n.name.Local == "img" || n.name.Local == "svg" {
			if err := flush(); err != nil {
				return err
			}
		}
		if isHeading {
			// Chapter headings start a page, as page-break-before does in most EPUB styles.
			if n.name.Local[1] <= '2' {
				if err := l.flushPage(); err != nil {
					return err
				}
			}
			if title := n.allText(); title != "" {
				pendingChapter = len(l.book.chaps)
				l.book.chaps = append(l.book.chaps, Chapter{Title: title, Page: len(l.book.pages)})
			}
		}
		if n.name.Local == "li" {
			// The bullet joins the item's first text, even inside a nested block.
			bullet = true
			defer func() { bullet = false }()
		}
		oldHeading := heading
		heading = heading || isHeading
		switch n.name.Local {
		case "img":
			src := n.attr("src")
			if src == "" {
				return fmt.Errorf("EPUB image lacks src")
			}
			resolved, err := epubPath(name, src)
			if err != nil {
				return err
			}
			return l.addImage(e, resolved, resources)
		case "svg":
			return l.svg(e, name, n, resources)
		case "ruby":
			return text.ruby(n)
		case "rt", "rp", "rtc":
			return fmt.Errorf("EPUB ruby annotation outside ruby: %s", n.name.Local)
		default:
			for _, c := range n.children {
				if err := walk(c); err != nil {
					return err
				}
			}
		}
		if block || n.name.Local == "br" {
			if err := flush(); err != nil {
				return err
			}
		}
		if isHeading {
			pendingChapter = -1
		}
		heading = oldHeading
		return nil
	}
	if err := walk(body); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}
	if title != "" && len(l.book.chaps) == firstChapter {
		l.book.chaps = append(l.book.chaps, Chapter{Title: title, Page: first})
	}
	return nil
}
func (l *epubLayout) svg(e *epubPackage, name string, n *epubNode, resources map[string]string) error {
	if n.name.Local != "" && n.name.Space != "http://www.w3.org/2000/svg" {
		return fmt.Errorf("EPUB unsupported SVG namespace: %s", n.name.Space)
	}
	for _, a := range n.attrs {
		if a.Name.Local == "transform" || a.Name.Local == "style" {
			return fmt.Errorf("EPUB SVG transforms/styles are unsupported")
		}
	}
	switch n.name.Local {
	case "svg", "g":
		for _, c := range n.children {
			if err := l.svg(e, name, c, resources); err != nil {
				return err
			}
		}
	case "image":
		ref := n.attr("href")
		if ref == "" {
			return fmt.Errorf("EPUB SVG image lacks href")
		}
		resolved, err := epubPath(name, ref)
		if err != nil {
			return err
		}
		return l.addImage(e, resolved, resources)
	case "title", "desc", "metadata":
	case "":
		if strings.TrimSpace(n.text) != "" {
			return fmt.Errorf("EPUB SVG text is unsupported")
		}
	default:
		return fmt.Errorf("EPUB SVG drawing is unsupported: %s", n.name.Local)
	}
	return nil
}

func validateEPUBReferences(name string, n *epubNode) error {
	if n.name.Local == "script" || n.name.Local == "base" {
		return fmt.Errorf("EPUB unsupported element: %s", n.name.Local)
	}
	for _, a := range n.attrs {
		if a.Name.Local == "href" && n.name.Local == "a" && n.name.Space == "http://www.w3.org/1999/xhtml" {
			continue // Render link labels without navigation or resource loading.
		}
		if a.Name.Local == "href" || a.Name.Local == "src" || a.Name.Local == "poster" {
			if _, err := epubPath(name, a.Value); err != nil {
				return err
			}
		}
		if a.Name.Local == "srcset" {
			return fmt.Errorf("EPUB srcset images are unsupported")
		}
	}
	for _, c := range n.children {
		if err := validateEPUBReferences(name, c); err != nil {
			return err
		}
	}
	return nil
}
