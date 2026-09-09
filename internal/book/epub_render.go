package book

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"strings"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/text/unicode/norm"
)

const (
	epubPageWidth  = 900
	epubPageHeight = 1200
	epubMargin     = 60
)

type epubLine struct {
	text    string
	y       int
	heading bool
}
type epubTextPage struct{ lines []epubLine }

func (p epubTextPage) Name() string { return "epub-text.png" }
func (p epubTextPage) Open() (io.ReadCloser, error) {
	regular, bold, err := epubFaces()
	if err != nil {
		return nil, err
	}
	defer regular.Close()
	defer bold.Close()
	canvas := image.NewRGBA(image.Rect(0, 0, epubPageWidth, epubPageHeight))
	draw.Draw(canvas, canvas.Bounds(), image.White, image.Point{}, draw.Src)
	d := font.Drawer{Dst: canvas, Src: image.Black}
	for _, line := range p.lines {
		d.Face = regular
		if line.heading {
			d.Face = bold
		}
		d.Dot = fixed.P(epubMargin, line.y)
		d.DrawString(line.text)
	}
	var data bytes.Buffer
	if err := png.Encode(&data, canvas); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data.Bytes())), nil
}

// IsTextPage identifies EPUB text pages that need a pixel-capable renderer.
func (b *Book) IsTextPage(i int) bool {
	if i < 0 || i >= len(b.pages) {
		return false
	}
	_, ok := b.pages[i].(epubTextPage)
	return ok
}

func epubFaces() (font.Face, font.Face, error) {
	regular, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, nil, err
	}
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, nil, err
	}
	r, err := opentype.NewFace(regular, &opentype.FaceOptions{Size: 32, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, nil, err
	}
	b, err := opentype.NewFace(bold, &opentype.FaceOptions{Size: 40, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		r.Close()
		return nil, nil, err
	}
	return r, b, nil
}

type epubLayout struct {
	book          *Book
	regular, bold font.Face
	lines         []epubLine
	y             int
	activeImages  map[string]bool
}

func newEPUBLayout(b *Book) (*epubLayout, error) {
	r, h, err := epubFaces()
	if err != nil {
		return nil, err
	}
	return &epubLayout{book: b, regular: r, bold: h, y: epubMargin, activeImages: map[string]bool{}}, nil
}
func (l *epubLayout) close() { l.regular.Close(); l.bold.Close() }
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
func (l *epubLayout) text(text string, heading bool) error {
	text = norm.NFC.String(strings.ReplaceAll(text, "\u00ad", ""))
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	face, height := l.regular, 44
	if heading {
		face, height = l.bold, 54
	}
	width := fixed.I(epubPageWidth - 2*epubMargin)
	line := []rune{}
	advance := fixed.Int26_6(0)
	emit := func() error {
		if len(line) == 0 {
			return nil
		}
		if l.y+height > epubPageHeight-epubMargin {
			if err := l.flushPage(); err != nil {
				return err
			}
		}
		l.y += height
		l.lines = append(l.lines, epubLine{text: string(line), y: l.y, heading: heading})
		line = nil
		advance = 0
		return nil
	}
	for _, word := range words {
		// ponytail: bundled Go fonts cover unshaped text; add shaping and fonts before expanding scripts.
		for _, r := range word {
			if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) || unicode.IsControl(r) || unicode.IsLetter(r) && !unicode.In(r, unicode.Latin, unicode.Greek, unicode.Cyrillic) {
				return fmt.Errorf("EPUB text needs unsupported glyph or shaping: U+%04X", r)
			}
			if _, ok := face.GlyphAdvance(r); !ok {
				return fmt.Errorf("EPUB bundled font lacks glyph U+%04X", r)
			}
		}
		wordWidth := font.MeasureString(face, word)
		space, _ := face.GlyphAdvance(' ')
		if len(line) > 0 && advance+space+wordWidth > width {
			if err := emit(); err != nil {
				return err
			}
		}
		if len(line) > 0 {
			line = append(line, ' ')
			advance += space
		}
		for _, r := range word {
			a, _ := face.GlyphAdvance(r)
			kern := fixed.Int26_6(0)
			if len(line) > 0 {
				kern = face.Kern(line[len(line)-1], r)
			}
			if advance+kern+a > width {
				if err := emit(); err != nil {
					return err
				}
				kern = 0
			}
			line = append(line, r)
			advance += kern + a
		}
	}
	if err := emit(); err != nil {
		return err
	}
	l.y += 18
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
	var text strings.Builder
	heading := false
	pendingChapter := -1
	flush := func() error {
		s := text.String()
		text.Reset()
		if heading && pendingChapter >= 0 && strings.TrimSpace(strings.ReplaceAll(s, "\u00ad", "")) != "" {
			l.book.chaps[pendingChapter].Page = len(l.book.pages)
			pendingChapter = -1
		}
		return l.text(s, heading)
	}
	var walk func(*epubNode) error
	walk = func(n *epubNode) error {
		if n.name.Local == "" {
			text.WriteString(n.text)
			return nil
		}
		switch n.name.Local {
		case "script", "iframe", "object", "embed", "audio", "video", "math", "switch", "base":
			return fmt.Errorf("unsupported EPUB element: %s", n.name.Local)
		case "style":
			return nil
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
			if err := l.flushPage(); err != nil {
				return err
			}
			if title := n.allText(); title != "" {
				pendingChapter = len(l.book.chaps)
				l.book.chaps = append(l.book.chaps, Chapter{Title: title, Page: len(l.book.pages)})
			}
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
