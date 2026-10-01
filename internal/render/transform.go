package render

import (
	"image"
	"image/draw"
)

type subImager interface {
	SubImage(image.Rectangle) image.Image
}

// Transform applies a clockwise quarter-turn rotation and a zoom crop.
// rot is quarter turns (0-3), zoom >= 1, cx/cy locate the visible center
// as fractions of the (rotated) image.
func Transform(img image.Image, rot int, zoom, cx, cy float64) image.Image {
	img = rotate(img, rot&3)
	if zoom <= 1.001 {
		return img
	}
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	vw, vh := w/zoom, h/zoom
	x0 := clampf(cx*w-vw/2, 0, w-vw)
	y0 := clampf(cy*h-vh/2, 0, h-vh)
	r := image.Rect(b.Min.X+int(x0), b.Min.Y+int(y0), b.Min.X+int(x0+vw), b.Min.Y+int(y0+vh))
	if s, ok := img.(subImager); ok {
		return s.SubImage(r)
	}
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			dst.Set(x, y, img.At(r.Min.X+x, r.Min.Y+y))
		}
	}
	return dst
}

// Invert returns a color-inverted copy, preserving alpha and image bounds.
func Invert(src image.Image) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	for i := 0; i < len(dst.Pix); i += 4 {
		a := dst.Pix[i+3]
		dst.Pix[i] = a - dst.Pix[i]
		dst.Pix[i+1] = a - dst.Pix[i+1]
		dst.Pix[i+2] = a - dst.Pix[i+2]
	}
	return dst
}

// Highlight returns a copy of src with the rectangles tinted toward light
// blue, halfway, so the text under a selection stays legible on plain and
// inverted pages.
func Highlight(src image.Image, rects []image.Rectangle) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	tint := [3]uint16{128, 176, 255}
	for _, r := range rects {
		r = r.Intersect(dst.Bounds())
		for y := r.Min.Y; y < r.Max.Y; y++ {
			i := dst.PixOffset(r.Min.X, y)
			for x := r.Min.X; x < r.Max.X; x++ {
				for c, t := range tint {
					dst.Pix[i+c] = uint8((uint16(dst.Pix[i+c]) + t) / 2)
				}
				i += 4
			}
		}
	}
	return dst
}

// rotate turns src by q quarter turns clockwise. Pixels move through an
// RGBA copy; At and Set would box a color per pixel.
func rotate(src image.Image, q int) image.Image {
	if q == 0 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	rgba, ok := src.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(b)
		draw.Draw(rgba, b, src, b.Min, draw.Src)
	}
	dw, dh := h, w
	if q == 2 {
		dw, dh = w, h
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		row := rgba.Pix[rgba.PixOffset(b.Min.X, b.Min.Y+y):]
		for x := 0; x < w; x++ {
			var i int
			switch q {
			case 1: // 90 cw
				i = dst.PixOffset(h-1-y, x)
			case 2:
				i = dst.PixOffset(w-1-x, h-1-y)
			default: // 270 cw
				i = dst.PixOffset(y, w-1-x)
			}
			*(*[4]byte)(dst.Pix[i:]) = [4]byte(row[4*x:])
		}
	}
	return dst
}

func clampf(v, lo, hi float64) float64 {
	if hi < lo {
		hi = lo
	}
	return min(hi, max(lo, v))
}
