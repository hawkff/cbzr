package render

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"sync"
)

// pngState keeps a compressor and row buffers between frames.
type pngState struct {
	z        *zlib.Writer
	prev, up []byte
}

var pngStates = sync.Pool{New: func() any {
	z, _ := zlib.NewWriterLevel(nil, zlib.BestSpeed)
	return &pngState{z: z}
}}

// encodePNG appends img to w as an 8-bit RGB PNG, or RGBA when a pixel is
// not opaque. Every row uses the Up filter: image/png tries all five filters
// per row, which doubles the encoding time of a frame for a few percent of
// size.
func encodePNG(w *bytes.Buffer, img *image.RGBA) error {
	width, height := img.Rect.Dx(), img.Rect.Dy()
	bpp, colorType := 3, byte(2)
	if !opaque(img) {
		bpp, colorType = 4, 6
	}
	w.WriteString("\x89PNG\r\n\x1a\n")
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(height))
	ihdr[8], ihdr[9] = 8, colorType
	writeChunk(w, "IHDR", ihdr[:])

	// IDAT: compress straight into w, then fill in the length and checksum.
	start := w.Len()
	w.WriteString("\x00\x00\x00\x00IDAT")
	s := pngStates.Get().(*pngState)
	defer pngStates.Put(s)
	s.z.Reset(w)
	n := 1 + bpp*width
	s.prev, s.up = grow(s.prev, n), grow(s.up, n)
	clear(s.prev)
	for y := 0; y < height; y++ {
		row := img.Pix[img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y+y):][:4*width]
		s.up[0] = 2 // Up
		cur := s.up[1:]
		for x, i := 0, 0; x < width; x, i = x+1, i+bpp {
			// cur briefly holds the pixel itself; the filter subtracts the row above.
			copy(cur[i:i+bpp], row[4*x:])
			if a := uint32(row[4*x+3]); bpp == 4 && a != 0 && a != 0xff {
				// PNG wants straight color; image.RGBA premultiplies it by alpha.
				for c := i; c < i+3; c++ {
					cur[c] = uint8(uint32(cur[c]) * 0xffff / a >> 8)
				}
			}
			for c := i; c < i+bpp; c++ {
				cur[c], s.prev[c] = cur[c]-s.prev[c], cur[c]
			}
		}
		if _, err := s.z.Write(s.up); err != nil {
			return err
		}
	}
	if err := s.z.Close(); err != nil {
		return err
	}
	data := w.Bytes()[start+8:]
	binary.BigEndian.PutUint32(w.Bytes()[start:], uint32(len(data)))
	crc := crc32.Update(crc32.ChecksumIEEE([]byte("IDAT")), crc32.IEEETable, data)
	w.Write(binary.BigEndian.AppendUint32(w.AvailableBuffer(), crc))
	writeChunk(w, "IEND", nil)
	return nil
}

func writeChunk(w *bytes.Buffer, kind string, data []byte) {
	w.Write(binary.BigEndian.AppendUint32(w.AvailableBuffer(), uint32(len(data))))
	w.WriteString(kind)
	w.Write(data)
	crc := crc32.Update(crc32.ChecksumIEEE([]byte(kind)), crc32.IEEETable, data)
	w.Write(binary.BigEndian.AppendUint32(w.AvailableBuffer(), crc))
}

func opaque(img *image.RGBA) bool {
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		row := img.Pix[img.PixOffset(img.Rect.Min.X, y):][:4*img.Rect.Dx()]
		for i := 3; i < len(row); i += 4 {
			if row[i] != 0xff {
				return false
			}
		}
	}
	return true
}

func grow(b []byte, n int) []byte {
	if cap(b) < n {
		return make([]byte, n)
	}
	return b[:n]
}
