//go:build darwin && cgo

package native

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

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

func TestNativePagePointUsesShownCropAndRotation(t *testing.T) {
	for _, scale := range []int{1, 2} {
		for rotation, want := range [][2]float64{{.375, .375}, {.375, .625}, {.625, .625}, {.625, .375}} {
			r := &reader{state: State{Page: 9}, frameSize: image.Pt(200*scale, 100*scale), shown: []shownPage{{
				page: 3, rotation: rotation, bounds: image.Rect(20*scale, 10*scale, 180*scale, 90*scale),
				crop: image.Rect(100, 50, 300, 150), size: image.Pt(400, 200),
			}}}
			page, x, y, ok := r.pagePoint(.3, .3)
			if !ok || page != 3 || math.Abs(x-want[0]) > 1e-9 || math.Abs(y-want[1]) > 1e-9 {
				t.Fatalf("scale=%d rotation=%d: point=%d,%v,%v,%v", scale, rotation, page, x, y, ok)
			}
			for _, point := range [][2]float64{{0, 0}, {1, 1}, {-.1, .5}, {math.NaN(), .5}} {
				if _, _, _, ok := r.pagePoint(point[0], point[1]); ok {
					t.Fatal("margin or invalid point mapped to a page")
				}
			}
		}
	}
}

