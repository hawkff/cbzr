package book

import (
	"archive/zip"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	rardecode "github.com/nwaples/rardecode/v2"
)

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
	if err == rardecode.ErrNoSig {
		return nil, fmt.Errorf("%s: not a zip or rar archive", filepath.Base(path))
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

type rarArchive struct{ entries []entry }

type rarEntry struct{ f *rardecode.File }

func (e rarEntry) Name() string                 { return e.f.Name }
func (e rarEntry) Open() (io.ReadCloser, error) { return e.f.Open() }

func openRar(path string) (archive, error) {
	files, err := rardecode.List(path)
	if err != nil {
		return nil, err
	}
	a := &rarArchive{}
	for _, f := range files {
		if f.IsDir {
			continue
		}
		a.entries = append(a.entries, rarEntry{f})
	}
	return a, nil
}

func (a *rarArchive) Entries() []entry { return a.entries }
func (a *rarArchive) Close() error     { return nil } // List readers open per entry

// hiddenEntry reports junk entries: AppleDouble forks, hidden files,
// and __MACOSX resource directories.
func hiddenEntry(name string) bool {
	if strings.Contains(name, "__MACOSX/") {
		return true
	}
	base := filepath.Base(name)
	return strings.HasPrefix(base, ".")
}
