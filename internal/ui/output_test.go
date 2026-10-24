package ui

import (
	"bytes"
	"os"
	"strings"
	"testing"

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
