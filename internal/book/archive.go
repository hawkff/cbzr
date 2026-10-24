package book

import (
	"archive/zip"
	"bytes"
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

func (e rarEntry) Name() string { return e.f.Name }
func (e rarEntry) Open() (io.ReadCloser, error) {
	if !e.f.Solid {
		return e.f.Open()
	}
	return e.a.openSolid(e)
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

// openSolid decodes up to the entry and returns its bytes.
func (a *rarArchive) openSolid(e rarEntry) (io.ReadCloser, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed.Load() {
		return nil, os.ErrClosed
	}
	if a.solid == nil || a.next > e.index {
		a.closeSolid()
		rc, err := rardecode.OpenReader(a.path)
		if err != nil {
			return nil, err
		}
		a.solid, a.next = rc, 0
	}
	for a.next <= e.index {
		var err error
		if a.next > 0 {
			// Decode the rest of the current file here, where Close can stop
			// it; Next would decode it without checking.
			_, err = io.Copy(io.Discard, solidReader{a})
		}
		var h *rardecode.FileHeader
		if err == nil {
			h, err = a.solid.Next()
		}
		if err == nil && a.next == e.index && h.Name != e.f.Name {
			err = fmt.Errorf("RAR entry %d is %q, want %q", e.index, h.Name, e.f.Name)
		}
		if err != nil {
			a.closeSolid()
			return nil, err
		}
		a.next++
	}
	data, err := readBounded(solidReader{a}, maxPageBytes)
	if err != nil {
		a.closeSolid()
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// solidReader reads the current solid file until the archive closes.
type solidReader struct{ a *rarArchive }

func (r solidReader) Read(p []byte) (int, error) {
	if r.a.closed.Load() {
		return 0, os.ErrClosed
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
