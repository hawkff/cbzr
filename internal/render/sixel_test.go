package render

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"strings"
	"testing"
)

// decodeSixel reads the images encodeSixel writes: raster size, palette and
// bands. Pixels no color sets stay transparent.
func decodeSixel(t *testing.T, data []byte) (*image.NRGBA, int) {
	t.Helper()
	s := string(data)
	head, ok := strings.CutPrefix(s, "\x1bP0;1;0q\"1;1;")
	if !ok || !strings.HasSuffix(s, "\x1b\\") {
		t.Fatalf("not a transparent 1:1 sixel image: %q", s[:min(len(s), 40)])
	}
	s = strings.TrimSuffix(head, "\x1b\\")
	var w, h int
	if _, err := fmt.Sscanf(s, "%d;%d", &w, &h); err != nil {
		t.Fatal(err)
	}
	s = strings.TrimLeft(s, "0123456789;")
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	palette := map[int]color.NRGBA{}
	number := func() int {
		end := strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' })
		if end < 0 {
			end = len(s)
		}
		n, err := strconv.Atoi(s[:end])
		if err != nil {
			t.Fatalf("number at %q", s[:min(len(s), 20)])
		}
		s = s[end:]
		return n
	}
	x, y, current := 0, 0, -1
	for s != "" {
		switch c := s[0]; {
		case c == '#':
			s = s[1:]
			i := number()
			if strings.HasPrefix(s, ";2;") {
				s = s[3:]
				var rgb [3]uint8
				for k := range rgb {
					if k > 0 {
						s = s[1:]
					}
					rgb[k] = uint8((number()*255 + 50) / 100)
				}
				palette[i] = color.NRGBA{rgb[0], rgb[1], rgb[2], 255}
			}
			current = i
		case c == '$':
			s, x = s[1:], 0
		case c == '-':
			s, x, y = s[1:], 0, y+6
		case c == '!' || c >= '?' && c <= '~':
			count := 1
			if c == '!' {
				s = s[1:]
				count = number()
				c = s[0]
			}
			s = s[1:]
			for range count {
				for r := range 6 {
					if (c-'?')&(1<<r) != 0 {
						if x >= w || y+r >= h {
							t.Fatalf("sixel at %d,%d outside %dx%d", x, y+r, w, h)
						}
						img.SetNRGBA(x, y+r, palette[current])
					}
				}
				x++
			}
		default:
			t.Fatalf("unexpected %q", s[:min(len(s), 20)])
		}
	}
	return img, len(palette)
}

// near allows the rounding of sixel's percent color components.
func near(a, b color.NRGBA) bool {
	d := func(x, y uint8) bool { return max(x, y)-min(x, y) < 3 }
	return a.A == b.A && d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B)
}

func TestSixelKeepsColorsAndTransparency(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 37, 14)) // a partial last band
	for y := range 14 {
		for x := range 37 {
			if x >= 3 { // a transparent margin on the left
				img.SetRGBA(x, y, color.RGBA{uint8(40 * (x % 4)), uint8(60 * (y % 3)), 200, 255})
			}
		}
	}
	got, colors := decodeSixel(t, encodeSixel(img, 256))
	if got.Bounds() != img.Bounds() || colors != 12 {
		t.Fatalf("%v with %d colors", got.Bounds(), colors)
	}
	for y := range 14 {
		for x := range 37 {
			want := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if g := got.NRGBAAt(x, y); !near(g, want) {
				t.Fatalf("pixel %d,%d = %v, want %v", x, y, g, want)
			}
		}
	}
}

func TestSixelGrayPagesKeepEveryLevel(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 7))
	for x := range 256 {
		for y := range 7 {
			img.SetRGBA(x, y, color.RGBA{uint8(x), uint8(x), uint8(x), 255})
		}
	}
	got, colors := decodeSixel(t, encodeSixel(img, 256))
	if colors != 256 {
		t.Fatalf("%d gray levels, want 256", colors)
	}
	for x := range 256 {
		if g := got.NRGBAAt(x, 6); !near(g, color.NRGBA{uint8(x), uint8(x), uint8(x), 255}) {
			t.Fatalf("gray %d came back as %v", x, g)
		}
	}
}

func TestSixelFitsTheTerminalsColors(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 60, 20))
	for y := range 20 {
		for x := range 60 {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 4), uint8(y * 12), uint8(x * y), 255})
		}
	}
	if _, colors := decodeSixel(t, encodeSixel(img, 16)); colors != 16 {
		t.Fatalf("%d colors for 16 registers", colors)
	}
	// A flat row repeats one sixel, written once with its count.
	flat := image.NewRGBA(image.Rect(0, 0, 400, 6))
	draw.Draw(flat, flat.Bounds(), image.White, image.Point{}, draw.Src)
	if data := encodeSixel(flat, 256); !bytes.Contains(data, []byte("!400~")) || len(data) > 60 {
		t.Fatalf("flat image: %q", data)
	}
}

