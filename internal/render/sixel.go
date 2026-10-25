package render

import (
	"bytes"
	"image"
	"slices"
	"strconv"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
)

// Sixel draws pages as DEC sixel graphics, which foot, WezTerm, Konsole,
// xterm and mlterm show. A sixel image is plain pixels on the screen, so
// its Result asks to be painted again whenever the text under it changes.
type Sixel struct {
	mu     sync.Mutex
	cw, ch float64
	colors int // color registers to fill
}

// NewSixel creates the sixel renderer for the probed cell size and colors.
func NewSixel() *Sixel {
	cw, ch := CellSize()
	colors := terminal.colors
	if colors < 2 {
		colors = 256 // what sixel terminals offer when they don't say
	}
	return &Sixel{cw: cw, ch: ch, colors: min(colors, 256)}
}

func (s *Sixel) Name() string { return "sixel" }

func (s *Sixel) SetCellSize(cw, ch float64) { s.mu.Lock(); s.cw, s.ch = cw, ch; s.mu.Unlock() }

func (s *Sixel) Delete(uint32) []byte { return nil }

func (s *Sixel) Render(img image.Image, _ uint32, maxCols, maxRows int, invert bool) (Result, error) {
	s.mu.Lock()
	cw, ch, colors := s.cw, s.ch, s.colors
	s.mu.Unlock()
	b := img.Bounds()
	cols, rows, pw, ph := fitBox(b.Dx(), b.Dy(), maxCols, maxRows, cw, ch)
	// A terminal draws whole six-pixel bands and moves the cursor below the
	// last one. When that band would spill into the next row of cells, trim
	// the image to whole bands.
	if ph > 6 && float64((ph+5)/6*6) > float64(rows)*ch {
		pw, ph = max(1, pw*(ph-ph%6)/ph), ph-ph%6
		cols, rows = cellsFor(pw, cw, maxCols), cellsFor(ph, ch, maxRows)
	}
	dst := getRGBA(image.Rect(0, 0, pw, ph))
	defer Recycle(dst)
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	if invert {
		invertRect(dst, dst.Bounds())
	}
	lines := make([]string, rows)
	blank := strings.Repeat(" ", cols)
	for i := range lines {
		lines[i] = blank
	}
	return Result{Cols: cols, Rows: rows, Lines: lines, Transmit: encodeSixel(dst, colors), Paint: true}, nil
}

// encodeSixel writes img as a sixel image with at most colors colors.
// Pixels below half opacity stay transparent and show the cells under them.
func encodeSixel(img *image.RGBA, colors int) []byte {
	tables := sixelTables.Get().(*sixelHistogram)
	defer sixelTables.Put(tables)
	pal := newSixelPalette(img, colors, tables)
	w, h := img.Rect.Dx(), img.Rect.Dy()
	var out bytes.Buffer
	out.Grow(w * h / 4)
	out.WriteString("\x1bP0;1;0q\"1;1;")
	out.Write(strconv.AppendInt(out.AvailableBuffer(), int64(w), 10))
	out.WriteByte(';')
	out.Write(strconv.AppendInt(out.AvailableBuffer(), int64(h), 10))
	for i, c := range pal.colors {
		// Sixel color components run from 0 to 100.
		out.WriteByte('#')
		out.Write(strconv.AppendInt(out.AvailableBuffer(), int64(i), 10))
		out.WriteString(";2")
		for _, v := range c {
			out.WriteByte(';')
			out.Write(strconv.AppendInt(out.AvailableBuffer(), (int64(v)*100+127)/255, 10))
		}
	}
	// A band is six pixel rows. Each color in it gets one line of sixels,
	// one per column, that covers only the columns where the color occurs.
	n := len(pal.colors)
	bits := make([]byte, n*w)  // the band's pixels of each color, per column
	cols := make([][]int32, n) // the columns each color occurs in, in order
	var used []int             // the band's colors, by first column
	run := sixelRun{out: &out}
	for y0 := 0; y0 < h; y0 += 6 {
		for x := 0; x < w; x++ {
			for r := 0; r < 6 && y0+r < h; r++ {
				i := img.PixOffset(img.Rect.Min.X+x, img.Rect.Min.Y+y0+r)
				c, ok := pal.index(img.Pix[i : i+4 : i+4])
				if !ok {
					continue
				}
				if bits[c*w+x] == 0 {
					if len(cols[c]) == 0 {
						used = append(used, c)
					}
					cols[c] = append(cols[c], int32(x))
				}
				bits[c*w+x] |= 1 << r
			}
		}
		for k, c := range used {
			out.WriteByte('#')
			out.Write(strconv.AppendInt(out.AvailableBuffer(), int64(c), 10))
			next := 0
			for _, x := range cols[c] {
				if gap := int(x) - next; gap > 0 {
					run.put('?', gap)
				}
				run.put(63+bits[c*w+int(x)], 1)
				bits[c*w+int(x)] = 0
				next = int(x) + 1
			}
			run.flush()
			cols[c] = cols[c][:0]
			if k < len(used)-1 {
				out.WriteByte('$') // back to the band's first column for the next color
			}
		}
		used = used[:0]
		if y0+6 < h {
			out.WriteByte('-')
		}
	}
	out.WriteString("\x1b\\")
	return out.Bytes()
}

