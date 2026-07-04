package render

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// Kitty renders via the kitty graphics protocol using Unicode placeholders
// (virtual placements, U=1). Placeholder cells are plain text from the
// terminal's point of view, so they compose with bubbletea repaints and
// side-by-side joins. Supported by kitty and ghostty; tmux via passthrough.
type Kitty struct {
	cw, ch float64
	tmux   bool
}

// NewKitty creates the kitty renderer using the detected cell size.
func NewKitty() *Kitty {
	cw, ch := CellSize()
	k := &Kitty{cw: cw, ch: ch, tmux: os.Getenv("TMUX") != ""}
	if k.tmux {
		// tmux drops APC by default; needs tmux >= 3.3.
		exec.Command("tmux", "set", "-p", "allow-passthrough", "on").Run() //nolint:errcheck
	}
	return k
}

func (k *Kitty) Name() string { return "kitty" }

func (k *Kitty) SetCellSize(cw, ch float64) { k.cw, k.ch = cw, ch }

const chunkSize = 4096

func (k *Kitty) Render(img image.Image, id uint32, maxCols, maxRows int) (Result, error) {
	maxCols = min(maxCols, len(diacritics))
	maxRows = min(maxRows, len(diacritics))
	b := img.Bounds()
	cols, rows, _, _ := fitBox(b.Dx(), b.Dy(), maxCols, maxRows, k.cw, k.ch)

	// Scale to exactly the grid's pixel size. The terminal fits the bitmap
	// to the placement grid anchored top-left; any aspect slack would show
	// as an off-center image, so leave none.
	pw := max(1, int(float64(cols)*k.cw+0.5))
	ph := max(1, int(float64(rows)*k.ch+0.5))
	dst := image.NewRGBA(image.Rect(0, 0, pw, ph))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)

	var pngBuf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&pngBuf, dst); err != nil {
		return Result{}, err
	}
	payload := base64.StdEncoding.EncodeToString(pngBuf.Bytes())

	var out bytes.Buffer
	first := true
	for len(payload) > 0 {
		n := min(chunkSize, len(payload))
		chunk := payload[:n]
		payload = payload[n:]
		m := 0
		if len(payload) > 0 {
			m = 1
		}
		if first {
			k.apc(&out, fmt.Sprintf("a=T,q=2,f=100,t=d,i=%d,U=1,c=%d,r=%d,m=%d", id, cols, rows, m), chunk)
			first = false
		} else {
			k.apc(&out, fmt.Sprintf("m=%d", m), chunk)
		}
	}

	// Placeholder grid: fg color carries the image id (24-bit), each cell is
	// U+10EEEE + row diacritic + column diacritic.
	fg := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", (id>>16)&0xff, (id>>8)&0xff, id&0xff)
	lines := make([]string, rows)
	var sb strings.Builder
	for r := 0; r < rows; r++ {
		sb.Reset()
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
	k.apc(&out, fmt.Sprintf("a=d,d=I,i=%d,q=2", id), "")
	return out.Bytes()
}

// apc writes one APC G sequence, wrapped for tmux passthrough when needed.
func (k *Kitty) apc(w *bytes.Buffer, opts, payload string) {
	seq := "\x1b_G" + opts
	if payload != "" {
		seq += ";" + payload
	}
	seq += "\x1b\\"
	if k.tmux {
		seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	w.WriteString(seq)
}
