package book

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

	mu     sync.Mutex
	solid  *rardecode.ReadCloser
	next   int         // archive index of the file the solid reader returns next
	closed atomic.Bool // stops a long decode without waiting for it
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
	files, err := rardecode.List(path)
	if err != nil {
		return nil, err
	}
	a := &rarArchive{path: path}
	for i, f := range files {
		if f.IsDir {
			continue
		}
		a.entries = append(a.entries, rarEntry{a, f, i})
	}
	return a, nil
}

// maxSkipBytes caps each file a solid page decodes on its way, so a small
// archive cannot hide gigabytes of junk before its pages. Tests lower it.
var maxSkipBytes int64 = maxPageBytes

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
		if ctx.Err() == nil {
			a.closeSolid() // only a canceled decode stops at a known place
		}
		return nil, err
	}
	if a.solid == nil || a.next > e.index {
		a.closeSolid()
		rc, err := rardecode.OpenReader(a.path)
		if err != nil {
			return nil, err
		}
		a.solid, a.next = rc, 0
	}
	r := solidReader{a, ctx}
	for a.next <= e.index {
		var err error
		if a.next > 0 {
			// Decode the rest of the current file here, where Close and ctx
			// can stop it; Next would decode it without checking.
			switch _, err = io.CopyN(io.Discard, r, maxSkipBytes+1); err {
			case io.EOF:
				err = nil
			case nil:
				err = fmt.Errorf("a file before %q exceeds %d bytes", e.f.Name, maxSkipBytes)
			}
		}
		var h *rardecode.FileHeader
		if err == nil {
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

// solidReader reads the current solid file until the archive closes or the
// request ends.
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
	return r.a.solid.Read(p)
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
func (a *rarArchive) Close() error {
	a.closed.Store(true)
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closeSolid()
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