// sixelRun writes repeated sixels as "!count char".
type sixelRun struct {
	out *bytes.Buffer
	ch  byte
	n   int
}

func (r *sixelRun) put(ch byte, n int) {
	if ch != r.ch {
		r.flush()
		r.ch = ch
	}
	r.n += n
}

func (r *sixelRun) flush() {
	if r.n > 3 {
		r.out.WriteByte('!')
		r.out.Write(strconv.AppendInt(r.out.AvailableBuffer(), int64(r.n), 10))
		r.out.WriteByte(r.ch)
	} else {
		for range r.n {
			r.out.WriteByte(r.ch)
		}
	}
	r.ch, r.n = 0, 0
}

// sixelPalette maps the opaque pixels of one image to at most a given
// number of colors. A gray image keeps its exact gray levels when they fit;
// others go through a median cut of a histogram with six bits per channel.
type sixelPalette struct {
	colors [][3]uint8
	gray   bool
	lut    []uint16 // gray level or histogram key -> palette index
}

// straight returns the color of a premultiplied pixel, and whether it is
// opaque enough to draw.
func straight(px []byte) (r, g, b uint8, ok bool) {
	a := uint32(px[3])
	switch {
	case a < 128:
		return 0, 0, 0, false
	case a == 255:
		return px[0], px[1], px[2], true
	}
	return uint8(uint32(px[0]) * 255 / a), uint8(uint32(px[1]) * 255 / a), uint8(uint32(px[2]) * 255 / a), true
}

func colorKey(r, g, b uint8) int { return int(r>>2)<<12 | int(g>>2)<<6 | int(b>>2) }

func (p *sixelPalette) index(px []byte) (int, bool) {
	r, g, b, ok := straight(px)
	if !ok {
		return 0, false
	}
	if p.gray {
		return int(p.lut[r]), true
	}
	return int(p.lut[colorKey(r, g, b)]), true
}

// sixelHistogram holds the tables of one palette; a megabyte and a half that
// every frame would otherwise allocate.
type sixelHistogram struct {
	counts []int32  // pixels per histogram key
	lut    []uint16 // histogram key -> palette index
}

var sixelTables = sync.Pool{New: func() any {
	return &sixelHistogram{counts: make([]int32, 1<<18), lut: make([]uint16, 1<<18)}
}}

// sixelBucket is one histogram cell: its color and how many pixels fell in.
type sixelBucket struct {
	key   int
	color [3]uint8
	count int
}

