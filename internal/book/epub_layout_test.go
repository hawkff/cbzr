package book

import (
	"bytes"
	"image"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/segmenter"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/math/fixed"
)

func bundledEPUBLayout(t *testing.T) *epubLayout {
	t.Helper()
	fonts, err := parseEPUBFonts(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	return &epubLayout{book: &Book{}, regular: []*font.Face{font.NewFace(fonts[0])}, bold: []*font.Face{font.NewFace(fonts[0])}, y: epubMargin}
}

func TestEPUBUnicodeWrapping(t *testing.T) {
	l := bundledEPUBLayout(t)
	for _, text := range []string{
		"one\u00a0two three four",
		"a\u200db c\u200dd e\u200df",
		"ABC \u202eDEF\u202c GHI",
	} {
		t.Run(text, func(t *testing.T) {
			runes := []rune(text)
			outputs, direction := l.shape(runes, false, 32)
			var wrapper shaping.LineWrapper
			wrapper.Prepare(shaping.WrapConfig{Direction: direction}, runes, shaping.NewSliceIterator(outputs))
			var seg segmenter.Segmenter
			seg.Init(runes)
			boundaries := map[int]bool{0: true}
			iter := seg.GraphemeIterator()
			for iter.Next() {
				g := iter.Grapheme()
				boundaries[g.Offset+len(g.Text)] = true
			}
			start := 0
			for done := false; !done; {
				var line shaping.WrappedLine
				line, done = wrapper.WrapNextLine(150)
				if line.NextLine <= start || !boundaries[line.NextLine] {
					t.Fatalf("split grapheme at %d", line.NextLine)
				}
				if text == "one\u00a0two three four" && start == 0 && line.NextLine < len([]rune("one\u00a0two")) {
					t.Fatal("broke at NBSP")
				}
				start = line.NextLine
			}
			if start != len(runes) {
				t.Fatal("lost input")
			}
		})
	}
	outputs, _ := l.shape([]rune("ABC \u202eDEF\u202c GHI"), false, 32)
	rtl := false
	for _, run := range outputs {
		if run.Direction == di.DirectionRTL {
			rtl = true
			if len(run.Glyphs) > 1 && run.Glyphs[0].TextIndex() < run.Glyphs[len(run.Glyphs)-1].TextIndex() {
				t.Fatal("RTL glyphs remained in logical order")
			}
		}
	}
	if !rtl {
		t.Fatal("bidi override did not produce RTL run")
	}
}

func TestEPUBRubyInlineAndPageBounds(t *testing.T) {
	for _, base := range []string{"word", strings.Repeat("b", 61), strings.Repeat("base ", 600)} {
		entries := epubFixture()
		replaceEPUB(entries, "OPS/text/z.xhtml", "Hello <em>reader</em> &amp; friends.", "Before <ruby>"+base+"<rp>(</rp><rt>reading annotation</rt><rp>)</rp></ruby> after.")
		b, err := Open(writeEPUB(t, entries, ".epub"))
		if err != nil {
			t.Fatal(err)
		}
		var actual, readings strings.Builder
		foundInline := false
		for _, page := range b.pages {
			for _, line := range page.(epubTextPage).lines {
				actual.WriteString(line.text)
				if strings.Contains(line.text, "Before ") && strings.Contains(line.text, "word") {
					foundInline = true
				}
				for _, annotation := range line.annotations {
					readings.WriteString(annotation.text)
					ascent, descent := epubLineBounds(annotation)
					if annotation.y-ascent < epubMargin || annotation.y+descent >= line.y {
						t.Fatal("ruby outside annotation band")
					}
					for _, run := range annotation.runs {
						if run.Size != fixed.I(16) {
							t.Fatal("reading is not smaller")
						}
					}
				}
				_, descent := epubLineBounds(line)
				if line.y+descent > epubPageHeight-epubMargin {
					t.Fatal("base clipped at page bottom")
				}
			}
		}
		var expected epubParagraph
		expected.WriteString("Before " + base + " after.")
		if !strings.Contains(actual.String(), expected.String()) || strings.ContainsAny(actual.String(), "()") || readings.String() != "reading annotation" {
			t.Fatalf("ruby content: base=%q reading=%q", actual.String(), readings.String())
		}
		if base == "word" && !foundInline {
			t.Fatal("ruby broke inline flow")
		}
		if _, _, err := b.PageBytes(0); err != nil {
			t.Fatal(err)
		}
		b.Close()
	}
	l := bundledEPUBLayout(t)
	l.y = epubPageHeight - epubMargin - 80
	if err := l.text("previous", false); err != nil {
		t.Fatal(err)
	}
	var p epubParagraph
	p.WriteString("word")
	p.rubies = []epubRuby{{0, 4, "reading"}}
	if err := l.paragraph(p, false); err != nil {
		t.Fatal(err)
	}
	if err := l.flushPage(); err != nil {
		t.Fatal(err)
	}
	if l.book.Len() != 2 {
		t.Fatalf("ruby did not move with its base: %d pages", l.book.Len())
	}
	p.rubies[0].reading = strings.Repeat("reading ", 1000)
	if err := l.paragraph(p, false); err == nil || !strings.Contains(err.Error(), "height") {
		t.Fatalf("oversized ruby reading: %v", err)
	}
}

func TestEPUBSystemCJKCombiningAndArabic(t *testing.T) {
	l, err := newEPUBLayout(&Book{})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"手ひらがな「漢字」、。", "q\u0301", "مرحبا بالعالم", "abc אבג 123"} {
		t.Run(text, func(t *testing.T) {
			for _, r := range text {
				covered := false
				for _, face := range l.regular {
					if _, ok := face.NominalGlyph(r); ok {
						covered = true
						break
					}
				}
				if !covered {
					if runtime.GOOS == "darwin" && text == "手ひらがな「漢字」、。" {
						t.Fatalf("macOS CJK fallback lacks U+%04X", r)
					}
					t.Skipf("requires an installed font for U+%04X", r)
				}
			}
			outputs, _ := l.shape([]rune(text), false, 32)
			if len(outputs) == 0 {
				t.Fatal("empty shaped output")
			}
			l.book = &Book{}
			l.lines = nil
			l.y = epubMargin
			source := strings.Repeat(text, 70)
			if err := l.text(source, false); err != nil {
				t.Fatal(err)
			}
			if err := l.flushPage(); err != nil {
				t.Fatal(err)
			}
			var actual strings.Builder
			for _, page := range l.book.pages {
				for _, line := range page.(epubTextPage).lines {
					actual.WriteString(line.text)
					if text == "手ひらがな「漢字」、。" && strings.ContainsRune("」、。", []rune(line.text)[0]) {
						t.Fatalf("CJK closing punctuation starts line: %q", line.text)
					}
				}
			}
			if actual.String() != source {
				t.Fatal("lost Unicode text")
			}
			data, _, err := l.book.PageBytes(0)
			if err != nil {
				t.Fatal(err)
			}
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil || img.Bounds() != image.Rect(0, 0, epubPageWidth, epubPageHeight) {
				t.Fatalf("Unicode raster: %v", err)
			}
			var wg sync.WaitGroup
			for range 3 {
				wg.Go(func() {
					again, _, err := l.book.PageBytes(0)
					if err != nil || !bytes.Equal(data, again) {
						t.Errorf("concurrent raster mismatch: %v", err)
					}
				})
			}
			wg.Wait()
		})
	}
}

