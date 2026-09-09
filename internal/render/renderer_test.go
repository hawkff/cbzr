package render

import (
	"image"
	"sync"
	"testing"
)

func TestRenderDuringCellResize(t *testing.T) {
	for _, r := range []Renderer{&Kitty{cw: 8, ch: 16}, &HalfBlock{cw: 8, ch: 16}} {
		t.Run(r.Name(), func(t *testing.T) {
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 40; i++ {
					r.SetCellSize(float64(8+i%2), float64(16+i%2))
				}
			}()
			for i := 0; i < 40; i++ {
				if _, err := r.Render(image.NewRGBA(image.Rect(0, 0, 8, 8)), 1, 2, 2); err != nil {
					t.Error(err)
				}
			}
			wg.Wait()
		})
	}
}
