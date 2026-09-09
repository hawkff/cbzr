// Package bookmarks persists per-book page bookmarks as JSON in the user
// config dir.
package bookmarks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cbzr/internal/storage"
)

// Mark is one saved position.
type Mark struct {
	Book  string    `json:"book"` // absolute path to the .cbz
	Title string    `json:"title"`
	Name  string    `json:"name,omitempty"` // user-given label
	Page  int       `json:"page"`
	Added time.Time `json:"added"`
}

// Label returns the display name: the custom name when set, else the
// book title.
func (m Mark) Label() string {
	if m.Name != "" {
		return m.Name
	}
	return m.Title
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

// Toggle adds a mark or removes the existing book/page mark.
// It returns true after saving a new mark.
func (s *Store) Toggle(book, title string, page int) (bool, error) {
	added := false
	err := s.update(func() {
		for i, m := range s.Marks {
			if m.Book == book && m.Page == page {
				s.Marks = append(s.Marks[:i], s.Marks[i+1:]...)
				return
			}
		}
		s.Marks = append(s.Marks, Mark{Book: book, Title: title, Page: page, Added: time.Now()})
		sort.SliceStable(s.Marks, func(i, j int) bool {
			if s.Marks[i].Book != s.Marks[j].Book {
				return s.Marks[i].Book < s.Marks[j].Book
			}
			return s.Marks[i].Page < s.Marks[j].Page
		})
		added = true
	})
	return added && err == nil, err
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

// Rename sets the custom label. An empty name restores the title.
func (s *Store) Rename(book string, page int, name string) error {
	return s.update(func() {
		for i := range s.Marks {
			if s.Marks[i].Book == book && s.Marks[i].Page == page {
				s.Marks[i].Name = strings.TrimSpace(name)
				return
			}
		}
	})
}

// Remove deletes the mark for book/page.
func (s *Store) Remove(book string, page int) error {
	return s.update(func() {
		for i, m := range s.Marks {
			if m.Book == book && m.Page == page {
				s.Marks = append(s.Marks[:i], s.Marks[i+1:]...)
				return
			}
		}
	})
}

func (s *Store) update(change func()) error {
	if s.path == "" {
		return fmt.Errorf("bookmark configuration directory unavailable")
	}
	previous := s.Marks
	err := storage.Update(s.path, func(data []byte) ([]byte, error) {
		var latest Store
		if len(data) > 0 {
			if err := json.Unmarshal(data, &latest); err != nil {
				return nil, err
			}
		}
		s.Marks = latest.Marks
		change()
		return json.MarshalIndent(s, "", "  ")
	})
	if err != nil {
		s.Marks = previous
	}
	return err
}
