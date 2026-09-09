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

	img, page, offset, err := ComposeWebtoon(load, len(pages), 0, 0, 150, 100, 100, 0, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page != 0 || math.Abs(offset-0.75) > 0.001 {
		t.Fatalf("position = page %d offset %.3f, want page 0 offset 0.75", page, offset)
	}
	assertPixel(t, img, 50, 25, color.RGBA{R: 255, A: 255})
	assertPixel(t, img, 50, 75, color.RGBA{B: 255, A: 255})

	img, page, offset, err = ComposeWebtoon(load, len(pages), 0, 0, 250, 100, 100, 0, 1, 1)
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

	img, page, offset, err := ComposeWebtoon(load, len(pages), 1, 0.25, -100, 100, 100, 0, 1, 1)
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

	img, page, offset, err := ComposeWebtoon(load, len(pages), 1, 1, 0, 100, 100, 0, 1, 1)
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

	img, _, _, err := ComposeWebtoon(load, 1, 0, 0, 0, 100, 100, 0, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertPixel(t, img, 49, 50, pageColor)
	assertPixel(t, img, 10, 50, color.RGBA{})
	assertPixel(t, img, 90, 50, color.RGBA{})
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
