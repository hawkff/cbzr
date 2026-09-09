//go:build darwin && cgo

package native

import (
	"archive/zip"
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
