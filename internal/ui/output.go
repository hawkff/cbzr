package ui

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

// uploadMarker opens the APC sequence that stands in for a page upload in a
// frame, and paintMarker the one that names a sixel image on each row it
// covers. Terminals ignore APC sequences they don't know, and Bubble Tea
// measures them as zero width and keeps them when it truncates a line.
const (
	markerStart  = "\x1b_cbz"
	uploadMarker = markerStart + "r:"
	paintMarker  = markerStart + "p:"
	markerEnd    = "\x1b\\"
)

// uploads hands page uploads to the terminal writer by marker. Bubble Tea
// copies each frame into its buffer, copies it three more times per flush and
// parses every changed line twice. With a kitty upload of a few megabytes in
// the frame, a flush cost about 12 ms and 12 MB; with markers, 56 µs.
type uploads struct {
	mu      sync.Mutex
	views   uint64 // frames built so far
	next    uint64 // id for the next new upload
	entries []upload
}

type upload struct {
	data []byte
	id   uint64
	view uint64 // the last frame that showed it
}

func newUploads() *uploads { return &uploads{} }

// begin starts a frame. It keeps the uploads of the previous frame, which
// Bubble Tea may still be writing, and drops older ones; an upload that
// shows up again later gets a new marker, so its line goes out in full.
func (u *uploads) begin() {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	kept := u.entries[:0]
	for _, e := range u.entries {
		if e.view == u.views {
			kept = append(kept, e)
		}
	}
	clear(u.entries[len(kept):])
	u.entries = kept
	u.views++
}

// appendMarker appends the marker for data to frame. An upload keeps its
// marker from frame to frame, so Bubble Tea sees its line unchanged and does
// not send it again. A nil table appends the upload itself.
func (u *uploads) appendMarker(frame, data []byte) []byte {
	if len(data) == 0 {
		return frame
	}
	if u == nil {
		return append(frame, data...)
	}
	frame = append(frame, uploadMarker...)
	frame = strconv.AppendUint(frame, u.register(data), 10)
	return append(frame, markerEnd...)
}

// register returns the id of data for this frame.
func (u *uploads) register(data []byte) uint64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	i := 0
	for i < len(u.entries) && (&u.entries[i].data[0] != &data[0] || len(u.entries[i].data) != len(data)) {
		i++
	}
	if i == len(u.entries) {
		u.next++
		u.entries = append(u.entries, upload{data: data, id: u.next})
	}
	u.entries[i].view = u.views
	return u.entries[i].id
}

// appendPaint appends the marker that names data, an image to draw with its
// top left corner on cell at, to a frame row the image covers. Bubble Tea
// writes only the rows that change; a written row erases the image under it,
// so the writer draws every image a written row names. A nil table appends
// nothing.
func (u *uploads) appendPaint(frame, data []byte, at image.Point) []byte {
	if u == nil || len(data) == 0 {
		return frame
	}
	frame = append(frame, paintMarker...)
	frame = strconv.AppendUint(frame, u.register(data), 10)
	frame = append(frame, ';')
	frame = strconv.AppendInt(frame, int64(at.Y), 10)
	frame = append(frame, ';')
	frame = strconv.AppendInt(frame, int64(at.X), 10)
	return append(frame, markerEnd...)
}

func (u *uploads) lookup(id uint64) []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, e := range u.entries {
		if e.id == id {
			return e.data
		}
	}
	return nil
}

// write sends p to w with every upload marker replaced by its upload, then
// draws each image that a paint marker in p names. A marker whose data is
// gone writes nothing.
func (u *uploads) write(w io.Writer, p []byte) (int, error) {
	if u == nil {
		return w.Write(p)
	}
	n := 0
	var paints []string
	for {
		i := bytes.Index(p, []byte(markerStart))
		if i < 0 {
			break
		}
		end := bytes.Index(p[i:], []byte(markerEnd))
		if end < 0 {
			break
		}
		marker := string(p[i : i+end])
		k, err := w.Write(p[:i])
		if err != nil {
			return n + k, err
		}
		if id, ok := strings.CutPrefix(marker, uploadMarker); ok {
			// Uploads never change after rendering, so the write needs no lock.
			if _, err := w.Write(u.lookup(parseID(id))); err != nil {
				return n + i, err
			}
		} else if strings.HasPrefix(marker, paintMarker) && !slices.Contains(paints, marker) {
			paints = append(paints, marker)
		}
		n += i + end + len(markerEnd)
		p = p[i+end+len(markerEnd):]
	}
	k, err := w.Write(p)
	if err != nil {
		return n + k, err
	}
	n += k
	for _, marker := range paints {
		var id uint64
		var row, col int
		fmt.Sscanf(marker[len(paintMarker):], "%d;%d;%d", &id, &row, &col) //nolint:errcheck
		data := u.lookup(id)
		if data == nil {
			continue
		}
		// Save the cursor, draw at the image's cell, and put the cursor back
		// where Bubble Tea left it.
		if _, err := fmt.Fprintf(w, "\x1b7\x1b[%d;%dH", row+1, col+1); err != nil {
			return n, err
		}
		if _, err := w.Write(data); err != nil {
			return n, err
		}
		if _, err := io.WriteString(w, "\x1b8"); err != nil {
			return n, err
		}
	}
	return n, nil
}

func parseID(s string) uint64 {
	id, _ := strconv.ParseUint(s, 10, 64)
	return id
}

// Output writes frames to the terminal. It splices page uploads in for their
// markers and wraps each frame in synchronized output, so the terminal never
// paints half a frame. Embedding the file keeps the descriptor Bubble Tea
// uses for resize detection.
type Output struct {
	*os.File
	uploads *uploads
}

// Output returns the terminal writer for this model's frames.
func (m Model) Output(f *os.File) Output { return Output{f, m.uploads} }

func (w Output) Write(p []byte) (int, error) {
	if _, err := w.File.WriteString(ansi.SetModeSynchronizedOutput); err != nil {
		return 0, err
	}
	n, err := w.uploads.write(w.File, p)
	if _, endErr := w.File.WriteString(ansi.ResetModeSynchronizedOutput); err == nil {
		err = endErr
	}
	return n, err
}
