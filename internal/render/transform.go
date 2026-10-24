package render

import (
	"image"
	"image/draw"
	"sync"
)

type subImager interface {
	SubImage(image.Rectangle) image.Image
}

// Transform applies a clockwise quarter-turn rotation and a zoom crop.
// rot is quarter turns (0-3), zoom >= 1, cx/cy locate the visible center
// as fractions of the (rotated) image. The result may share pixels with src
// or with other callers; treat it as read-only and never Recycle it.
// Rotations are cached by image, so src must not change afterwards unless it
// goes through Recycle.
func Transform(img image.Image, rot int, zoom, cx, cy float64) image.Image {
	img = rotate(img, rot&3)
	if zoom <= 1.001 {
		return img
	}
	b := img.Bounds()
	if b.Empty() {
		return img
	}
	w, h := float64(b.Dx()), float64(b.Dy())
	vw, vh := max(1, w/zoom), max(1, h/zoom)
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
	dst := getRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	invertRect(dst, dst.Bounds())
	return dst
}

// invertRect inverts the premultiplied colors inside r in place.
func invertRect(img *image.RGBA, r image.Rectangle) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[img.PixOffset(r.Min.X, y):img.PixOffset(r.Max.X, y)]
		for i := 0; i < len(row); i += 4 {
			a := row[i+3]
			row[i], row[i+1], row[i+2] = a-row[i], a-row[i+1], a-row[i+2]
		}
	}
}

// Highlight returns a copy of src with the rectangles tinted toward light
// blue, halfway, so the text under a selection stays legible on plain and
// inverted pages.
func Highlight(src image.Image, rects []image.Rectangle) *image.RGBA {
	dst := getRGBA(src.Bounds())
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

// rotations keeps the latest quarter turns. Panning or zooming a rotated
// page and every rotated webtoon frame turn the same pages again; two
// entries cover a spread, two panes or the pages of a webtoon frame.
var rotations struct {
	sync.Mutex
	entries [2]rotation
	next    int
}

type rotation struct {
	src image.Image
	q   int
	dst *image.RGBA
}

// rotate turns src by q quarter turns clockwise. Pixels move through an
// RGBA copy; At and Set would box a color per pixel. The result is shared
// with later calls for the same src.
func rotate(src image.Image, q int) image.Image {
	if q == 0 {
		return src
	}
	rotations.Lock()
	for _, r := range rotations.entries {
		if r.dst != nil && r.src == src && r.q == q {
			rotations.Unlock()
			return r.dst
		}
	}
	rotations.Unlock()
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	rgba, ok := src.(*image.RGBA)
	if !ok {
		rgba = getRGBA(b)
		defer Recycle(rgba)
		draw.Draw(rgba, b, src, b.Min, draw.Src)
	}
	dw, dh := h, w
	if q == 2 {
		dw, dh = w, h
	}
	dst := getRGBA(image.Rect(0, 0, dw, dh))
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
	rotations.Lock()
	rotations.entries[rotations.next] = rotation{src, q, dst}
	rotations.next = (rotations.next + 1) % len(rotations.entries)
	rotations.Unlock()
	return dst
}

// forgetRotations drops the turns of img, or the turn that is img, before
// its pixels change.
func forgetRotations(img *image.RGBA) {
	rotations.Lock()
	defer rotations.Unlock()
	for i, r := range rotations.entries {
		if r.src == image.Image(img) || r.dst == img {
			rotations.entries[i] = rotation{}
		}
	}
}

func clampf(v, lo, hi float64) float64 {
	if hi < lo {
		hi = lo
	}
	return min(hi, max(lo, v))
}