func newSixelPalette(img *image.RGBA, colors int, tables *sixelHistogram) *sixelPalette {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	gray := true
	var grays [256]int
	counts := tables.counts
	clear(counts)
	for y := 0; y < h; y++ {
		row := img.Pix[img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y+y):][:4*w]
		for x := 0; x < len(row); x += 4 {
			r, g, b, ok := straight(row[x : x+4 : x+4])
			if !ok {
				continue
			}
			gray = gray && r == g && g == b
			grays[r]++
			counts[colorKey(r, g, b)]++
		}
	}
	p := &sixelPalette{gray: gray}
	var buckets []sixelBucket
	if gray {
		p.lut = make([]uint16, 256)
		for v, n := range grays {
			if n > 0 {
				buckets = append(buckets, sixelBucket{v, [3]uint8{uint8(v), uint8(v), uint8(v)}, n})
			}
		}
	} else {
		p.lut = tables.lut
		used := 0
		for _, n := range counts {
			if n > 0 {
				used++
			}
		}
		buckets = make([]sixelBucket, 0, used)
		for key, n := range counts {
			if n > 0 {
				// The cut sorts a bucket by the middle of the colors it holds.
				c := [3]uint8{uint8(key>>12)<<2 | 2, uint8(key>>6&63)<<2 | 2, uint8(key&63)<<2 | 2}
				buckets = append(buckets, sixelBucket{key, c, int(n)})
			}
		}
	}
	boxes := medianCut(buckets, colors)
	for i, box := range boxes {
		for _, b := range box {
			p.lut[b.key] = uint16(i)
		}
	}
	// Each color is the mean of the pixels it stands for.
	sums := make([][4]int64, len(boxes)) // 32-bit ints overflow on large images
	for y := 0; y < h; y++ {
		row := img.Pix[img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y+y):][:4*w]
		for x := 0; x < len(row); x += 4 {
			if r, g, b, ok := straight(row[x : x+4 : x+4]); ok {
				i, _ := p.index(row[x : x+4 : x+4])
				sums[i][0] += int64(r)
				sums[i][1] += int64(g)
				sums[i][2] += int64(b)
				sums[i][3]++
			}
		}
	}
	p.colors = make([][3]uint8, len(boxes))
	for i, s := range sums {
		p.colors[i] = [3]uint8{uint8(s[0] / s[3]), uint8(s[1] / s[3]), uint8(s[2] / s[3])}
	}
	return p
}

// medianCut splits buckets into at most n groups: it keeps cutting the group
// with the widest channel at the pixel count's median along that channel.
func medianCut(buckets []sixelBucket, n int) [][]sixelBucket {
	if len(buckets) == 0 {
		return nil
	}
	boxes := []cutBox{newCutBox(buckets)}
	for len(boxes) < n {
		widest := 0
		for i, box := range boxes {
			if box.span > boxes[widest].span {
				widest = i
			}
		}
		box := boxes[widest]
		if box.span < 0 {
			break // every group holds one bucket
		}
		slices.SortFunc(box.buckets, func(a, b sixelBucket) int {
			return int(a.color[box.channel]) - int(b.color[box.channel])
		})
		total := 0
		for _, b := range box.buckets {
			total += b.count
		}
		cut, seen := 1, box.buckets[0].count
		for cut < len(box.buckets)-1 && seen*2 < total {
			seen += box.buckets[cut].count
			cut++
		}
		boxes[widest] = newCutBox(box.buckets[:cut])
		boxes = append(boxes, newCutBox(box.buckets[cut:]))
	}
	groups := make([][]sixelBucket, len(boxes))
	for i, box := range boxes {
		groups[i] = box.buckets
	}
	return groups
}

// cutBox is a group of buckets with its widest channel; a span below zero
// means one bucket, which cannot be cut.
type cutBox struct {
	buckets       []sixelBucket
	channel, span int
}

func newCutBox(buckets []sixelBucket) cutBox {
	box := cutBox{buckets: buckets, span: -1}
	if len(buckets) < 2 {
		return box
	}
	for c := range 3 {
		lo, hi := buckets[0].color[c], buckets[0].color[c]
		for _, b := range buckets[1:] {
			lo, hi = min(lo, b.color[c]), max(hi, b.color[c])
		}
		if s := int(hi) - int(lo); s > box.span {
			box.channel, box.span = c, s
		}
	}
	return box
}
