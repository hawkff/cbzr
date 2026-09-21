package book

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".gif": true, ".webp": true, ".bmp": true,
}

// IsImagePath reports whether the path has a supported image extension.
func IsImagePath(p string) bool {
	return imageExts[strings.ToLower(filepath.Ext(p))]
}

// imageMedia derives the media type the layout expects from a file name.
func imageMedia(name string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if ext == "jpg" {
		ext = "jpeg"
	}
	return "image/" + ext
}

// Book holds image pages from a comic archive, a document reading order or
// an external page renderer.
type Book struct {
	Path  string
	Title string

	arc   archive
	pages []entry
	text  bool // pages were laid out from text; only those pages invert

	chapOnce sync.Once
	chaps    []Chapter
	chapErr  error

	mu    sync.Mutex
	cache map[int]image.Image // small LRU-ish cache
	order []int
}

const (
	cacheSize        = 8
	maxPageBytes     = 64 << 20
	maxPagePixels    = 32_000_000
	maxMetadataBytes = 1 << 20
)

func newBook(path string, text bool) *Book {
	return &Book{Path: path, Title: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), text: text, cache: make(map[int]image.Image)}
}

// readHead returns the first bytes of a file for format sniffing.
func readHead(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 16)
	n, err := f.Read(head)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return head[:n], nil
}

// Open detects the format by signature: PDF, DJVU and DOC go to external
// tools, archives hold comics, EPUB, DOCX or a zipped FB2, and XML is FB2.
func Open(path string) (*Book, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	head, err := readHead(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	switch {
	case bytes.HasPrefix(head, []byte("%PDF")):
		return openRendered(path, false)
	case bytes.HasPrefix(head, []byte("AT&TFORM")):
		return openRendered(path, true)
	case bytes.HasPrefix(head, []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1")):
		return openDOC(path)
	}
	arc, err := openArchive(path)
	if errors.Is(err, errNotArchive) {
		if text := bytes.TrimLeft(bytes.TrimPrefix(head, []byte("\xef\xbb\xbf")), " \t\r\n"); bytes.HasPrefix(text, []byte("<")) {
			return openFB2File(path)
		}
		return nil, fmt.Errorf("%s: unsupported format", filepath.Base(path))
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	container, docx := false, false
	var fb2 entry
	for _, e := range arc.Entries() {
		switch name := e.Name(); {
		case name == "META-INF/container.xml":
			container = true
		case name == "word/document.xml":
			docx = true
		case fb2 == nil && !hiddenEntry(name) && strings.EqualFold(filepath.Ext(name), ".fb2"):
			fb2 = e
		}
	}
	// The contents win over the extension: an EPUB container, then DOCX, then
	// a zipped FB2. A bare .epub extension still demands an EPUB.
	if container || !docx && fb2 == nil && strings.EqualFold(filepath.Ext(path), ".epub") {
		if _, ok := arc.(*zipArchive); !ok {
			arc.Close()
			return nil, fmt.Errorf("EPUB requires a ZIP container")
		}
		b, err := openEPUB(arc, path)
		if err != nil {
			arc.Close()
		}
		return b, err
	}
	if docx {
		b, err := openDOCX(arc, path)
		if err != nil {
			arc.Close()
		}
		return b, err
	}
	if fb2 != nil {
		defer arc.Close()
		data, err := readEntry(fb2, maxDocumentBytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		return openFB2(data, path)
	}
	var pages []entry
	for _, e := range arc.Entries() {
		if !IsImagePath(e.Name()) || hiddenEntry(e.Name()) {
			continue
		}
		pages = append(pages, e)
	}
	if len(pages) == 0 {
		arc.Close()
		return nil, fmt.Errorf("%s: no images found", filepath.Base(path))
	}
	sort.Slice(pages, func(i, j int) bool {
		return NaturalLess(pages[i].Name(), pages[j].Name())
	})
	title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return &Book{
		Path:  path,
		Title: title,
		arc:   arc,
		pages: pages,
		cache: make(map[int]image.Image),
	}, nil
}

// Close releases the underlying archive.
func (b *Book) Close() error {
	if b.arc == nil {
		return nil
	}
	err := b.arc.Close()
	b.arc = nil
	return err
}

// Len returns the number of pages.
func (b *Book) Len() int { return len(b.pages) }

// CanInvertPage allows comic and rendered page inversion but keeps the
// illustrations of text books.
func (b *Book) CanInvertPage(i int) bool {
	return i >= 0 && i < len(b.pages) && (!b.text || b.IsTextPage(i))
}

// PageBytes returns the raw encoded bytes of page i (for HTTP serving).
func (b *Book) PageBytes(i int) ([]byte, string, error) {
	if i < 0 || i >= len(b.pages) {
		return nil, "", fmt.Errorf("page %d out of range", i)
	}
	e := b.pages[i]
	r, err := e.Open()
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	data, err := readBounded(r, maxPageBytes)
	if err != nil {
		return nil, "", err
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode page %d: %w", i+1, err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxPagePixels/config.Height {
		return nil, "", fmt.Errorf("page %d exceeds %d decoded pixels", i+1, maxPagePixels)
	}
	return data, "image/" + format, nil
}

// Page decodes page i, using a small in-memory cache.
func (b *Book) Page(i int) (image.Image, error) {
	if i < 0 || i >= len(b.pages) {
		return nil, fmt.Errorf("page %d out of range", i)
	}
	b.mu.Lock()
	if img, ok := b.cache[i]; ok {
		b.mu.Unlock()
		return img, nil
	}
	b.mu.Unlock()

	data, _, err := b.PageBytes(i)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode page %d: %w", i+1, err)
	}

	b.mu.Lock()
	if _, ok := b.cache[i]; !ok {
		b.cache[i] = img
		b.order = append(b.order, i)
		for len(b.order) > cacheSize {
			evict := b.order[0]
			b.order = b.order[1:]
			delete(b.cache, evict)
		}
	}
	b.mu.Unlock()
	return img, nil
}

func readEntry(e entry, limit int64) ([]byte, error) {
	if e == nil {
		return nil, errors.New("missing archive entry")
	}
	r, err := e.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return readBounded(r, limit)
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("archive entry exceeds %d bytes", limit)
	}
	return data, nil
}
