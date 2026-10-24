package render

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"
)

func TestComposeWebtoonScrollsAcrossPages(t *testing.T) {
	pages := []image.Image{
		solidPage(100, 200, color.RGBA{R: 255, A: 255}),
		solidPage(100, 200, color.RGBA{B: 255, A: 255}),
	}
	load := func(i int) (image.Image, error) { return pages[i], nil }

	img, page, offset, err := ComposeWebtoon(load, len(pages), 0, 0, 150, 100, 100, 0, 1, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if page != 0 || math.Abs(offset-0.75) > 0.001 {
		t.Fatalf("position = page %d offset %.3f, want page 0 offset 0.75", page, offset)
	}
	assertPixel(t, img, 50, 25, color.RGBA{R: 255, A: 255})
	assertPixel(t, img, 50, 75, color.RGBA{B: 255, A: 255})

	img, page, offset, err = ComposeWebtoon(load, len(pages), 0, 0, 250, 100, 100, 0, 1, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if page != 1 || math.Abs(offset-0.25) > 0.001 {
		t.Fatalf("position = page %d offset %.3f, want page 1 offset 0.25", page, offset)
	}
	assertPixel(t, img, 50, 50, color.RGBA{B: 255, A: 255})
}

func TestComposeWebtoonScrollsBackwardAcrossPage(t *testing.T) {
	pages := []image.Image{
		solidPage(100, 200, color.RGBA{R: 255, A: 255}),
		solidPage(100, 200, color.RGBA{B: 255, A: 255}),
	}
	load := func(i int) (image.Image, error) { return pages[i], nil }

	img, page, offset, err := ComposeWebtoon(load, len(pages), 1, 0.25, -100, 100, 100, 0, 1, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if page != 0 || math.Abs(offset-0.75) > 0.001 {
		t.Fatalf("position = page %d offset %.3f, want page 0 offset 0.75", page, offset)
	}
	assertPixel(t, img, 50, 25, color.RGBA{R: 255, A: 255})
	assertPixel(t, img, 50, 75, color.RGBA{B: 255, A: 255})
}

func TestComposeWebtoonKeepsStripEndAtViewportBottom(t *testing.T) {
	pages := []image.Image{
		solidPage(100, 200, color.RGBA{R: 255, A: 255}),
		solidPage(100, 50, color.RGBA{B: 255, A: 255}),
	}
	load := func(i int) (image.Image, error) { return pages[i], nil }

	img, page, offset, err := ComposeWebtoon(load, len(pages), 1, 1, 0, 100, 100, 0, 1, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if page != 0 || math.Abs(offset-0.75) > 0.001 {
		t.Fatalf("position = page %d offset %.3f, want page 0 offset 0.75", page, offset)
	}
	assertPixel(t, img, 50, 25, color.RGBA{R: 255, A: 255})
	assertPixel(t, img, 50, 75, color.RGBA{B: 255, A: 255})
}

func TestComposeWebtoonDoesNotUpscaleNarrowPages(t *testing.T) {
	pageColor := color.RGBA{G: 255, A: 255}
	pages := []image.Image{solidPage(40, 200, pageColor)}
	load := func(i int) (image.Image, error) { return pages[i], nil }

	img, _, _, err := ComposeWebtoon(load, 1, 0, 0, 0, 100, 100, 0, 1, 1, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertPixel(t, img, 49, 50, pageColor)
	assertPixel(t, img, 10, 50, color.RGBA{})
	assertPixel(t, img, 90, 50, color.RGBA{})
}

func TestComposeWebtoonInvertsShownPages(t *testing.T) {
	pages := []image.Image{
		solidPage(100, 60, color.RGBA{R: 255, A: 255}),
		solidPage(40, 60, color.RGBA{B: 255, A: 255}),
		solidPage(100, 60, color.RGBA{G: 255, A: 255}),
	}
	load := func(i int) (image.Image, error) { return pages[i], nil }
	// Only the narrow middle page inverts; its blank margins stay transparent.
	img, _, _, err := ComposeWebtoon(load, len(pages), 0, 0, 30, 100, 100, 0, 1, 1, 1, func(i int) bool { return i == 1 })
	if err != nil {
		t.Fatal(err)
	}
	assertPixel(t, img, 50, 10, color.RGBA{R: 255, A: 255})
	assertPixel(t, img, 50, 50, color.RGBA{R: 255, G: 255, A: 255})
	assertPixel(t, img, 10, 50, color.RGBA{})
	assertPixel(t, img, 50, 95, color.RGBA{G: 255, A: 255})
	if pages[1].(*image.RGBA).Pix[2] != 255 {
		t.Fatal("inversion changed the loaded page")
	}
}

func TestComposeWebtoonAtCellResolutionKeepsLayout(t *testing.T) {
	red, blue := color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}
	pages := []image.Image{solidPage(40, 400, red), solidPage(400, 400, blue)}
	load := func(i int) (image.Image, error) { return pages[i], nil }
	// 25x25 cells of 8x16 pixels, scrolled 10 rows: the full frame is
	// 200x400; one pixel per cell across gives 25x50.
	full, fullPage, fullOffset, err := ComposeWebtoon(load, 2, 0, 0, 10, 25, 25, 0, 8, 16, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	small, page, offset, err := ComposeWebtoon(load, 2, 0, 0, 10, 25, 25, 0, 8, 16, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	if full.Bounds().Size() != image.Pt(200, 400) || small.Bounds().Size() != image.Pt(25, 50) {
		t.Fatalf("frames %v and %v", full.Bounds(), small.Bounds())
	}
	if page != fullPage || math.Abs(offset-fullOffset) > 1e-9 || math.Abs(offset-0.4) > 1e-9 {
		t.Fatalf("position = page %d offset %.3f, full frame page %d offset %.3f", page, offset, fullPage, fullOffset)
	}
	// The narrow page keeps a fifth of the width; the wide one fills it.
	assertPixel(t, small, 12, 10, red)
	assertPixel(t, small, 5, 10, color.RGBA{})
	assertPixel(t, small, 12, 40, blue)
	assertPixel(t, full, 100, 80, red)
	assertPixel(t, full, 40, 80, color.RGBA{})
	// Cells of 8.4x17 pixels make a frame 50.6 pixels tall; rounded up, it
	// would fit its cells by height and leave a column blank.
	frame, _, _, err := ComposeWebtoon(load, 2, 0, 0, 0, 200, 25, 0, 8.4, 17, 8.4, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := (&HalfBlock{cw: 8.4, ch: 17}).Render(frame, 0, 200, 25, false)
	if err != nil || res.Cols != 200 || res.Rows != 25 {
		t.Fatalf("half-block grid %dx%d, %v", res.Cols, res.Rows, err)
	}
}

func solidPage(width, height int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	return img
}

func assertPixel(t *testing.T, img image.Image, x, y int, want color.RGBA) {
	t.Helper()
	got := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
	if got != want {
		t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got, want)
	}
}
