package storage

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestConcurrentUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "count.json")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Update(path, func(data []byte) ([]byte, error) {
				count := 0
				if len(data) > 0 {
					var err error
					count, err = strconv.Atoi(string(data))
					if err != nil {
						return nil, err
					}
				}
				return []byte(strconv.Itoa(count + 1)), nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "8" {
		t.Fatalf("count = %q, want 8", data)
	}
}
