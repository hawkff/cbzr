package render

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
)

// kittyImage joins the base64 chunks of a transmission and decodes the PNG.
func kittyImage(t *testing.T, transmit []byte) image.Image {
	t.Helper()
	var payload strings.Builder
	for _, seq := range strings.Split(string(transmit), "_G")[1:] {
		command, _, _ := strings.Cut(seq, "\x1b") // base64 holds no ESC
		if _, data, ok := strings.Cut(command, ";"); ok {
			payload.WriteString(data)
		}
	}
	raw, err := base64.StdEncoding.DecodeString(payload.String())
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestKittyChunksScaledAndExactFrames(t *testing.T) {
	// Opaque noise keeps the PNG over several chunks; a flat block keeps the
	// probed pixel independent of interpolation.
	frame := image.NewRGBA(image.Rect(0, 0, 160, 96))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range frame.Pix {
		frame.Pix[i] = uint8(rng.Uint32())
		if i%4 == 3 {
			frame.Pix[i] = 255
		}
	}
	probe := color.RGBA{200, 120, 40, 255}
	draw.Draw(frame, image.Rect(2, 2, 9, 9), image.NewUniform(probe), image.Point{}, draw.Src)
	before := bytes.Clone(frame.Pix)
	for _, tmux := range []bool{false, true} {
		k := &Kitty{cw: 8, ch: 16, tmux: tmux}
		for _, invert := range []bool{false, true} {
			// 160x96 pixels fill 20x6 cells of 8x16, the exact-size case.
			res, err := k.Render(frame, 1, 20, 6, invert)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(res.Transmit), "m=1") < 2 {
				t.Fatal("transmission fit in one chunk")
			}
			want := probe
			if invert {
				want = color.RGBA{255 - probe.R, 255 - probe.G, 255 - probe.B, 255}
			}
			got := kittyImage(t, res.Transmit)
			if got.Bounds() != frame.Bounds() || color.RGBAModel.Convert(got.At(5, 5)) != want {
				t.Fatalf("tmux=%v invert=%v: %v in %v", tmux, invert, got.At(5, 5), got.Bounds())
			}
		}
	}
	if !bytes.Equal(frame.Pix, before) {
		t.Fatal("rendering changed the caller's frame")
	}
}

func TestHalfBlockSkipsRepeatedColors(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	h := &HalfBlock{cw: 8, ch: 16}
	for _, invert := range []bool{false, true} {
		res, err := h.Render(img, 0, 4, 1, invert)
		if err != nil {
			t.Fatal(err)
		}
		c := "255;255;255m"
		if invert {
			c = "0;0;0m"
		}
		want := "\x1b[38;2;" + c + "\x1b[48;2;" + c + strings.Repeat("\u2580", 4) + "\x1b[0m"
		if res.Rows != 1 || res.Lines[0] != want {
			t.Fatalf("invert=%v: %q", invert, res.Lines[0])
		}
	}
}

func TestKittyOffsetsImageIDs(t *testing.T) {
	k := &Kitty{cw: 8, ch: 16, base: 0x1200}
	res, err := k.Render(image.NewRGBA(image.Rect(0, 0, 8, 8)), 3, 2, 2, false)
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
				if _, err := r.Render(image.NewRGBA(image.Rect(0, 0, 8, 8)), 1, 2, 2, i%2 == 0); err != nil {
					t.Error(err)
				}
			}
			wg.Wait()
		})
	}
}

func TestEncodePNGRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	for _, size := range []image.Point{{1, 1}, {3, 5}, {97, 61}} {
		for _, transparent := range []bool{false, true} {
			img := image.NewRGBA(image.Rectangle{Max: size})
			for i := 0; i < len(img.Pix); i += 4 {
				a := uint32(255)
				if transparent {
					a = rng.Uint32N(256) // soft edges of scaled line art
				}
				// Premultiplied color never exceeds its alpha.
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(rng.Uint32N(a+1)), uint8(rng.Uint32N(a+1)), uint8(rng.Uint32N(a+1)), uint8(a)
			}
			if transparent {
				img.SetRGBA(0, 0, color.RGBA{}) // a webtoon margin
			}
			var buf bytes.Buffer
			if err := encodePNG(&buf, img); err != nil {
				t.Fatal(err)
			}
			got, err := png.Decode(&buf)
			if err != nil {
				t.Fatal(err)
			}
			for y := 0; y < size.Y; y++ {
				for x := 0; x < size.X; x++ {
					// Compare straight color, as PNG stores it.
					if c, want := color.NRGBAModel.Convert(got.At(x, y)), color.NRGBAModel.Convert(img.RGBAAt(x, y)); c != want {
						t.Fatalf("%v transparent=%v: pixel %d,%d = %v, want %v", size, transparent, x, y, c, want)
					}
				}
			}
		}
	}
}
