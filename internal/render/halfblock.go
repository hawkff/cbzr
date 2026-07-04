package render

import (
	"fmt"
	"image"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// HalfBlock renders with U+2580 (upper half block): two pixels per cell
// using truecolor fg/bg. Works in any 24-bit color terminal.
type HalfBlock struct {
	cw, ch float64
}

// NewHalfBlock creates the half-block renderer using the detected cell size.
func NewHalfBlock() *HalfBlock {
	cw, ch := CellSize()
	return &HalfBlock{cw: cw, ch: ch}
}

func (h *HalfBlock) Name() string { return "halfblock" }

func (h *HalfBlock) SetCellSize(cw, ch float64) { h.cw, h.ch = cw, ch }

func (h *HalfBlock) Render(img image.Image, _ uint32, maxCols, maxRows int) (Result, error) {
	b := img.Bounds()
	// Aspect: cell is cw x ch px, and a cell holds 1x2 "pixels".
	cols, rows, _, _ := fitBox(b.Dx(), b.Dy(), maxCols, maxRows, h.cw, h.ch)

	dst := image.NewRGBA(image.Rect(0, 0, cols, rows*2))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)

	lines := make([]string, rows)
	var sb strings.Builder
	for r := 0; r < rows; r++ {
		sb.Reset()
		for c := 0; c < cols; c++ {
			tr, tg, tb, _ := dst.At(c, r*2).RGBA()
			br, bg, bb, _ := dst.At(c, r*2+1).RGBA()
			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm\u2580",
				tr>>8, tg>>8, tb>>8, br>>8, bg>>8, bb>>8)
		}
		sb.WriteString("\x1b[0m")
		lines[r] = sb.String()
	}
	return Result{Cols: cols, Rows: rows, Lines: lines}, nil
}

func (h *HalfBlock) Delete(uint32) []byte { return nil }

// Detect picks the best renderer for the current terminal, like yazi probes
// TERM/TERM_PROGRAM before choosing an adapter.
func Detect(force string) Renderer {
	switch force {
	case "kitty":
		return NewKitty()
	case "halfblock":
		return NewHalfBlock()
	}
	if supportsKitty() {
		return NewKitty()
	}
	return NewHalfBlock()
}
