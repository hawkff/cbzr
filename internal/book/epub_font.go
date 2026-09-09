package book

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	maxEPUBFontBytes = 32 << 20
	maxEPUBFontFaces = 32
)

// Cache font data, not faces: each layout and rasterizer needs its own face.
var epubFallbackFonts = sync.OnceValues(findEPUBFallbackFonts)

func findEPUBFallbackFonts() ([]*opentype.Font, error) {
	if path := os.Getenv("CBZR_EPUB_FALLBACK_FONT"); path != "" {
		fonts, err := readEPUBFallbackFonts(path)
		if err != nil {
			return nil, fmt.Errorf("EPUB fallback font: %w", err)
		}
		return fonts, nil
	}
	var paths []string
	switch runtime.GOOS {
	case "darwin":
		paths = []string{
			"/System/Library/Fonts/Apple Symbols.ttf",
			"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
			"/System/Library/Fonts/ヒラギノ角ゴシック W3.ttc",
			"/System/Library/Fonts/STHeiti Light.ttc",
		}
	case "windows":
		root := os.Getenv("WINDIR")
		if root == "" {
			root = `C:\Windows`
		}
		paths = []string{
			filepath.Join(root, "Fonts", "seguisym.ttf"),
			filepath.Join(root, "Fonts", "arial.ttf"),
			filepath.Join(root, "Fonts", "msgothic.ttc"),
			filepath.Join(root, "Fonts", "msyh.ttc"),
		}
	default:
		paths = []string{
			"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/dejavu-sans-fonts/DejaVuSans.ttf",
			"/usr/local/share/fonts/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/truetype/noto/NotoSansSymbols2-Regular.ttf",
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/google-noto-cjk/NotoSansCJK-Regular.ttc",
		}
	}
	var fonts []*opentype.Font
	for _, path := range paths {
		if found, err := readEPUBFallbackFonts(path); err == nil {
			fonts = append(fonts, found...)
		}
	}
	return fonts, nil
}

func readEPUBFallbackFonts(path string) ([]*opentype.Font, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := readBounded(f, maxEPUBFontBytes)
	if err != nil {
		return nil, err
	}
	collection, err := opentype.ParseCollection(data)
	if err != nil {
		return nil, err
	}
	count := collection.NumFonts()
	if count < 1 || count > maxEPUBFontFaces {
		return nil, fmt.Errorf("font collection must contain 1 to %d faces", maxEPUBFontFaces)
	}
	fonts := make([]*opentype.Font, 0, count)
	for i := 0; i < count; i++ {
		f, err := collection.Font(i)
		if err != nil {
			return nil, err
		}
		fonts = append(fonts, f)
	}
	return fonts, nil
}

func newEPUBFace(primary *opentype.Font, fallbacks []*opentype.Font, size float64) (font.Face, error) {
	opts := &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull}
	face, err := opentype.NewFace(primary, opts)
	if err != nil || len(fallbacks) == 0 {
		return face, err
	}
	combined := &epubFallbackFace{Face: face}
	for _, fallback := range fallbacks {
		other, err := opentype.NewFace(fallback, opts)
		if err != nil {
			combined.Close()
			return nil, err
		}
		combined.fallbacks = append(combined.fallbacks, other)
	}
	return combined, nil
}

type epubFallbackFace struct {
	font.Face
	fallbacks []font.Face
}

func (f *epubFallbackFace) faceFor(r rune) font.Face {
	if _, ok := f.Face.GlyphAdvance(r); ok {
		return f.Face
	}
	for _, fallback := range f.fallbacks {
		if _, ok := fallback.GlyphAdvance(r); ok {
			return fallback
		}
	}
	return f.Face
}

func (f *epubFallbackFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	return f.faceFor(r).Glyph(dot, r)
}

func (f *epubFallbackFace) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	return f.faceFor(r).GlyphBounds(r)
}

func (f *epubFallbackFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	return f.faceFor(r).GlyphAdvance(r)
}

func (f *epubFallbackFace) Kern(a, b rune) fixed.Int26_6 {
	first, second := f.faceFor(a), f.faceFor(b)
	if first != second {
		return 0
	}
	return first.Kern(a, b)
}

func (f *epubFallbackFace) Close() error {
	err := f.Face.Close()
	for _, fallback := range f.fallbacks {
		err = errors.Join(err, fallback.Close())
	}
	return err
}
