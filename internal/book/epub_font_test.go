package book

import (
	"encoding/binary"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

func TestEPUBFallbackFace(t *testing.T) {
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	other, err := newEPUBFace(parsed, nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	face := &epubFallbackFace{Face: basicfont.Face7x13, fallbacks: []font.Face{basicfont.Face7x13, other}}
	defer face.Close()
	for _, tc := range []struct {
		r    rune
		want font.Face
	}{{'A', basicfont.Face7x13}, {'Ω', other}} {
		advance, ok := face.GlyphAdvance(tc.r)
		wantAdvance, wantOK := tc.want.GlyphAdvance(tc.r)
		if !ok || !wantOK || advance != wantAdvance {
			t.Fatalf("advance for %U = %v, %v", tc.r, advance, ok)
		}
		bounds, _, ok := face.GlyphBounds(tc.r)
		wantBounds, _, _ := tc.want.GlyphBounds(tc.r)
		if !ok || bounds != wantBounds {
			t.Fatalf("bounds for %U = %v, %v", tc.r, bounds, ok)
		}
		dot := fixed.P(40, 60)
		wantRect, _, _, _, _ := tc.want.Glyph(dot, tc.r)
		rect, mask, _, gotAdvance, ok := face.Glyph(dot, tc.r)
		if !ok || mask == nil || rect.Empty() || rect != wantRect || gotAdvance != wantAdvance {
			t.Fatalf("glyph for %U = %v, %v", tc.r, rect, ok)
		}
	}
	if face.Kern('A', 'Ω') != 0 || face.Kern('Ω', 'A') != 0 || face.Kern('Ω', 'Ω') != other.Kern('Ω', 'Ω') {
		t.Fatal("kerning crossed font boundaries")
	}
	if face.Metrics() != basicfont.Face7x13.Metrics() {
		t.Fatal("fallback changed primary metrics")
	}
	if _, ok := face.GlyphAdvance('\U0010ffff'); ok {
		t.Fatal("missing glyph reported as supported")
	}
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
			face, err := newEPUBFace(f, nil, 32)
			if err != nil {
				t.Fatal(err)
			}
			rect, mask, _, _, ok := face.Glyph(fixed.P(40, 60), 'A')
			face.Close()
			if !ok || rect.Empty() || mask == nil {
				t.Fatal("collection font did not rasterize")
			}
		}
	}
}

func TestEPUBMacOSSymbolFallback(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS system font regression")
	}
	regular, bold, err := epubFaces()
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	defer bold.Close()
	for _, face := range []font.Face{regular, bold} {
		for _, r := range "◎『』「」" {
			if _, ok := face.GlyphAdvance(r); !ok {
				t.Fatalf("system fallback lacks %U", r)
			}
			rect, mask, _, _, ok := face.Glyph(fixed.P(40, 60), r)
			if !ok || rect.Empty() || mask == nil {
				t.Fatalf("system fallback did not rasterize %U", r)
			}
		}
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
