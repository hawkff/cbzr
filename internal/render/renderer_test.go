package render

import (
	"image"
	"strings"
	"sync"
	"testing"
)

func TestKittyOffsetsImageIDs(t *testing.T) {
	k := &Kitty{cw: 8, ch: 16, base: 0x1200}
	res, err := k.Render(image.NewRGBA(image.Rect(0, 0, 8, 8)), 3, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	// 0x1203 is 4611, and its color bytes are 0, 18 and 3.
	if !strings.Contains(string(res.Transmit), "i=4611,") || !strings.HasPrefix(res.Lines[0], "\x1b[38;2;0;18;3m") || !strings.Contains(string(k.Delete(3)), "i=4611,") {
		t.Fatalf("image id lost its base: %q %q", res.Transmit[:40], res.Lines[0])
	}
}

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