// The synthetic cmap tests Unicode layout without a bundled CJK font fixture.
// TestEPUBSystemCJKCombiningAndArabic checks installed fonts and their outlines.
type epubSyntheticCmap struct {
	font.Cmap
	glyphs map[rune]font.GID
}

func (c epubSyntheticCmap) Lookup(r rune) (font.GID, bool) {
	if glyph, ok := c.glyphs[r]; ok {
		return glyph, true
	}
	return c.Cmap.Lookup(r)
}

func TestEPUBCJKLineBreaks(t *testing.T) {
	l := bundledEPUBLayout(t)
	fallback := *l.regular[0].Font
	glyph, ok := fallback.NominalGlyph('M')
	if !ok {
		t.Fatal("fixture lacks M")
	}
	const sample = "手ひらがな「漢字」、。"
	cmap := epubSyntheticCmap{Cmap: fallback.Cmap, glyphs: map[rune]font.GID{}}
	for _, r := range sample {
		cmap.glyphs[r] = glyph
	}
	fallback.Cmap = cmap
	l.regular = append(l.regular, font.NewFace(&fallback))
	source := strings.Repeat(sample, 90)
	if err := l.text(source, false); err != nil {
		t.Fatal(err)
	}
	if err := l.flushPage(); err != nil {
		t.Fatal(err)
	}
	var actual strings.Builder
	lines := 0
	for _, page := range l.book.pages {
		for _, line := range page.(epubTextPage).lines {
			runes := []rune(line.text)
			if strings.ContainsRune("」、。", runes[0]) || runes[len(runes)-1] == '「' {
				t.Fatalf("split CJK punctuation: %q", line.text)
			}
			if epubLineWidth(line) > fixed.I(epubPageWidth-2*epubMargin) {
				t.Fatal("CJK line overflow")
			}
			actual.WriteString(line.text)
			lines++
		}
	}
	if actual.String() != source || lines < 2 {
		t.Fatal("CJK wrapping lost text or did not wrap")
	}
	if _, _, err := l.book.PageBytes(0); err != nil {
		t.Fatal(err)
	}
}

