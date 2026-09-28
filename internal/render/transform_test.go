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
