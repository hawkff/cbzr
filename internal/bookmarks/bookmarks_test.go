package bookmarks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStoresPreserveOtherChangesAndDeletions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bookmarks.json")
	a, b := &Store{path: path}, &Store{path: path}
	if added, err := a.Toggle("a", "A", 1); err != nil || !added {
		t.Fatalf("add A: %v, %v", added, err)
	}
	if added, err := b.Toggle("b", "B", 2); err != nil || !added {
		t.Fatalf("add B: %v, %v", added, err)
	}
	if !b.Has("a", 1) {
		t.Fatal("second writer lost first mark")
	}
	if err := a.Remove("a", 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Rename("b", 2, "new"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved Store
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Has("a", 1) || len(saved.Marks) != 1 || saved.Marks[0].Name != "new" {
		t.Fatalf("saved marks: %#v", saved.Marks)
	}
}

func TestWriteFailurePreservesMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Store{path: filepath.Join(path, "bookmarks.json")}
	if added, err := s.Toggle("a", "A", 0); err == nil || added || len(s.Marks) != 0 {
		t.Fatalf("failed write reported success: %v, %v, %#v", added, err, s.Marks)
	}
}
