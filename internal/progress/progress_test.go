package progress

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStoreClampsAndSavesPosition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cbzr", "progress.json")
	book := filepath.Join(t.TempDir(), "comic.cbz")
	store := &Store{path: path}
	store.Set(book, Position{Page: -4, Offset: 2, Scroll: 7, Webtoon: true})

	got, ok := store.Get(book)
	if !ok {
		t.Fatal("saved position not found")
	}
	want := Position{Page: 0, Offset: 1, Scroll: 7, Webtoon: true}
	if got != want {
		t.Fatalf("position = %#v, want %#v", got, want)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Store
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.Positions[pathKey(book)]; got != want {
		t.Fatalf("decoded position = %#v, want %#v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("progress file mode = %o, want owner-only", info.Mode().Perm())
	}

	store.Delete(book)
	if _, ok := store.Get(book); ok {
		t.Fatal("deleted position was still found")
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded = Store{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded.Positions[pathKey(book)]; ok {
		t.Fatal("deleted position remained on disk")
	}
}

func TestStoresPreserveOtherChangesAndDeletions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.json")
	a, b := &Store{path: path}, &Store{path: path}
	a.Set("a.cbz", Position{Page: 1})
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b.Set("b.cbz", Position{Page: 2})
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Get("a.cbz"); !ok {
		t.Fatal("second writer lost first position")
	}
	a.Delete("a.cbz")
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b.Set("b.cbz", Position{Page: 3})
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Get("a.cbz"); ok {
		t.Fatal("stale writer resurrected deletion")
	}
	if got, _ := b.Get("b.cbz"); got.Page != 3 {
		t.Fatalf("position: %#v", got)
	}
}