func TestEPUBCombiningClusterFallback(t *testing.T) {
	l := bundledEPUBLayout(t)
	fallback := *l.regular[0].Font
	accent, ok := fallback.NominalGlyph('´')
	if !ok {
		t.Fatal("fixture lacks acute accent")
	}
	fallback.Cmap = epubSyntheticCmap{Cmap: fallback.Cmap, glyphs: map[rune]font.GID{'\u0301': accent}}
	primary := *l.regular[0].Font
	primary.Cmap = epubMissingRuneCmap{Cmap: primary.Cmap, missing: '\u0301'}
	l.regular = []*font.Face{font.NewFace(&primary), font.NewFace(&fallback)}
	outputs, _ := l.shape([]rune("q\u0301q"), false, 32)
	if len(outputs) != 2 || outputs[0].Runes.Count != 2 || outputs[0].Face.Font != &fallback || outputs[1].Face.Font != &primary {
		t.Fatalf("combining cluster split across fonts: %#v", outputs)
	}
	for _, glyph := range outputs[0].Glyphs {
		if glyph.TextIndex() != 0 || glyph.RunesCount() != 2 {
			t.Fatal("combining mark escaped its cluster")
		}
	}
}

func TestEPUBEmptyOutlineFallback(t *testing.T) {
	l := bundledEPUBLayout(t)
	primary := *l.regular[0].Font
	space, ok := primary.NominalGlyph(' ')
	if !ok {
		t.Fatal("fixture lacks space")
	}
	primary.Cmap = epubSyntheticCmap{Cmap: primary.Cmap, glyphs: map[rune]font.GID{'A': space}}
	fallback := l.regular[0]
	broken := font.NewFace(&primary)
	l.regular = []*font.Face{broken}
	if output, _ := l.shape([]rune("A"), false, 32); output[0].Face != broken {
		t.Fatal("invisible letter without an alternative changed face")
	}
	l.regular = append(l.regular, fallback)
	if output, _ := l.shape([]rune("A"), false, 32); output[0].Face != fallback {
		t.Fatal("outline fallback")
	}
}

func TestEPUBRubyHeadingAndInvalidMarkup(t *testing.T) {
	entries := epubFixture()
	replaceEPUB(entries, "OPS/text/z.xhtml", "First heading", `<ruby>Base<rp>(</rp><rt>reading</rt><rp>)</rp></ruby>`)
	b, err := Open(writeEPUB(t, entries, ".epub"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	chapters, err := b.Chapters()
	if err != nil || len(chapters) == 0 || chapters[0].Title != "Base" {
		t.Fatalf("ruby chapter label: %#v, %v", chapters, err)
	}
	for _, markup := range []string{
		`<ruby>base<rtc><rt>reading</rt></rtc></ruby>`,
		`<ruby>base<ruby>nested<rt>reading</rt></ruby></ruby>`,
		`<ruby>base<rt xmlns="urn:wrong">reading</rt></ruby>`,
		`<ruby><rt>reading</rt></ruby>`,
	} {
		doc, err := parseEPUBXML([]byte(`<ruby xmlns="http://www.w3.org/1999/xhtml">` + markup + `</ruby>`))
		if err != nil {
			t.Fatal(err)
		}
		var p epubParagraph
		if err := p.ruby(doc.children[0]); err == nil {
			t.Fatalf("unsupported ruby accepted: %s", markup)
		}
	}
}
