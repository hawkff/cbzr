package book

import (
	"archive/zip"
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

// Book is an open .cbz archive with pages sorted in natural order.
type Book struct {
	Path  string
	Title string

	rc    *zip.ReadCloser
	pages []*zip.File

	chapOnce sync.Once
	chaps    []Chapter

	mu    sync.Mutex
	cache map[int]image.Image // small LRU-ish cache
	order []int
}

const cacheSize = 8

// Open opens a .cbz (zip) archive and indexes its image entries.
func Open(path string) (*Book, error) {
	rc, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	var pages []*zip.File
	for _, f := range rc.File {
		if f.FileInfo().IsDir() || !IsImagePath(f.Name) {
			continue
		}
		base := filepath.Base(f.Name)
		if strings.HasPrefix(base, "._") || strings.HasPrefix(base, ".") {
			continue // AppleDouble and hidden files
		}
		pages = append(pages, f)
	}
	if len(pages) == 0 {
		rc.Close()
		return nil, fmt.Errorf("%s: no images found", filepath.Base(path))
	}
	sort.Slice(pages, func(i, j int) bool {
		return NaturalLess(pages[i].Name, pages[j].Name)
	})
	title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return &Book{
		Path:  path,
		Title: title,
		rc:    rc,
		pages: pages,
		cache: make(map[int]image.Image),
	}, nil
}

// Close releases the underlying archive.
func (b *Book) Close() error {
	if b.rc == nil {
		return nil
	}
	err := b.rc.Close()
	b.rc = nil
	return err
}

// Len returns the number of pages.
func (b *Book) Len() int { return len(b.pages) }

// PageName returns the archive entry name for page i.
func (b *Book) PageName(i int) string {
	if i < 0 || i >= len(b.pages) {
		return ""
	}
	return filepath.Base(b.pages[i].Name)
}

// PageBytes returns the raw encoded bytes of page i (for HTTP serving).
func (b *Book) PageBytes(i int) ([]byte, string, error) {
	if i < 0 || i >= len(b.pages) {
		return nil, "", fmt.Errorf("page %d out of range", i)
	}
	f := b.pages[i]
	r, err := f.Open()
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", err
	}
	mime := mimeFor(f.Name)
	return data, mime, nil
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

	r, err := b.pages[i].Open()
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(r)
	r.Close()
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", filepath.Base(b.pages[i].Name), err)
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

func mimeFor(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	}
	return "application/octet-stream"
}
