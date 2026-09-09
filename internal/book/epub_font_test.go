package book

import (
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
	face := &epubFallbackFace{Face: basicfont.Face7x13, fallback: other}
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
	if _, err := findEPUBFallbackFont(); err == nil {
		t.Fatal("missing configured font ignored")
	}
	if err := os.WriteFile(path, []byte("not a font"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := findEPUBFallbackFont(); err == nil {
		t.Fatal("invalid configured font ignored")
	}
	if err := os.WriteFile(path, goregular.TTF, 0o600); err != nil {
		t.Fatal(err)
	}
	if f, err := findEPUBFallbackFont(); err != nil || f == nil {
		t.Fatalf("configured font: %v", err)
	}
	if err := os.Truncate(path, maxEPUBFontBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := findEPUBFallbackFont(); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized font: %v", err)
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
		if _, ok := face.GlyphAdvance('◎'); !ok {
			t.Fatal("system fallback lacks U+25CE")
		}
		rect, mask, _, _, ok := face.Glyph(fixed.P(40, 60), '◎')
		if !ok || rect.Empty() || mask == nil {
			t.Fatal("system fallback did not rasterize U+25CE")
		}
	}
	entries := epubFixture()
	replaceEPUB(entries, "OPS/text/z.xhtml", "First heading", "◎ First heading")
	replaceEPUB(entries, "OPS/text/z.xhtml", "Hello", "◎ Hello")
	b, err := Open(writeEPUB(t, entries, ".epub"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !strings.Contains(textEPUBPage(t, b, 0), "◎ Hello") {
		t.Fatal("fallback rewrote the book text")
	}
	img, err := b.Page(0)
	if err != nil || img.Bounds() != image.Rect(0, 0, epubPageWidth, epubPageHeight) {
		t.Fatalf("fallback text page: %v", err)
	}
}
