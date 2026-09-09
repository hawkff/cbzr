// Package progress stores the last reading position for each book.
package progress

import (
	"encoding/json"
	"os"
	"path/filepath"

	"cbzr/internal/storage"
)

// Position is the last visible location in a book.
type Position struct {
	Page    int     `json:"page"`
	Offset  float64 `json:"offset,omitempty"`
	Scroll  float64 `json:"scroll,omitempty"`
	Webtoon bool    `json:"webtoon,omitempty"`
}

// Store holds positions keyed by absolute book path.
type Store struct {
	path      string
	pending   map[string]*Position
	Positions map[string]Position `json:"positions"`
}

// Load reads saved positions. Missing or invalid data produces an empty store.
func Load() *Store {
	s := &Store{Positions: make(map[string]Position)}
	dir, err := os.UserConfigDir()
	if err != nil {
		return s
	}
	s.path = filepath.Join(dir, "cbzr", "progress.json")
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, s); err != nil || s.Positions == nil {
		s.Positions = make(map[string]Position)
	}
	return s
}

// Get returns the saved position for a book.
func (s *Store) Get(book string) (Position, bool) {
	if s == nil {
		return Position{}, false
	}
	pos, ok := s.Positions[pathKey(book)]
	return pos, ok
}

// Set updates a book's in-memory position. Save writes it to disk.
func (s *Store) Set(book string, pos Position) {
	if s == nil {
		return
	}
	if s.Positions == nil {
		s.Positions = make(map[string]Position)
	}
	pos.Page = max(0, pos.Page)
	pos.Offset = min(1, max(0, pos.Offset))
	key := pathKey(book)
	s.Positions[key] = pos
	if s.pending == nil {
		s.pending = make(map[string]*Position)
	}
	s.pending[key] = &pos
}

// Delete removes a book's saved position. Save writes the removal to disk.
func (s *Store) Delete(book string) {
	if s == nil {
		return
	}
	key := pathKey(book)
	delete(s.Positions, key)
	if s.pending == nil {
		s.pending = make(map[string]*Position)
	}
	s.pending[key] = nil
}

// Save applies this session's changes to the latest saved positions.
func (s *Store) Save() error {
	if s == nil || s.path == "" || len(s.pending) == 0 {
		return nil
	}
	var latest Store
	err := storage.Update(s.path, func(data []byte) ([]byte, error) {
		if len(data) > 0 {
			if err := json.Unmarshal(data, &latest); err != nil {
				return nil, err
			}
		}
		if latest.Positions == nil {
			latest.Positions = make(map[string]Position)
		}
		for key, pos := range s.pending {
			if pos == nil {
				delete(latest.Positions, key)
			} else {
				latest.Positions[key] = *pos
			}
		}
		return json.MarshalIndent(&latest, "", "  ")
	})
	if err == nil {
		s.Positions = latest.Positions
		s.pending = nil
	}
	return err
}

func pathKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}
