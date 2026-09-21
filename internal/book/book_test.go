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
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestOpenEmbeddedPDFMarker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	fb2 := filepath.Join(dir, "signature.fb2")
	if err := os.WriteFile(fb2, []byte(`<FictionBook><body><section><p>The PDF signature is %PDF-1.7.</p></section></body></FictionBook>`), 0o644); err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	z := zip.NewWriter(&data)
	for _, e := range []memEntry{{"README.txt", []byte("The PDF signature is %PDF-1.7.")}, {"page.png", epubImage(t)}} {
		w, err := z.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	cbz := filepath.Join(dir, "signature.cbz")
	if err := os.WriteFile(cbz, data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fb2, cbz} {
		b, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		if b.Len() != 1 || b.IsTextPage(0) != (path == fb2) {
			t.Fatalf("%s: pages = %d, text = %t", path, b.Len(), b.IsTextPage(0))
		}
	}
}

type countedPage struct {
	memEntry
	opens            atomic.Int32
	started, release chan struct{}
}

func (e *countedPage) Open() (io.ReadCloser, error) {
	if e.opens.Add(1) == 1 && e.started != nil {
		close(e.started)
		<-e.release
	}
	return e.memEntry.Open()
}

func TestPageCacheSharesLoads(t *testing.T) {
	e := &countedPage{memEntry: memEntry{"page.png", epubImage(t)}}
	b := newBook("book", false)
	for range cacheSize + 1 {
		b.pages = append(b.pages, e)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			<-start
			if i%2 == 0 {
				if _, err := b.Page(0); err != nil {
					t.Error(err)
				}
			} else if _, _, err := b.PageBytes(0); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	if got := e.opens.Load(); got != 1 {
		t.Fatalf("opened the same page %d times", got)
	}
	data, _, err := b.PageBytes(0)
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 0
	again, _, err := b.PageBytes(0)
	if err != nil || !bytes.Equal(again, e.data) {
		t.Fatalf("caller changed the cached bytes: %v", err)
	}
	for i := 1; i < b.Len(); i++ {
		if _, err := b.Page(i); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.cache) != cacheSize || len(b.encoded) != cacheSize || b.encodedBytes != cacheSize*len(e.data) {
		t.Fatalf("cache sizes: decoded=%d, encoded=%d, bytes=%d", len(b.cache), len(b.encoded), b.encodedBytes)
	}
	if _, err := b.Page(0); err != nil {
		t.Fatal(err)
	}
	if got := e.opens.Load(); got != cacheSize+2 {
		t.Fatalf("evicted page was not reloaded: %d opens", got)
	}
}

func TestPageLoadsDoNotBlockOtherPages(t *testing.T) {
	slow := &countedPage{
		memEntry: memEntry{"slow.png", epubImage(t)},
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	b := newBook("book", false)
	b.pages = []entry{slow, memEntry{"fast.png", epubImage(t)}}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		close(slow.release)
		wg.Wait()
		if len(b.loading) != 0 {
			t.Fatal("completed loads retained their coordination state")
		}
	})
	wg.Go(func() {
		if _, _, err := b.PageBytes(0); err != nil {
			t.Error(err)
		}
	})
	<-slow.started
	finished := make(chan error, 1)
	wg.Go(func() {
		_, err := b.Page(1)
		finished <- err
	})
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an unrelated page waited for the slow load")
	}
}

func TestPageCacheByteLimit(t *testing.T) {
	data := make([]byte, maxPageBytes/2+1)
	copy(data, epubImage(t))
	b := newBook("book", false)
	b.pages = []entry{memEntry{"one.png", data}, memEntry{"two.png", data}}
	for i := range b.pages {
		unlock := b.lockPage(i)
		_, _, err := b.pageBytes(i)
		unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(b.encoded) != 1 || b.encodedBytes != len(data) || b.encoded[1].data == nil {
		t.Fatalf("encoded cache exceeded its byte budget: %d entries, %d bytes", len(b.encoded), b.encodedBytes)
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
