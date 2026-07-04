// Package bookmarks persists per-book page bookmarks as JSON in the user
// config dir.
package bookmarks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Mark is one saved position.
type Mark struct {
	Book  string    `json:"book"` // absolute path to the .cbz
	Title string    `json:"title"`
	Page  int       `json:"page"`
	Added time.Time `json:"added"`
}

// Store holds all marks, keyed nowhere: a flat list, newest last.
type Store struct {
	path  string
	Marks []Mark `json:"marks"`
}

// Load reads the store; a missing file yields an empty store.
func Load() *Store {
	s := &Store{}
	dir, err := os.UserConfigDir()
	if err != nil {
		return s
	}
	s.path = filepath.Join(dir, "cbzr", "bookmarks.json")
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	json.Unmarshal(data, s) //nolint:errcheck // corrupt file = empty store
	return s
}

// Toggle adds a mark, or removes it when the same book+page already exists.
// Returns true when a mark was added.
func (s *Store) Toggle(book, title string, page int) bool {
	for i, m := range s.Marks {
		if m.Book == book && m.Page == page {
			s.Marks = append(s.Marks[:i], s.Marks[i+1:]...)
			s.save()
			return false
		}
	}
	s.Marks = append(s.Marks, Mark{Book: book, Title: title, Page: page, Added: time.Now()})
	sort.SliceStable(s.Marks, func(i, j int) bool {
		if s.Marks[i].Book != s.Marks[j].Book {
			return s.Marks[i].Book < s.Marks[j].Book
		}
		return s.Marks[i].Page < s.Marks[j].Page
	})
	s.save()
	return true
}

// Has reports whether book+page is bookmarked.
func (s *Store) Has(book string, page int) bool {
	for _, m := range s.Marks {
		if m.Book == book && m.Page == page {
			return true
		}
	}
	return false
}

func (s *Store) save() {
	if s.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	os.Rename(tmp, s.path) //nolint:errcheck
}