func TestSixelRenderFitsItsCells(t *testing.T) {
	page := image.NewRGBA(image.Rect(0, 0, 300, 500))
	draw.Draw(page, page.Bounds(), &image.Uniform{color.RGBA{10, 20, 30, 255}}, image.Point{}, draw.Src)
	s := &Sixel{cw: 8, ch: 17, colors: 256}
	for _, invert := range []bool{false, true} {
		res, err := s.Render(page, 0, 40, 12, invert)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Paint || res.Rows != len(res.Lines) || res.Cols > 40 || res.Rows > 12 || strings.Trim(res.Lines[0], " ") != "" || len(res.Lines[0]) != res.Cols {
			t.Fatalf("cells: %+v", res)
		}
		img, _ := decodeSixel(t, res.Transmit)
		b := img.Bounds()
		if (b.Dy()+5)/6*6 > res.Rows*17 || b.Dx() > res.Cols*8 || b.Dy() > res.Rows*17 {
			t.Fatalf("%v spills out of %dx%d cells", b, res.Cols, res.Rows)
		}
		want := color.NRGBA{10, 20, 30, 255}
		if invert {
			want = color.NRGBA{245, 235, 225, 255}
		}
		if g := img.NRGBAAt(b.Dx()/2, b.Dy()/2); !near(g, want) {
			t.Fatalf("invert=%v: %v, want %v", invert, g, want)
		}
	}
}

func TestSixelCellsMatchTheBitmap(t *testing.T) {
	for _, tc := range []struct {
		name               string
		w, h               int
		cw, ch             float64
		maxCols, maxRows   int
		wantW, wantH       int
		wantCols, wantRows int
	}{
		// The last band ends inside the image's row, so nothing is trimmed.
		{"short and wide", 640, 11, 8, 16, 80, 10, 640, 11, 80, 1},
		// A 9-pixel strip in 10-pixel rows would spill its second band
		// into the next row; trimmed to one band, it needs fewer columns.
		{"band spill", 100, 9, 10, 10, 10, 5, 66, 6, 7, 1},
	} {
		page := image.NewRGBA(image.Rect(0, 0, tc.w, tc.h))
		draw.Draw(page, page.Bounds(), image.Black, image.Point{}, draw.Src)
		res, err := (&Sixel{cw: tc.cw, ch: tc.ch, colors: 256}).Render(page, 0, tc.maxCols, tc.maxRows, false)
		if err != nil {
			t.Fatal(err)
		}
		img, _ := decodeSixel(t, res.Transmit)
		if got := img.Bounds().Size(); got != image.Pt(tc.wantW, tc.wantH) || res.Cols != tc.wantCols || res.Rows != tc.wantRows {
			t.Fatalf("%s: %v in %dx%d cells, want %dx%d in %dx%d", tc.name, got, res.Cols, res.Rows, tc.wantW, tc.wantH, tc.wantCols, tc.wantRows)
		}
	}
}

func TestProbeReadsSixelSupport(t *testing.T) {
	for reply, want := range map[string]bool{
		"\x1b[6;17;8t\x1b[?62;4;22c": true,  // foot
		"\x1b[?65;4;6;18;22c":        true,  // WezTerm
		"\x1b[?62;22c":               false, // Ghostty
		"\x1b[?1;2c":                 false, // tmux without sixel
		"\x1b[?6c":                   false, // the Linux console
		"\x1b[?4c":                   false, // a class, not an attribute
		"":                           false, // no answer
	} {
		if got := hasSixel([]byte(reply)); got != want {
			t.Fatalf("%q: sixel = %v", reply, got)
		}
	}
	if cw, ch, ok := parseCellReply([]byte("\x1b[6;17;8t\x1b[?62;4;22c")); !ok || cw != 8 || ch != 17 {
		t.Fatalf("cell size %vx%v, %v", cw, ch, ok)
	}
	if n := colorRegisters([]byte("\x1b[?1;0;16S\x1b[?63;4c")); n != 16 {
		t.Fatalf("color registers = %d", n)
	}
	if n := colorRegisters([]byte("\x1b[?1;3;0S\x1b[?63;4c")); n != 0 {
		t.Fatalf("failed query gave %d registers", n)
	}
}
