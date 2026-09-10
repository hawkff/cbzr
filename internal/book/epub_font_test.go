package book

import (
	"bytes"
	"encoding/binary"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-text/typesetting/font"
	"golang.org/x/image/font/gofont/goregular"
)

func TestEPUBFallbackShapedRaster(t *testing.T) {
	fonts, err := parseEPUBFonts(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	primary := *fonts[0]
	primary.Cmap = epubMissingRuneCmap{Cmap: primary.Cmap, missing: 'Ω'}
	l := &epubLayout{book: &Book{}, regular: []*font.Face{font.NewFace(&primary), font.NewFace(fonts[0])}, y: epubMargin}
	if err := l.text("AΩA", false); err != nil {
		t.Fatal(err)
	}
	if err := l.flushPage(); err != nil {
		t.Fatal(err)
	}
	page := l.book.pages[0].(epubTextPage)
	runs := page.lines[0].runs
	if len(runs) != 3 || runs[0].font != &primary || runs[1].font != fonts[0] || runs[2].font != &primary {
		t.Fatalf("wrong fallback runs: %#v", runs)
	}
	for _, run := range runs {
		if run.Face != nil {
			t.Fatal("page retained mutable layout face")
		}
	}
	before, _, err := l.book.PageBytes(0)
	if err != nil {
		t.Fatal(err)
	}
	l.regular = nil
	after, _, err := l.book.PageBytes(0)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("raster depends on layout faces: %v", err)
	}
	if err := l.text("", false); err != nil {
		t.Fatal(err)
	}
}

type epubMissingRuneCmap struct {
	font.Cmap
	missing rune
}

func (c epubMissingRuneCmap) Lookup(r rune) (font.GID, bool) {
	if r == c.missing {
		return 0, false
	}
	return c.Cmap.Lookup(r)
}

func TestConfiguredEPUBFallbackFont(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fallback.ttf")
	t.Setenv("CBZR_EPUB_FALLBACK_FONT", path)
	if _, err := findEPUBFallbackFonts(); err == nil {
		t.Fatal("missing configured font ignored")
	}
	if err := os.WriteFile(path, []byte("not a font"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := findEPUBFallbackFonts(); err == nil {
		t.Fatal("invalid configured font ignored")
	}
	if err := os.WriteFile(path, goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}
	if fonts, err := findEPUBFallbackFonts(); err != nil || len(fonts) != 1 {
		t.Fatalf("configured font: %v", err)
	}
	if err := os.Truncate(path, maxEPUBFontBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := findEPUBFallbackFonts(); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized font: %v", err)
	}
}

func TestEPUBFontCollection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fallback.ttc")
	t.Setenv("CBZR_EPUB_FALLBACK_FONT", path)
	for _, count := range []int{2, maxEPUBFontFaces + 1} {
		header := 12 + 4*count
		data := make([]byte, header+len(goregular.TTF))
		copy(data, "ttcf")
		binary.BigEndian.PutUint32(data[4:], 0x00010000)
		binary.BigEndian.PutUint32(data[8:], uint32(count))
		for i := 0; i < count; i++ {
			binary.BigEndian.PutUint32(data[12+4*i:], uint32(header))
		}
		copy(data[header:], goregular.TTF)
		tables := int(binary.BigEndian.Uint16(data[header+4:]))
		for i := 0; i < tables; i++ {
			offset := data[header+12+16*i+8:]
			binary.BigEndian.PutUint32(offset, binary.BigEndian.Uint32(offset)+uint32(header))
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		fonts, err := findEPUBFallbackFonts()
		if count > maxEPUBFontFaces {
			if err == nil || !strings.Contains(err.Error(), "faces") {
				t.Fatalf("oversized font collection: %v", err)
			}
			continue
		}
		if err != nil || len(fonts) != count {
			t.Fatalf("font collection: %d fonts, %v", len(fonts), err)
		}
		for _, f := range fonts {
			l := &epubLayout{book: &Book{}, regular: []*font.Face{font.NewFace(f)}, y: epubMargin}
			if err := l.text("A", false); err != nil {
				t.Fatal(err)
			}
			if err := l.flushPage(); err != nil {
				t.Fatal(err)
			}
			if _, _, err := l.book.PageBytes(0); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestEPUBMacOSSymbolFallback(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS system font regression")
	}
	entries := epubFixture()
	replaceEPUB(entries, "OPS/text/z.xhtml", "First heading", "◎『First heading』")
	replaceEPUB(entries, "OPS/text/z.xhtml", "Hello", "◎『Hello』")
	b, err := Open(writeEPUB(t, entries, ".epub"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !strings.Contains(textEPUBPage(t, b, 0), "◎『Hello』") {
		t.Fatal("fallback rewrote the book text")
	}
	img, err := b.Page(0)
	if err != nil || img.Bounds() != image.Rect(0, 0, epubPageWidth, epubPageHeight) {
		t.Fatalf("fallback text page: %v", err)
	}
}
