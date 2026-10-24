package render

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"os"
	"os/exec"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
)

// Kitty renders via the kitty graphics protocol using Unicode placeholders
// (virtual placements, U=1). Placeholder cells are plain text from the
// terminal's point of view, so they compose with bubbletea repaints and
// side-by-side joins. Supported by kitty and ghostty; tmux via passthrough.
type Kitty struct {
	mu     sync.Mutex
	cw, ch float64
	tmux   bool
	base   uint32 // offsets image ids: tmux panes of one terminal share its images
}

// NewKitty creates the kitty renderer using the detected cell size.
func NewKitty() *Kitty {
	cw, ch := CellSize()
	// Placeholders carry the id in a 24-bit color, so the base stays below 2^24.
	k := &Kitty{cw: cw, ch: ch, tmux: os.Getenv("TMUX") != "", base: uint32(os.Getpid()%0xffff) << 8}
	if k.tmux {
		// tmux drops APC by default; needs tmux >= 3.3.
		exec.Command("tmux", "set", "-p", "allow-passthrough", "on").Run() //nolint:errcheck
	}
	return k
}

func (k *Kitty) Name() string { return "kitty" }

func (k *Kitty) SetCellSize(cw, ch float64) { k.mu.Lock(); k.cw, k.ch = cw, ch; k.mu.Unlock() }

// chunkSize bytes of PNG fill one 4096-byte base64 chunk, the protocol's limit.
const chunkSize = 3072

var pngBufs = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func (k *Kitty) Render(img image.Image, id uint32, maxCols, maxRows int, invert bool) (Result, error) {
	k.mu.Lock()
	cw, ch := k.cw, k.ch
	k.mu.Unlock()
	id += k.base
	maxCols = min(maxCols, len(diacritics))
	maxRows = min(maxRows, len(diacritics))
	b := img.Bounds()
	cols, rows, _, _ := fitBox(b.Dx(), b.Dy(), maxCols, maxRows, cw, ch)

	// Scale to exactly the grid's pixel size. The terminal fits the bitmap
	// to the placement grid anchored top-left; any aspect slack would show
	// as an off-center image, so leave none.
	pw := max(1, int(float64(cols)*cw+0.5))
	ph := max(1, int(float64(rows)*ch+0.5))
	src, exact := img.(*image.RGBA)
	if !exact || invert || src.Rect != image.Rect(0, 0, pw, ph) {
		// Webtoon frames arrive at exactly this size and skip the copy.
		src = getRGBA(image.Rect(0, 0, pw, ph))
		defer Recycle(src)
		xdraw.ApproxBiLinear.Scale(src, src.Bounds(), img, b, xdraw.Src, nil)
		if invert {
			invertRect(src, src.Bounds())
		}
	}

	pngBuf := pngBufs.Get().(*bytes.Buffer)
	defer pngBufs.Put(pngBuf)
	pngBuf.Reset()
	if err := encodePNG(pngBuf, src); err != nil {
		return Result{}, err
	}
	data := pngBuf.Bytes()

	var out bytes.Buffer
	chunks := (len(data) + chunkSize - 1) / chunkSize
	out.Grow(128 + chunks*(base64.StdEncoding.EncodedLen(chunkSize)+32))
	// Kill the previous image and its virtual placement first. Reusing an
	// id with different c/r otherwise leaves the old placement geometry
	// live: placeholder cells outside it stay blank (half-drawn pages) and
	// narrower grids show a slice of the old mapping.
	k.apc(&out, fmt.Sprintf("a=d,d=I,i=%d,q=2", id), nil)
	for first := true; len(data) > 0; first = false {
		n := min(chunkSize, len(data))
		chunk := data[:n]
		data = data[n:]
		opts := "m=1"
		if len(data) == 0 {
			opts = "m=0"
		}
		if first {
			opts = fmt.Sprintf("a=T,q=2,f=100,t=d,i=%d,U=1,c=%d,r=%d,%s", id, cols, rows, opts)
		}
		k.apc(&out, opts, chunk)
	}

	// Placeholder grid: fg color carries the image id (24-bit), each cell is
	// U+10EEEE + row diacritic + column diacritic.
	fg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", (id>>16)&0xff, (id>>8)&0xff, id&0xff)
	lines := make([]string, rows)
	var sb strings.Builder
	for r := 0; r < rows; r++ {
		sb.Reset()
		sb.Grow(len(fg) + 8*cols + 5) // a 4-byte placeholder and two 2-byte diacritics per cell
		sb.WriteString(fg)
		for c := 0; c < cols; c++ {
			sb.WriteRune(0x10EEEE)
			sb.WriteRune(diacritics[r])
			sb.WriteRune(diacritics[c])
		}
		sb.WriteString("\x1b[39m")
		lines[r] = sb.String()
	}
	return Result{Cols: cols, Rows: rows, Lines: lines, Transmit: out.Bytes()}, nil
}

// Delete frees image id (and its placements) in the terminal.
func (k *Kitty) Delete(id uint32) []byte {
	var out bytes.Buffer
	k.apc(&out, fmt.Sprintf("a=d,d=I,i=%d,q=2", id+k.base), nil)
	return out.Bytes()
}

// apc writes one APC G sequence with a base64 payload, wrapped for tmux
// passthrough when needed. tmux wants every ESC doubled; base64 has none,
// so only the framing changes.
func (k *Kitty) apc(w *bytes.Buffer, opts string, payload []byte) {
	if k.tmux {
		w.WriteString("\x1bPtmux;\x1b")
	}
	w.WriteString("\x1b_G")
	w.WriteString(opts)
	if len(payload) > 0 {
		w.WriteByte(';')
		w.Write(base64.StdEncoding.AppendEncode(w.AvailableBuffer(), payload))
	}
	if k.tmux {
		w.WriteString("\x1b\x1b\\\x1b\\")
	} else {
		w.WriteString("\x1b\\")
	}
}
