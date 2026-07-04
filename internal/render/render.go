// Package render draws images into terminal cells.
//
// Two backends, modeled on yazi's adapter approach:
//   - kitty graphics protocol with Unicode placeholders (kitty, ghostty; tmux passthrough)
//   - half-block cells (works everywhere)
package render

import "image"

// Result is a rendered image occupying Cols x Rows terminal cells.
type Result struct {
	Cols, Rows int
	Lines      []string // Rows lines, each exactly Cols cells wide
	Transmit   []byte   // out-of-band bytes to write to the tty before Lines are shown
}

// Renderer converts an image into terminal cells.
type Renderer interface {
	Name() string
	SetCellSize(cw, ch float64)
	Render(img image.Image, id uint32, maxCols, maxRows int) (Result, error)
	Delete(id uint32) []byte // bytes that free image id in the terminal (nil if n/a)
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
