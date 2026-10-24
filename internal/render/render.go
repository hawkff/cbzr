// Package render draws images into terminal cells.
//
// Two backends, modeled on yazi's adapter approach:
//   - kitty graphics protocol with Unicode placeholders (kitty, ghostty; tmux passthrough)
//   - half-block cells (works everywhere)
package render

import (
	"image"
	"sync"
)

// Result is a rendered image occupying Cols x Rows terminal cells.
type Result struct {
	Cols, Rows int
	Lines      []string // Rows lines, each exactly Cols cells wide
	Transmit   []byte   // out-of-band bytes to write to the tty before Lines are shown
}

// Renderer converts an image into terminal cells. With invert, it inverts the
// colors after scaling, which costs far less than inverting a full page.
type Renderer interface {
	Name() string
	SetCellSize(cw, ch float64)
	Render(img image.Image, id uint32, maxCols, maxRows int, invert bool) (Result, error)
	Delete(id uint32) []byte // bytes that free image id in the terminal (nil if n/a)
}

// Frames and scaled copies take megabytes each and live for one render, so
// they come from a pool.
var rgbaPool sync.Pool

// getRGBA returns an image for r with unspecified pixels.
func getRGBA(r image.Rectangle) *image.RGBA {
	n := 4 * r.Dx() * r.Dy()
	if img, ok := rgbaPool.Get().(*image.RGBA); ok && cap(img.Pix) >= n {
		img.Pix, img.Stride, img.Rect = img.Pix[:n], 4*r.Dx(), r
		return img
	}
	return image.NewRGBA(r)
}

// Recycle hands back a frame from ComposeWebtoon, Invert or Highlight once
// nothing refers to it any more.
func Recycle(img *image.RGBA) {
	forgetRotations(img)
	rgbaPool.Put(img)
}

// fitBox scales iw x ih pixels to fit maxCols x maxRows cells of cw x ch
// pixels each, preserving aspect ratio. Returns the cell grid and pixel size.
func fitBox(iw, ih, maxCols, maxRows int, cw, ch float64) (cols, rows, pw, ph int) {
	if iw <= 0 || ih <= 0 || maxCols < 1 || maxRows < 1 {
		return 1, 1, 1, 1
	}
	boxW := float64(maxCols) * cw
	boxH := float64(maxRows) * ch
	s := boxW / float64(iw)
	if r := boxH / float64(ih); r < s {
		s = r
	}
	pw = max(1, int(float64(iw)*s))
	ph = max(1, int(float64(ih)*s))
	cols = min(maxCols, max(1, int((float64(pw)+cw-1)/cw)))
	rows = min(maxRows, max(1, int((float64(ph)+ch-1)/ch)))
	return cols, rows, pw, ph
}
