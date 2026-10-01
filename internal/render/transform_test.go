package render

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func TestInvertPreservesAlphaBoundsAndSource(t *testing.T) {
	src := image.NewRGBA(image.Rect(2, 3, 6, 5))
	for i, c := range []color.RGBA{{255, 255, 255, 255}, {0, 0, 0, 255}, {20, 40, 80, 128}, {0, 0, 0, 0}} {
		src.SetRGBA(2+i, 3, c)
	}
	before := append([]byte(nil), src.Pix...)
	crop := src.SubImage(image.Rect(3, 3, 5, 4))
	for _, img := range []image.Image{src, crop, image.NewNRGBA(src.Bounds()), image.NewGray(src.Bounds())} {
		got := Invert(img)
		if got.Bounds() != img.Bounds() {
			t.Fatalf("bounds = %v, want %v", got.Bounds(), img.Bounds())
		}
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				want := color.RGBA{uint8(a>>8) - uint8(r>>8), uint8(a>>8) - uint8(g>>8), uint8(a>>8) - uint8(b>>8), uint8(a >> 8)}
				if got.RGBAAt(x, y) != want {
					t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got.RGBAAt(x, y), want)
				}
			}
		}
	}
	if !bytes.Equal(src.Pix, before) || !bytes.Equal(Invert(Invert(src)).Pix, before) {
		t.Fatal("inversion changed source pixels or failed to restore colors")
	}
}

func TestRotateQuarterTurnsClockwise(t *testing.T) {
	src := image.NewNRGBA(image.Rect(1, 1, 3, 4)) // 2 wide, 3 tall, offset bounds
	for y := range 3 {
		for x := range 2 {
			src.SetNRGBA(1+x, 1+y, color.NRGBA{uint8(x), uint8(y), 0, 255})
		}
	}
	at := func(img image.Image, x, y int) color.RGBA { return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA) }
	cases := []struct {
		q, w, h int
		pos     func(x, y int) (int, int) // where source pixel (x, y) lands
	}{
		{1, 3, 2, func(x, y int) (int, int) { return 2 - y, x }},
		{2, 2, 3, func(x, y int) (int, int) { return 1 - x, 2 - y }},
		{3, 3, 2, func(x, y int) (int, int) { return y, 1 - x }},
	}
	for _, c := range cases {
		got := rotate(src, c.q)
		if got.Bounds() != image.Rect(0, 0, c.w, c.h) {
			t.Fatalf("q=%d bounds = %v", c.q, got.Bounds())
		}
		for y := range 3 {
			for x := range 2 {
				dx, dy := c.pos(x, y)
				if want := (color.RGBA{uint8(x), uint8(y), 0, 255}); at(got, dx, dy) != want {
					t.Fatalf("q=%d pixel (%d,%d) = %v, want %v", c.q, dx, dy, at(got, dx, dy), want)
				}
			}
		}
	}
	// RGBA input skips the conversion copy, including a sub-image with offset bounds.
	rgba := rotate(src, 2).(*image.RGBA)
	sub := rgba.SubImage(image.Rect(1, 1, 2, 3))
	if got := rotate(sub, 1); at(got, 1, 0) != at(sub, 1, 1) || at(got, 0, 0) != at(sub, 1, 2) {
		t.Fatalf("sub-image rotation: %v %v", at(got, 1, 0), at(got, 0, 0))
	}
	if back := rotate(rotate(rgba, 1), 1); at(back, 0, 0) != at(src, 1, 1) || at(back, 1, 2) != at(src, 2, 3) {
		t.Fatal("four quarter turns did not restore the image")
	}
}

func TestHighlightTintsRectanglesOnly(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	src.SetRGBA(0, 0, color.RGBA{0, 0, 0, 255})
	before := append([]byte(nil), src.Pix...)
	got := Highlight(src, []image.Rectangle{image.Rect(0, 0, 2, 1), image.Rect(3, 3, 9, 9)})
	if !bytes.Equal(src.Pix, before) {
		t.Fatal("highlight changed the source")
	}
	if got.RGBAAt(0, 0) != (color.RGBA{64, 88, 127, 255}) || got.RGBAAt(1, 0) != (color.RGBA{191, 215, 255, 255}) {
		t.Fatalf("tinted pixels: %v %v", got.RGBAAt(0, 0), got.RGBAAt(1, 0))
	}
	if got.RGBAAt(2, 0) != (color.RGBA{255, 255, 255, 255}) || got.RGBAAt(3, 3) == (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("rectangle edges: %v %v", got.RGBAAt(2, 0), got.RGBAAt(3, 3))
	}
}
