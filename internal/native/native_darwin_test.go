//go:build darwin && cgo

package native

import (
	"archive/zip"
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"cbzr/internal/book"
)

func TestWebtoonFrameKeepsTallPagesWidthFit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tall.cbz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, err := z.Create("page.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(w, image.NewRGBA(image.Rect(0, 0, 100, 2000))); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := book.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	r := &reader{book: b, state: State{Webtoon: true}, scaledPages: make(map[scaledPageKey]image.Image)}
	if frame := r.frame(100, 80); frame == nil || frame.Bounds() != image.Rect(0, 0, 100, 80) {
		t.Fatalf("frame: %v, error: %v", frame, r.err)
	}
	r.pendingScroll = 80
	if r.frame(100, 80) == nil || r.state.Offset <= 0 || r.state.Offset >= .1 {
		t.Fatalf("tall page did not scroll at source width: %#v, %v", r.state, r.err)
	}
	for _, offset := range []float64{0, 1} {
		r.state.Offset = offset
		if r.frame(100, 80) == nil {
			t.Fatal(r.err)
		}
		scroll := 64.0
		if offset == 0 {
			scroll = -64
		}
		r.pendingScroll = scroll
		if r.frame(100, 80) == nil || r.blockedScroll != scroll {
			t.Fatalf("boundary feedback = %v, want %v; error: %v", r.blockedScroll, scroll, r.err)
		}
		r.pendingScroll = -scroll
		if r.frame(100, 80) == nil || r.blockedScroll != 0 {
			t.Fatalf("reverse movement blocked: %v, %v", r.blockedScroll, r.err)
		}
	}
	img, err := r.scaledPage(0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 100 {
		t.Fatal("native webtoon upscaled a narrow page")
	}
}

func TestNativeInversionKeepsOriginalPages(t *testing.T) {
	var data bytes.Buffer
	z := zip.NewWriter(&data)
	for _, name := range []string{"1.png", "2.png"} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(w, image.NewGray(image.Rect(0, 0, 8, 8))); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pages.cbz")
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := book.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, mode := range []string{"single", "spread", "webtoon"} {
		t.Run(mode, func(t *testing.T) {
			r := &reader{book: b, state: State{Zoom: 1, CenterX: .5, CenterY: .5, Spread: mode == "spread", Webtoon: mode == "webtoon"}, scaledPages: make(map[scaledPageKey]image.Image)}
			points := []image.Point{{16, 4}}
			margin := image.Pt(0, 0)
			if r.state.Spread {
				points = []image.Point{{7, 8}, {24, 8}}
				margin = image.Pt(16, 0)
			}
			for _, inverted := range []bool{false, true, false} {
				r.state.Inverted = inverted
				frame := r.frame(32, 16)
				if frame == nil {
					t.Fatal(r.err)
				}
				want := uint32(0)
				if inverted {
					want = 0xffff
				}
				for _, p := range points {
					if red, _, _, _ := frame.At(p.X, p.Y).RGBA(); red != want {
						t.Fatalf("inverted=%v: pixel %v = %d, want %d", inverted, p, red, want)
					}
				}
				if red, _, _, _ := frame.At(margin.X, margin.Y).RGBA(); red != 0 {
					t.Fatal("inversion brightened the page margins")
				}
			}
			cached, err := r.scaledPage(0, 32)
			if err != nil {
				t.Fatal(err)
			}
			if red, _, _, _ := cached.At(0, 0).RGBA(); red != 0 {
				t.Fatal("inversion changed a cached page")
			}
		})
	}
}
