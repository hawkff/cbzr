package ui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"cbzr/internal/book"
	"cbzr/internal/render"
)

// written returns the bytes Output sends for the model's frame.
func written(t *testing.T, m Model) string {
	t.Helper()
	var out bytes.Buffer
	if _, err := m.uploads.write(&out, []byte(m.View())); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestUploadsTravelByMarker(t *testing.T) {
	m := testModel()
	p := m.panes[0]
	p.book = &book.Book{Title: "Synthetic"}
	p.res = render.Result{Cols: 1, Rows: 1, Lines: []string{"x"}, Transmit: []byte("\x1b_Gfirst\x1b\\")}
	first := m.View()
	if strings.Contains(first, "first") || strings.Count(first, uploadMarker) != 1 {
		t.Fatalf("frame carries the upload instead of a marker: %q", first)
	}
	if m.View() != first {
		t.Fatal("the marker changed between frames, so the upload would go out again")
	}
	// Bubble Tea truncates and measures each line; the marker must survive both.
	if line := uploadMarker + "1" + markerEnd + "abc"; ansi.StringWidth(line) != 3 || !strings.HasPrefix(ansi.Truncate(line, 2, ""), uploadMarker+"1"+markerEnd) {
		t.Fatal("the terminal library no longer treats the marker as a zero-width sequence")
	}
	var out bytes.Buffer
	if n, err := m.uploads.write(&out, []byte(first)); err != nil || n != len(first) || strings.Count(out.String(), "\x1b_Gfirst\x1b\\") != 1 || strings.Contains(out.String(), uploadMarker) {
		t.Fatalf("spliced frame: %d, %v, %q", n, err, out.String())
	}

	// A new page gets a new marker; the previous frame, which Bubble Tea may
	// still be writing, keeps its upload for one more frame.
	p.res.Transmit = []byte("\x1b_Gsecond\x1b\\")
	second := m.View()
	out.Reset()
	m.uploads.write(&out, []byte(second)) //nolint:errcheck
	if second == first || !strings.Contains(out.String(), "second") {
		t.Fatal("a new upload kept the old marker")
	}
	out.Reset()
	m.uploads.write(&out, []byte(first)) //nolint:errcheck
	if !strings.Contains(out.String(), "first") {
		t.Fatal("the previous frame lost its upload")
	}
	m.View()
	out.Reset()
	m.uploads.write(&out, []byte(first)) //nolint:errcheck
	if strings.Contains(out.String(), "first") || strings.Contains(out.String(), uploadMarker) || len(m.uploads.entries) != 1 {
		t.Fatalf("an old upload stayed or its marker leaked: %q, %d kept", out.String(), len(m.uploads.entries))
	}
}

func TestOutputFramesAndSplicesUploads(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m := testModel()
	out := m.Output(f)
	if out.Fd() != f.Fd() {
		t.Fatal("output lost the terminal descriptor")
	}
	marked := m.uploads.appendMarker([]byte("a"), []byte("\x1b_Gimage\x1b\\"))
	frame := append(marked, "b"+uploadMarker+"99"+markerEnd+"c"...)
	if n, err := out.Write(frame); err != nil || n != len(frame) {
		t.Fatalf("frame write: %d, %v", n, err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || string(got) != "\x1b[?2026ha\x1b_Gimage\x1b\\bc\x1b[?2026l" {
		t.Fatalf("unframed output: %q, %v", got, err)
	}
	f.Close()
	if n, err := out.Write(frame); err == nil || n != 0 {
		t.Fatalf("failed output write: %d, %v", n, err)
	}
}

func TestSixelImagesRedrawWhenTheirRowsChange(t *testing.T) {
	m := testModel()
	m.renderer = render.NewSixel()
	p := m.panes[0]
	p.book = &book.Book{Title: "Synthetic"}
	img := []byte("\x1bP0;1;0q\"1;1;8;12#0;2;0;0;0#0~~\x1b\\")
	p.res = render.Result{Cols: 3, Rows: 2, Lines: []string{"   ", "   "}, Transmit: img, Paint: true}
	r, ok := m.shownRect(0, 0)
	if !ok {
		t.Fatal("the page is not shown")
	}
	frame := m.View()
	for y, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, paintMarker) != (y >= r.Min.Y && y < r.Max.Y) {
			t.Fatalf("row %d: marker %v, image rows %d-%d", y, strings.Contains(line, paintMarker), r.Min.Y, r.Max.Y-1)
		}
	}
	if strings.Contains(frame, string(img)) || m.View() != frame {
		t.Fatal("the frame carried the image or its marker changed")
	}
	// The image goes out once, after the rows that erased it, at its cell.
	var out bytes.Buffer
	if n, err := m.uploads.write(&out, []byte(frame)); err != nil || n != len(frame) {
		t.Fatalf("write: %d, %v", n, err)
	}
	draw := fmt.Sprintf("\x1b7\x1b[%d;%dH%s\x1b8", r.Min.Y+1, r.Min.X+1, img)
	if got := out.String(); strings.Count(got, draw) != 1 || !strings.HasSuffix(got, draw) || strings.Contains(got, paintMarker) {
		t.Fatalf("frame output %q", got)
	}
	// Bubble Tea writes only the rows that changed; others keep the image.
	out.Reset()
	m.uploads.write(&out, []byte(strings.Split(frame, "\n")[0])) //nolint:errcheck
	if strings.Contains(out.String(), "\x1b7") {
		t.Fatal("a title update redrew the image")
	}
	// A page error replaces the image.
	p.err = errors.New("corrupt page")
	if strings.Contains(m.View(), paintMarker) {
		t.Fatal("an error page kept its image")
	}
}

func TestSixelSkipsStaleImages(t *testing.T) {
	m := testModel()
	m.renderer = render.NewSixel()
	m.zen = true
	p := m.panes[0]
	p.book = &book.Book{Title: "Synthetic"}
	img := []byte("\x1bP0;1;0q\"1;1;8;12#0;2;0;0;0#0~~\x1b\\")
	// Rendered for a taller terminal, the image now reaches the bottom row.
	lines := make([]string, m.height)
	for i := range lines {
		lines[i] = "  "
	}
	p.res = render.Result{Cols: 2, Rows: m.height, Lines: lines, Transmit: img, Paint: true}
	if strings.Contains(m.View(), paintMarker) {
		t.Fatal("an image on the bottom row was painted")
	}
	if _, ok := m.shownRect(0, 0); ok {
		t.Fatal("the mouse maps onto an image that is not drawn")
	}
	// A resize, a font change included, drops sixel images until they render again.
	p.res.Rows, p.res.Lines = 2, lines[:2]
	p.res2 = p.res
	if !strings.Contains(m.View(), paintMarker) {
		t.Fatal("a fitting image was not painted")
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
	m = updated.(Model)
	if m.panes[0].res.Rows != 0 || m.panes[0].res2.Rows != 0 || strings.Contains(m.View(), paintMarker) {
		t.Fatal("a resize kept the old sixel images")
	}
}

func TestSixelKeepsTheBottomRowFree(t *testing.T) {
	m := testModel()
	m.zen = true
	if _, rows := m.imgBox(0); rows != m.height {
		t.Fatalf("half blocks use %d of %d rows", rows, m.height)
	}
	m.renderer = render.NewSixel()
	if _, rows := m.imgBox(0); rows != m.height-1 {
		t.Fatalf("sixel uses %d of %d rows", rows, m.height)
	}
	m.mode = modeSearch // the search line already keeps the bottom row
	if _, rows := m.imgBox(0); rows != m.height-1 {
		t.Fatalf("sixel while searching uses %d of %d rows", rows, m.height)
	}
}