func nativeLinkedPDF(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"pdfinfo", "pdftoppm", "pdftotext", "pdftohtml"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is not installed", name)
		}
	}
	text := "BT /F1 12 Tf 10 80 Td (next page) Tj 0 -20 Td (web link) Tj ET"
	pdf := "%PDF-1.4\n" +
		"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
		"2 0 obj<</Type/Pages/Kids[3 0 R 6 0 R]/Count 2>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]/Resources<</Font<</F1 4 0 R>>>>/Contents 5 0 R/Annots[7 0 R 8 0 R]>>endobj\n" +
		"4 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\n" +
		"5 0 obj<</Length " + strconv.Itoa(len(text)) + ">>stream\n" + text + "\nendstream\nendobj\n" +
		"6 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]>>endobj\n" +
		"7 0 obj<</Type/Annot/Subtype/Link/Rect[10 76 80 92]/Border[0 0 0]/Dest[6 0 R /Fit]>>endobj\n" +
		"8 0 obj<</Type/Annot/Subtype/Link/Rect[10 56 80 72]/Border[0 0 0]/A<</S/URI/URI(https://example.com/read?utm_source=pdf&id=7)>>>>endobj\n" +
		"trailer<</Root 1 0 R>>\n"
	path := filepath.Join(t.TempDir(), "links.pdf")
	if err := os.WriteFile(path, []byte(pdf), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNativeSlowRenderKeepsInputAndShownPageIndependent(t *testing.T) {
	path := nativeLinkedPDF(t)
	converter, err := exec.LookPath("pdftoppm")
	if err != nil {
		t.Fatal(err)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("slow converter helper needs sh")
	}
	dir := t.TempDir()
	started, release := filepath.Join(dir, "started"), filepath.Join(dir, "release")
	t.Setenv("CBZR_TEST_NATIVE_STARTED", started)
	t.Setenv("CBZR_TEST_NATIVE_RELEASE", release)
	t.Setenv("CBZR_TEST_NATIVE_CONVERTER", converter)
	script := "#!" + sh + "\nprevious=; page=\n" +
		"for arg in \"$@\"; do if [ \"$previous\" = -f ]; then page=\"$arg\"; fi; previous=\"$arg\"; done\n" +
		"if [ \"$page\" = 1 ]; then\n" +
		"printf started > \"$CBZR_TEST_NATIVE_STARTED\"\n" +
		"while [ ! -e \"$CBZR_TEST_NATIVE_RELEASE\" ]; do sleep 0.01; done\nfi\n" +
		"exec \"$CBZR_TEST_NATIVE_CONVERTER\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pdftoppm"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	b, err := book.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r := &reader{book: b, state: State{Zoom: 1}, shown: []shownPage{{page: 0}}}
	done := make(chan *renderedFrame, 2)
	var work sync.WaitGroup
	t.Cleanup(func() {
		os.WriteFile(release, nil, 0o600)
		b.Close()
		work.Wait()
	})
	start := func() {
		work.Add(1)
		go r.renderAsync(100, 80, func(frame *renderedFrame) { done <- frame; work.Done() })
	}
	start()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("converter did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	updated := make(chan struct{})
	go func() {
		r.mu.Lock()
		r.goTo(1)
		r.mu.Unlock()
		r.cancelRender()
		close(updated)
	}()
	select {
	case <-updated:
	case <-time.After(time.Second):
		t.Fatal("page input waited for rendering")
	}
	var old *renderedFrame
	select {
	case old = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("obsolete conversion blocked the replacement page")
	}
	if old == nil || !errors.Is(old.snapshot.err, context.Canceled) || old.accept() {
		t.Fatal("stale render was not canceled")
	}
	start()
	var frame *renderedFrame
	select {
	case frame = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("replacement render did not finish")
	}
	if frame == nil || frame.image == nil || !frame.accept() {
		t.Fatal("replacement frame was not accepted")
	}
	if r.shown[0].page != 0 {
		t.Fatal("unpainted frame changed hit testing")
	}
	r.goTo(0)
	frame.show()
	if r.shown[0].page != 1 || r.state.Page != 0 {
		t.Fatal("painting changed pending navigation or kept the old hit map")
	}
}

func TestNativePDFLinks(t *testing.T) {
	b, err := book.Open(nativeLinkedPDF(t))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, tc := range []struct {
		name          string
		width, height int
		state         State
		x, y          float64
	}{
		{"letterbox", 400, 400, State{Zoom: 1}, .1, .325},
		{"zoom", 400, 200, State{Zoom: 2, CenterX: .25, CenterY: .25}, .2, .3},
		{"rotation", 400, 400, State{Zoom: 1, Rotation: 1}, .675, .1},
		{"spread", 808, 200, State{Zoom: 1, Spread: true}, 41.0 / 808, .15},
		{"webtoon", 200, 80, State{Zoom: 1, Webtoon: true, Offset: .1}, .1, .0625},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &reader{book: b, state: tc.state, scaledPages: make(map[scaledPageKey]image.Image)}
			if r.frame(tc.width, tc.height) == nil {
				t.Fatal(r.err)
			}
			if tc.name == "letterbox" && b.TextReady(0) {
				t.Fatal("painting a PDF started text extraction")
			}
			if r.state.Spread {
				if page, _, _, ok := r.pagePoint(.8, .15); !ok || page != 1 {
					t.Fatal("right spread slot maps to the wrong page")
				}
				if _, _, _, ok := r.pagePoint(.5, .5); ok {
					t.Fatal("spread gap maps to a page")
				}
			}
			r.state.Page = 1 // a turn awaiting paint must not change the clicked page
			done := make(chan book.Link, 1)
			r.click(tc.x, tc.y, func(link book.Link, ok bool) {
				if !ok {
					link.Page = -1
				}
				done <- link
			})
			select {
			case link := <-done:
				if link.URL != "" || link.Page != 1 {
					t.Fatalf("internal link: %#v", link)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("PDF click did not complete")
			}
		})
	}
	r := &reader{book: b, state: State{Zoom: 1}}
	if r.frame(400, 200) == nil {
		t.Fatal(r.err)
	}
	done := make(chan book.Link, 1)
	r.click(.1, .35, func(link book.Link, _ bool) { done <- link })
	select {
	case link := <-done:
		if link.URL != "https://example.com/read?id=7" || link.Page != -1 {
			t.Fatalf("external link: %#v", link)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("external link did not complete")
	}
	r.state = State{Spread: true, Zoom: 2, Offset: .5, Scroll: 3}
	r.pendingScroll = 50
	r.goTo(1)
	if r.state.Page != 1 || r.state.Zoom != 1 || r.state.CenterX != .5 || r.state.CenterY != .5 || r.state.Offset != 0 || r.state.Scroll != 0 || r.pendingScroll != 0 {
		t.Fatalf("link destination kept a stale view: %#v", r.state)
	}
	r.state = State{Webtoon: true, Offset: .1}
	r.scaledPages = make(map[scaledPageKey]image.Image)
	if r.frame(200, 150) == nil {
		t.Fatal(r.err)
	}
	if page, x, y, ok := r.pagePoint(.1, .7); !ok || page != 1 || math.Abs(x-.1) > 1e-9 || math.Abs(y-.15) > 1e-9 {
		t.Fatalf("second webtoon page: %d,%v,%v,%v", page, x, y, ok)
	}
}
