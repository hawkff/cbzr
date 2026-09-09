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

const maxEPUBFontBytes = 32 << 20

// Cache font data, not faces: each layout and rasterizer needs its own face.
var epubFallbackFont = sync.OnceValues(findEPUBFallbackFont)

func findEPUBFallbackFont() (*opentype.Font, error) {
	if path := os.Getenv("CBZR_EPUB_FALLBACK_FONT"); path != "" {
		f, err := readEPUBFallbackFont(path)
		if err != nil {
			return nil, fmt.Errorf("EPUB fallback font: %w", err)
		}
		return f, nil
	}
	var paths []string
	switch runtime.GOOS {
	case "darwin":
		paths = []string{
			"/System/Library/Fonts/Apple Symbols.ttf",
			"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
		}
	case "windows":
		root := os.Getenv("WINDIR")
		if root == "" {
			root = `C:\Windows`
		}
		paths = []string{filepath.Join(root, "Fonts", "seguisym.ttf")}
	default:
		paths = []string{
			"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/dejavu-sans-fonts/DejaVuSans.ttf",
			"/usr/local/share/fonts/dejavu/DejaVuSans.ttf",
		}
	}
	for _, path := range paths {
		if f, err := readEPUBFallbackFont(path); err == nil {
			return f, nil
		}
	}
	return nil, nil
}

func readEPUBFallbackFont(path string) (*opentype.Font, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := readBounded(f, maxEPUBFontBytes)
	if err != nil {
		return nil, err
	}
	return opentype.Parse(data)
}

func newEPUBFace(primary, fallback *opentype.Font, size float64) (font.Face, error) {
	opts := &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull}
	face, err := opentype.NewFace(primary, opts)
	if err != nil || fallback == nil {
		return face, err
	}
	other, err := opentype.NewFace(fallback, opts)
	if err != nil {
		face.Close()
		return nil, err
	}
	return &epubFallbackFace{Face: face, fallback: other}, nil
}

type epubFallbackFace struct {
	font.Face
	fallback font.Face
}

func (f *epubFallbackFace) faceFor(r rune) font.Face {
	if _, ok := f.Face.GlyphAdvance(r); ok {
		return f.Face
	}
	return f.fallback
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
	_, first := f.Face.GlyphAdvance(a)
	_, second := f.Face.GlyphAdvance(b)
	if first != second {
		return 0
	}
	if first {
		return f.Face.Kern(a, b)
	}
	return f.fallback.Kern(a, b)
}

func (f *epubFallbackFace) Close() error {
	return errors.Join(f.Face.Close(), f.fallback.Close())
}
