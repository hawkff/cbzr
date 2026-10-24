package book

import (
	"bytes"
	"errors"
	"image/png"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestRenderedPageBytesShareConvertedCache(t *testing.T) {
	e := &countedPage{memEntry: memEntry{"page.ppm", []byte("P6\n1 1\n255\n\xff\x00\x00")}}
	b := newBook("book", false)
	defer b.Close()
	for range cacheSize + 1 {
		b.pages = append(b.pages, e)
	}
	if _, err := b.Page(0); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			<-start
			data, mime, err := b.PageBytes(0)
			if err != nil || mime != "image/png" {
				t.Errorf("page bytes: %s, %v", mime, err)
				return
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Error(err)
				return
			}
			if red, green, blue, _ := img.At(0, 0).RGBA(); red != 0xffff || green != 0 || blue != 0 {
				t.Error("conversion changed the pixel")
			}
			data[0] = 0
		})
	}
	close(start)
	wg.Wait()
	cached := b.encoded[0]
	if cached.mime != "image/png" || e.opens.Load() != 1 || len(b.encodedOrder) != 1 || b.encodedBytes != len(cached.data) {
		t.Fatalf("conversion cache: MIME=%s opens=%d slots=%d bytes=%d", cached.mime, e.opens.Load(), len(b.encodedOrder), b.encodedBytes)
	}
	again, _, err := b.PageBytes(0)
	if err != nil || !bytes.Equal(again, cached.data) || &b.encoded[0].data[0] != &cached.data[0] {
		t.Fatalf("cached conversion changed or was recomputed: %v", err)
	}
	for i := 1; i < b.Len(); i++ {
		if _, _, err := b.PageBytes(i); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.encoded) != cacheSize || len(b.encodedOrder) != cacheSize || b.encodedBytes != cacheSize*len(cached.data) || b.encoded[0].data != nil {
		t.Fatal("converted pages broke cache accounting or eviction")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.PageBytes(1); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed book served a converted page: %v", err)
	}
}

func BenchmarkRenderedPageBytes(b *testing.B) {
	data := []byte("P6\n1000 1000\n255\n" + strings.Repeat("\xff\x00\x00", 1000*1000))
	book := newBook("book", false)
	book.pages = []entry{memEntry{"page.ppm", data}}
	defer book.Close()
	if _, _, err := book.PageBytes(0); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := book.PageBytes(0); err != nil {
			b.Fatal(err)
		}
	}
}
