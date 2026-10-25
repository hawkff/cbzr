package book

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	rardecode "github.com/nwaples/rardecode/v2"
)

// errNotArchive lets Open try the single-file document formats.
var errNotArchive = errors.New("not a zip or rar archive")

// entry is one file inside an archive.
type entry interface {
	Name() string
	Open() (io.ReadCloser, error)
}

// archive abstracts zip (.cbz) and rar (.cbr) containers.
type archive interface {
	Entries() []entry
	Close() error
}

// openArchive sniffs the container format by signature, not extension:
// plenty of .cbz files in the wild are actually rar and vice versa.
func openArchive(path string) (archive, error) {
	if a, err := openZip(path); err == nil {
		return a, nil
	}
	a, err := openRar(path)
	if err == nil {
		return a, nil
	}
	if errors.Is(err, rardecode.ErrNoSig) {
		return nil, errNotArchive
	}
	return nil, err
}

// -- zip ---------------------------------------------------------------------

type zipArchive struct {
	rc      *zip.ReadCloser
	entries []entry
}

type zipEntry struct{ f *zip.File }

func (e zipEntry) Name() string                 { return e.f.Name }
func (e zipEntry) Open() (io.ReadCloser, error) { return e.f.Open() }

func openZip(path string) (archive, error) {
	rc, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	a := &zipArchive{rc: rc}
	for _, f := range rc.File {
		if f.FileInfo().IsDir() {
			continue
		}
		a.entries = append(a.entries, zipEntry{f})
	}
	return a, nil
}

func (a *zipArchive) Entries() []entry { return a.entries }
func (a *zipArchive) Close() error     { return a.rc.Close() }

// -- rar ---------------------------------------------------------------------

// rarArchive opens plain entries on their own. A solid entry depends on every
// file before it, so solid entries share one sequential reader, which starts
// over only for a file at or before its position.
type rarArchive struct {
	path    string
	entries []entry
	budget  int64 // bytes one pass of the solid reader may decode

	mu      sync.Mutex
	solid   *rardecode.ReadCloser
	next    int         // archive index of the file the solid reader returns next
	decoded int64       // bytes the solid reader decoded since it opened
	closed  atomic.Bool // stops a long decode without waiting for it
}

type rarEntry struct {
	a     *rarArchive
	f     *rardecode.File
	index int // position among all archive files, directories included
}

func (e rarEntry) Name() string                 { return e.f.Name }
func (e rarEntry) Open() (io.ReadCloser, error) { return e.openContext(context.Background()) }

// openContext opens the file. A solid file stops decoding when ctx ends.
func (e rarEntry) openContext(ctx context.Context) (io.ReadCloser, error) {
	if !e.f.Solid {
		return e.f.Open()
	}
	return e.a.openSolid(ctx, e)
}

func openRar(path string) (archive, error) {
	volumes := &volumeSizes{sizes: map[string]int64{}}
	files, err := rardecode.List(path, rardecode.FileSystem(volumes))
	if err != nil {
		return nil, err
	}
	// Pages barely compress, so a solid stream that decodes to a hundred
	// times the size of its volumes, beyond 1 GiB, is junk built to burn
	// CPU, and one pass stops there. Volumes count by their size on disk,
	// not by the headers, which the archive could inflate. A large file
	// before a page, such as a bonus video, still lets the reader through.
	a := &rarArchive{path: path, budget: max(1<<30, 100*volumes.total())}
	for i, f := range files {
		if f.IsDir {
			continue
		}
		a.entries = append(a.entries, rarEntry{a, f, i})
	}
	return a, nil
}

// volumeSizes opens archive volumes for rardecode and keeps the size of
// each, so a split archive's budget counts all of them. The listed files
// keep it to open their contents, possibly from several pages at once.
type volumeSizes struct {
	mu    sync.Mutex
	sizes map[string]int64
}

func (v *volumeSizes) Open(name string) (fs.File, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err == nil {
		v.mu.Lock()
		v.sizes[name] = info.Size()
		v.mu.Unlock()
	}
	return f, nil
}

func (v *volumeSizes) total() int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	var n int64
	for _, size := range v.sizes {
		n += size
	}
	return n
}

// openSolid decodes up to the entry and returns its bytes. When ctx ends,
// the reader keeps its place, and the next page continues from there.
func (a *rarArchive) openSolid(ctx context.Context, e rarEntry) (io.ReadCloser, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed.Load() {
		return nil, os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err // ended while an earlier page held the reader
	}
	fail := func(err error) (io.ReadCloser, error) {
		// A canceled read stops between decoder calls, where the next request
		// continues. After any other error, even one that comes while the
		// request ends, the decoder may be broken, so start over.
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			a.closeSolid()
		}
		return nil, err
	}
	if a.solid == nil || a.next > e.index {
		a.closeSolid()
		rc, err := rardecode.OpenReader(a.path)
		if err != nil {
			return nil, err
		}
		a.solid, a.next, a.decoded = rc, 0, 0
	}
	r := solidReader{a, ctx}
	for a.next <= e.index {
		var err error
		if a.next > 0 {
			// Decode the rest of the current file here, where Close, ctx and
			// the budget can stop it; Next would decode it without checking.
			_, err = io.Copy(io.Discard, r)
		}
		var h *rardecode.FileHeader
		if err == nil {
			// shortcut: Next decodes whatever packed data the file holds past
			// its declared size, outside the budget and any check, so a
			// malformed archive spends CPU bounded only by its own size.
			// Covering that needs a hook in rardecode.
			h, err = a.solid.Next()
		}
		if err == nil && a.next == e.index && h.Name != e.f.Name {
			err = fmt.Errorf("RAR entry %d is %q, want %q", e.index, h.Name, e.f.Name)
		}
		if err != nil {
			return fail(err)
		}
		a.next++
	}
	data, err := readBounded(r, maxPageBytes)
	if err != nil {
		return fail(err)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// solidReader reads the current solid file until the archive closes, the
// request ends or the pass decodes past its budget. It requires a.mu.
type solidReader struct {
	a   *rarArchive
	ctx context.Context
}

func (r solidReader) Read(p []byte) (int, error) {
	if r.a.closed.Load() {
		return 0, os.ErrClosed
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.a.solid.Read(p)
	if r.a.decoded += int64(n); r.a.decoded > r.a.budget {
		return n, fmt.Errorf("RAR archive expands past %d MiB", r.a.budget>>20)
	}
	return n, err
}

// closeSolid requires a.mu.
func (a *rarArchive) closeSolid() error {
	if a.solid == nil {
		return nil
	}
	err := a.solid.Close()
	a.solid = nil
	return err
}

func (a *rarArchive) Entries() []entry { return a.entries }

// Close stops decoding without waiting for it. Reads stop at their next
// check, but Next can decode for long without one, so the reader closes in
// the background once the decode lets go of it.
func (a *rarArchive) Close() error {
	a.closed.Store(true)
	go func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.closeSolid() //nolint:errcheck
	}()
	return nil
}

// hiddenEntry reports junk entries: AppleDouble forks, hidden files,
// and __MACOSX resource directories.
func hiddenEntry(name string) bool {
	if strings.Contains(name, "__MACOSX/") {
		return true
	}
	base := filepath.Base(name)
	return strings.HasPrefix(base, ".")
}
