package book

import (
	"bytes"
	"fmt"
	"image"
	"io"
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

// Book holds image pages from a comic archive or an EPUB reading order.
type Book struct {
	Path  string
	Title string

	arc   archive
	pages []entry
	epub  bool

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

// Open indexes comic images in natural order or EPUB content in spine order.
func Open(path string) (*Book, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	arc, err := openArchive(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	isEPUB := strings.EqualFold(filepath.Ext(path), ".epub")
	for _, e := range arc.Entries() {
		if e.Name() == "META-INF/container.xml" {
			isEPUB = true
			break
		}
	}
	if isEPUB {
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

// CanInvertPage excludes EPUB illustrations while allowing comic page inversion.
func (b *Book) CanInvertPage(i int) bool {
	return i >= 0 && i < len(b.pages) && (!b.epub || b.IsTextPage(i))
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
