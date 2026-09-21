package book

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testArchive []entry

func (a testArchive) Entries() []entry { return a }
func (a testArchive) Close() error     { return nil }

func TestPageRejectsOversizedDimensions(t *testing.T) {
	// BMP dimensions need no large pixel payload for DecodeConfig.
	data := make([]byte, 54)
	copy(data, "BM")
	binary.LittleEndian.PutUint32(data[10:], 54)
	binary.LittleEndian.PutUint32(data[14:], 40)
	binary.LittleEndian.PutUint32(data[18:], 50000)
	binary.LittleEndian.PutUint32(data[22:], 50000)
	binary.LittleEndian.PutUint16(data[26:], 1)
	binary.LittleEndian.PutUint16(data[28:], 24)
	b := &Book{pages: []entry{memEntry{"page.bmp", data}}}
	if _, _, err := b.PageBytes(0); err == nil || !strings.Contains(err.Error(), "decoded pixels") {
		t.Fatalf("oversized encoded page: %v", err)
	}
	if _, err := b.Page(0); err == nil || !strings.Contains(err.Error(), "decoded pixels") {
		t.Fatalf("oversized page: %v", err)
	}
}

func TestReadBoundedAndMetadata(t *testing.T) {
	if _, err := readBounded(strings.NewReader("12345"), 4); err == nil {
		t.Fatal("accepted oversized read")
	}
	if data, err := readBounded(strings.NewReader("1234"), 4); err != nil || string(data) != "1234" {
		t.Fatalf("exact limit: %q, %v", data, err)
	}
	b := &Book{arc: testArchive{memEntry{"ComicInfo.xml", bytes.Repeat([]byte(" "), maxMetadataBytes+1)}}}
	if _, err := b.Chapters(); err == nil {
		t.Fatal("accepted oversized metadata")
	}
}

func TestOpenUsesAbsolutePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "comic.cbz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, err := z.Create("page.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(w, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Dir(path))
	b, err := Open("comic.cbz")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Path != path {
		t.Fatalf("path = %q, want %q", b.Path, path)
	}
	if _, err := b.Page(0); err != nil {
		t.Fatal(err)
	}
	if !b.CanInvertPage(0) || b.CanInvertPage(-1) || b.CanInvertPage(b.Len()) {
		t.Fatal("comic inversion eligibility or page bounds changed")
	}
}

func TestOpenRejectsUnknownFormats(t *testing.T) {
	for name, data := range map[string]string{"notes.txt": "plain text", "page.fb2": "<html><body>markup</body></html>", "empty.pdf": ""} {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		b, err := Open(path)
		if err == nil {
			b.Close()
			t.Fatalf("opened %s", name)
		}
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := Open(filepath.Join(t.TempDir(), "absent.cbz")); err == nil || !strings.Contains(err.Error(), "absent.cbz") {
		t.Fatalf("missing file: %v", err)
	}
}

func TestNaturalLessZeroRuns(t *testing.T) {
	if !NaturalLess("p000", "p1") || NaturalLess("p0", "p000") || !NaturalLess("p2", "p10") {
		t.Fatal("natural digit ordering changed")
	}
}

type generatedEntry struct{ read int64 }

func (e *generatedEntry) Name() string                 { return "page.png" }
func (e *generatedEntry) Open() (io.ReadCloser, error) { return io.NopCloser(e), nil }
func (e *generatedEntry) Read(p []byte) (int, error) {
	clear(p)
	e.read += int64(len(p))
	return len(p), nil
}

func TestPageBytesBoundsActualDecompression(t *testing.T) {
	e := new(generatedEntry)
	b := &Book{pages: []entry{e}}
	if _, _, err := b.PageBytes(0); err == nil {
		t.Fatal("accepted oversized page bytes")
	}
	if e.read != maxPageBytes+1 {
		t.Fatalf("read %d bytes, want limit + 1", e.read)
	}
}
