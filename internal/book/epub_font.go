package book

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unicode"

	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/harfbuzz"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"
)

const (
	maxEPUBFontBytes = 32 << 20
	maxEPUBFontFaces = 32
)

// Cache font data, not faces: each layout and rasterizer needs its own face.
var epubFallbackFonts = sync.OnceValues(findEPUBFallbackFonts)

func findEPUBFallbackFonts() ([]*font.Font, error) {
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
	var fonts []*font.Font
	for _, path := range paths {
		if found, err := readEPUBFallbackFonts(path); err == nil {
			fonts = append(fonts, found...)
		}
	}
	return fonts, nil
}

func readEPUBFallbackFonts(path string) ([]*font.Font, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := readBounded(f, maxEPUBFontBytes)
	if err != nil {
		return nil, err
	}
	return parseEPUBFonts(data)
}

func parseEPUBFonts(data []byte) ([]*font.Font, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("font must be TTF/OTF or TTC/OTC")
	}
	switch string(data[:4]) {
	case "\x00\x01\x00\x00", "OTTO", "true", "typ1", "ttcf":
	default:
		return nil, fmt.Errorf("font must be TTF/OTF or TTC/OTC")
	}
	// Bound collection allocation before handing the file to the font parser.
	if len(data) >= 12 && string(data[:4]) == "ttcf" {
		count := binary.BigEndian.Uint32(data[8:12])
		if count < 1 || count > maxEPUBFontFaces {
			return nil, fmt.Errorf("font collection must contain 1 to %d faces", maxEPUBFontFaces)
		}
	}
	loaders, err := opentype.NewLoaders(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if len(loaders) < 1 || len(loaders) > maxEPUBFontFaces {
		return nil, fmt.Errorf("font collection must contain 1 to %d faces", maxEPUBFontFaces)
	}
	fonts := make([]*font.Font, 0, len(loaders))
	for _, loader := range loaders {
		f, err := font.NewFont(loader)
		if err != nil {
			return nil, err
		}
		fonts = append(fonts, f)
	}
	return fonts, nil
}

// Bundled styles in order: regular, bold, italic, bold italic.
var epubBundledFonts = sync.OnceValues(func() ([]*font.Font, error) {
	var fonts []*font.Font
	for _, data := range [][]byte{goregular.TTF, gobold.TTF, goitalic.TTF, gobolditalic.TTF} {
		parsed, err := parseEPUBFonts(data)
		if err != nil {
			return nil, err
		}
		fonts = append(fonts, parsed[0])
	}
	return fonts, nil
})

// epubFaces returns one face list per bundled style, each followed by the fallbacks.
func epubFaces() ([][]*font.Face, error) {
	bundled, err := epubBundledFonts()
	if err != nil {
		return nil, err
	}
	fallbacks, err := epubFallbackFonts()
	if err != nil {
		return nil, err
	}
	styles := make([][]*font.Face, len(bundled))
	for i, f := range bundled {
		styles[i] = []*font.Face{font.NewFace(f)}
		for _, fallback := range fallbacks {
			styles[i] = append(styles[i], font.NewFace(fallback))
		}
	}
	return styles, nil
}

type epubFontMap struct {
	faces    []*font.Face
	resolved map[rune]*font.Face
}

// ResolveFace prefers the bundled font, then the first fallback with an outline.
func (fonts epubFontMap) ResolveFace(r rune) *font.Face {
	if face := fonts.resolved[r]; face != nil {
		return face
	}
	for _, f := range fonts.faces {
		if gid, ok := f.NominalGlyph(r); ok && gid != 0 {
			if epubRuneOutline(f, gid, r) {
				fonts.resolved[r] = f
				return f
			}
		}
	}
	fonts.resolved[r] = fonts.faces[0]
	return fonts.faces[0]
}

func epubRuneOutline(face *font.Face, glyph font.GID, r rune) bool {
	outline, ok := face.GlyphDataOutline(glyph)
	return ok && (len(outline.Segments) != 0 || epubInvisibleRune(r))
}

func epubInvisibleRune(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r) || harfbuzz.IsDefaultIgnorable(r)
}
