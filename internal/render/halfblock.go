package render

import (
	"image"
	"strconv"
	"sync"

	xdraw "golang.org/x/image/draw"
)

// HalfBlock renders with U+2580 (upper half block): two pixels per cell
// using truecolor fg/bg. Works in any 24-bit color terminal.
type HalfBlock struct {
	mu     sync.Mutex
	cw, ch float64
}

// NewHalfBlock creates the half-block renderer using the detected cell size.
func NewHalfBlock() *HalfBlock {
	cw, ch := CellSize()
	return &HalfBlock{cw: cw, ch: ch}
}

func (h *HalfBlock) Name() string { return "halfblock" }

func (h *HalfBlock) SetCellSize(cw, ch float64) { h.mu.Lock(); h.cw, h.ch = cw, ch; h.mu.Unlock() }

func (h *HalfBlock) Render(img image.Image, _ uint32, maxCols, maxRows int, invert bool) (Result, error) {
	h.mu.Lock()
	cw, ch := h.cw, h.ch
	h.mu.Unlock()
	b := img.Bounds()
	// Aspect: cell is cw x ch px, and a cell holds 1x2 "pixels".
	cols, rows, _, _ := fitBox(b.Dx(), b.Dy(), maxCols, maxRows, cw, ch)

	dst := getRGBA(image.Rect(0, 0, cols, rows*2))
	defer Recycle(dst)
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	if invert {
		invertRect(dst, dst.Bounds())
	}

	lines := make([]string, rows)
	var line []byte
	for r := 0; r < rows; r++ {
		top := dst.Pix[dst.PixOffset(0, 2*r):]
		bottom := dst.Pix[dst.PixOffset(0, 2*r+1):]
		line = line[:0]
		var fg, bg [3]byte
		for c := 0; c < cols; c++ {
			// Colors repeat across flat areas; only changes need a sequence.
			if t := [3]byte(top[4*c:]); c == 0 || t != fg {
				line, fg = appendColor(line, "\x1b[38;2;", t), t
			}
			if u := [3]byte(bottom[4*c:]); c == 0 || u != bg {
				line, bg = appendColor(line, "\x1b[48;2;", u), u
			}
			line = append(line, "\u2580"...)
		}
		line = append(line, "\x1b[0m"...)
		lines[r] = string(line)
	}
	return Result{Cols: cols, Rows: rows, Lines: lines}, nil
}

// appendColor appends a truecolor SGR sequence.
func appendColor(b []byte, prefix string, c [3]byte) []byte {
	b = append(b, prefix...)
	b = strconv.AppendUint(b, uint64(c[0]), 10)
	b = append(b, ';')
	b = strconv.AppendUint(b, uint64(c[1]), 10)
	b = append(b, ';')
	b = strconv.AppendUint(b, uint64(c[2]), 10)
	return append(b, 'm')
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
