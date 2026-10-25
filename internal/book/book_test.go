package book

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
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
	// A download cut short keeps the ZIP signature but loses the directory.
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	if w, err := z.Create("page.png"); err != nil {
		t.Fatal(err)
	} else if _, err := w.Write(epubImage(t)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "partial.cbz")
	if err := os.WriteFile(path, archive.Bytes()[:archive.Len()/2], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, zip.ErrFormat) || !strings.Contains(err.Error(), "partial.cbz") {
		t.Fatalf("truncated ZIP: %v", err)
	}
}

func TestOpenSolidRAR(t *testing.T) {
	b, err := Open("testdata/solid.cbr")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Len() != 3 || !b.pages[1].(rarEntry).f.Solid {
		t.Fatalf("%d pages; the fixture needs solid files after the first", b.Len())
	}
	// Each page is a 4-pixel-wide strip, 2+i rows high, in red, green and blue.
	want := []color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}}
	for _, i := range []int{2, 0, 1, 2, 1} { // backwards restarts the shared reader
		data, err := readEntry(b.pages[i], maxPageBytes)
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil || img.Bounds() != image.Rect(0, 0, 4, 2+i) || color.RGBAModel.Convert(img.At(1, 1)) != want[i] {
			t.Fatalf("page %d decoded wrong: %v", i, err)
		}
	}
	if _, err := b.Page(2); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readEntry(b.pages[2], maxPageBytes); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("read after close: %v", err)
	}
}

// endsAfter is a context that reports cancellation once checks run out.
type endsAfter struct {
	context.Context
	checks int
}

func (c *endsAfter) Err() error {
	if c.checks--; c.checks < 0 {
		return context.Canceled
	}
	return nil
}

func TestSolidRARStopsAndBoundsDecoding(t *testing.T) {
	b, err := Open("testdata/solid.cbr")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	last := b.pages[2].(rarEntry)
	want, err := readEntry(last, maxPageBytes)
	if err != nil {
		t.Fatal(err)
	}
	// A request that ends while the first file decodes leaves the reader
	// there, and the next request continues to the same bytes.
	if _, err := last.openContext(&endsAfter{Context: context.Background(), checks: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ended request: %v", err)
	}
	if last.a.solid == nil || last.a.next != 1 {
		t.Fatalf("ended request moved the reader to file %d", last.a.next)
	}
	if got, err := readEntry(last, maxPageBytes); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("continued request: %v", err)
	}
	// One pass may decode every file before a page, however large each one
	// is, until the archive's budget runs out.
	var size int64
	for _, p := range b.pages {
		data, err := readEntry(p, maxPageBytes)
		if err != nil {
			t.Fatal(err)
		}
		size += int64(len(data))
	}
	last.a.budget = size
	if _, err := readEntry(last, maxPageBytes); err != nil {
		t.Fatalf("page within the budget: %v", err)
	}
	last.a.budget = size - 1
	if _, err := readEntry(last, maxPageBytes); err == nil || !strings.Contains(err.Error(), "expands past") {
		t.Fatalf("page past the budget: %v", err)
	}
}

func TestPageCacheBoundsDecodedPixels(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewGray(image.Rect(0, 0, 5000, 5000))); err != nil {
		t.Fatal(err)
	}
	b := newBook("book", false)
	for i := range 3 {
		b.pages = append(b.pages, memEntry{fmt.Sprintf("%d.png", i), data.Bytes()})
		if _, err := b.Page(i); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.cache) != 2 || b.cache[0] != nil || b.cachePixels != 2*5000*5000 {
		t.Fatalf("decoded cache kept %d pages, %d pixels", len(b.cache), b.cachePixels)
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

func TestCanceledPageWaitLeavesSharedLoadRunning(t *testing.T) {
	e := &countedPage{memEntry: memEntry{"page.png", epubImage(t)}, started: make(chan struct{}), release: make(chan struct{})}
	b := newBook("book", false)
	b.pages = []entry{e}
	loaded := make(chan error, 1)
	go func() { _, err := b.Page(0); loaded <- err }()
	t.Cleanup(func() {
		close(e.release)
		if err := <-loaded; err != nil {
			t.Errorf("shared load failed: %v", err)
		}
		b.Close()
	})
	<-e.started
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := make(chan error, 1)
	go func() { _, err := b.PageContext(ctx, 0); waiting <- err }()
	cancel()
	select {
	case err := <-waiting:
		if err != context.Canceled {
			t.Fatalf("canceled wait: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("obsolete request waited for another page load")
	}
	if e.opens.Load() != 1 {
		t.Fatal("canceled waiter started a duplicate load")
	}
}

type cancelingPage struct {
	data   []byte
	cancel context.CancelFunc
	reads  int
	closed bool
}

func (p *cancelingPage) Name() string                 { return "page.png" }
func (p *cancelingPage) Open() (io.ReadCloser, error) { return p, nil }
func (p *cancelingPage) Close() error                 { p.closed = true; return nil }
func (p *cancelingPage) Read(dst []byte) (int, error) {
	p.reads++
	if p.reads > 1 {
		return 0, io.EOF
	}
	p.cancel()
	return copy(dst, p.data), nil
}

func TestPageContextStopsArchiveReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	page := &cancelingPage{data: epubImage(t), cancel: cancel}
	b := &Book{pages: []entry{page}}
	if _, err := b.PageContext(ctx, 0); err != context.Canceled {
		t.Fatalf("canceled page: %v", err)
	}
	if page.reads != 1 || !page.closed || len(b.loading) != 0 || len(b.cache) != 0 {
		t.Fatal("canceled archive read continued or retained its load slot")
	}
	if _, err := (epubTextPage{}).render(ctx); err != context.Canceled {
		t.Fatalf("canceled text page: %v", err)
	}
}

func TestPageCacheByteLimit(t *testing.T) {
	data := make([]byte, maxPageBytes/2+1)
	copy(data, epubImage(t))
	b := newBook("book", false)
	b.pages = []entry{memEntry{"one.png", data}, memEntry{"two.png", data}}
	for i := range b.pages {
		unlock, err := b.lockPage(context.Background(), i)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = b.pageBytes(context.Background(), i)
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
