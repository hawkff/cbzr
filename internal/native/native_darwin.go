//go:build darwin && cgo

package native

/*
#cgo CFLAGS: -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#include <stdint.h>
#include <stdlib.h>

int cbzr_native_run(uintptr_t handle, const char *title);
void cbzr_native_link_done(void *view, uint64_t generation, int page, const char *url);
*/
import "C"

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"runtime"
	"runtime/cgo"
	"sync"
	"unsafe"

	"cbzr/internal/book"
	"cbzr/internal/render"
	xdraw "golang.org/x/image/draw"
)

const (
	eventClear = iota + 1
	eventSave
	eventReturn
	eventToggleWebtoon
	eventRotate
	eventFirst
	eventLast
	eventToggleSpread
	eventZoomIn
	eventZoomOut
	eventResetView
	eventPanUp
	eventPanDown
	eventPanLeft
	eventPanRight
	eventToggleInversion
)

type scaledPageKey struct {
	page, width, rotation int
	inverted              bool
}

// shownPage maps a painted page, including its crop and rotation, to the canvas.
type shownPage struct {
	page, rotation int
	bounds, crop   image.Rectangle
	size           image.Point
}

type reader struct {
	mu sync.Mutex

	book          *book.Book
	state         State
	action        Action
	pendingScroll float64
	blockedScroll float64
	scaledPages   map[scaledPageKey]image.Image
	scaledOrder   []scaledPageKey
	shown         []shownPage
	frameSize     image.Point
	err           error
}

// Available reports whether this build includes the native reader.
func Available() bool { return true }

// Run starts an AppKit reader window and blocks until it closes.
func Run(state State) (Result, error) {
	b, err := book.Open(state.Path)
	if err != nil {
		return Result{}, err
	}
	defer b.Close() //nolint:errcheck

	state.Page = min(b.Len()-1, max(0, state.Page))
	state.Offset = min(1, max(0, state.Offset))
	if state.Zoom < 1 {
		state.Zoom = 1
	}
	if state.CenterX == 0 {
		state.CenterX = 0.5
	}
	if state.CenterY == 0 {
		state.CenterY = 0.5
	}
	r := &reader{
		book: b, state: state, action: ActionClear,
		scaledPages: make(map[scaledPageKey]image.Image),
	}
	handle := cgo.NewHandle(r)
	defer handle.Delete()

	title := C.CString("cbzr — " + b.Title)
	defer C.free(unsafe.Pointer(title))
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if C.cbzr_native_run(C.uintptr_t(handle), title) != 0 {
		return Result{}, fmt.Errorf("could not start the macOS window")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	result := Result{State: r.state, Action: r.action}
	return result, r.err
}

func (r *reader) frame(width, height int) *image.RGBA {
	width, height = max(1, width), max(1, height)
	r.shown = r.shown[:0]
	r.frameSize = image.Pt(width, height)
	r.blockedScroll = 0
	var img *image.RGBA
	var err error
	if r.state.Webtoon {
		img, err = r.webtoonFrame(width, height)
	} else {
		img, err = r.pageFrame(width, height)
	}
	if err != nil {
		r.shown = r.shown[:0]
		r.err = err
		return nil
	}
	r.err = nil
	return img
}

func (r *reader) webtoonFrame(width, height int) (*image.RGBA, error) {
	sizes := make(map[int]image.Point)
	load := func(page int) (image.Image, error) {
		img, err := r.scaledPage(page, width)
		if err == nil {
			sizes[page] = img.Bounds().Size()
		}
		return img, err
	}
	scrollPixels := r.pendingScroll
	r.pendingScroll = 0
	if r.state.Scroll != 0 {
		_, cellHeight := render.CellSize()
		scrollPixels += r.state.Scroll * max(1, cellHeight)
		r.state.Scroll = 0
	}
	img, page, offset, err := render.ComposeWebtoon(
		load, r.book.Len(), r.state.Page, r.state.Offset, scrollPixels,
		width, height, 0, 1, 1,
	)
	if err == nil {
		if page == r.state.Page && math.Abs(offset-r.state.Offset) < 1e-9 {
			r.blockedScroll = scrollPixels
		}
		r.state.Page, r.state.Offset = page, offset
		y := -min(sizes[page].Y, max(0, int(math.Round(offset*float64(sizes[page].Y)))))
		for pg := page; y < height && pg < r.book.Len(); pg++ {
			size, ok := sizes[pg]
			if !ok {
				break
			}
			x := (width - size.X) / 2
			r.shown = append(r.shown, shownPage{
				page: pg, rotation: r.state.Rotation, size: size,
				bounds: image.Rect(x, y, x+size.X, y+size.Y), crop: image.Rectangle{Max: size},
			})
			y += size.Y
		}
	}
	return img, err
}

func (r *reader) pageFrame(width, height int) (*image.RGBA, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), image.Black, image.Point{}, draw.Src)
	slots := []image.Rectangle{canvas.Bounds()}
	if r.state.Spread && r.state.Page+1 < r.book.Len() {
		gap := min(8, max(2, width/300))
		half := (width - gap) / 2
		slots = []image.Rectangle{image.Rect(0, 0, half, height), image.Rect(half+gap, 0, width, height)}
	}
	for slot, box := range slots {
		page := r.state.Page + slot
		img, err := r.book.Page(page)
		if err != nil {
			return nil, err
		}
		img = render.Transform(img, r.state.Rotation, 1, 0.5, 0.5)
		full := img.Bounds()
		img = render.Transform(img, 0, r.state.Zoom, r.state.CenterX, r.state.CenterY)
		crop := img.Bounds().Sub(full.Min)
		if r.state.Inverted && r.book.CanInvertPage(page) {
			img = render.Invert(img)
		}
		bounds := drawFit(canvas, box, img)
		r.shown = append(r.shown, shownPage{page: page, rotation: r.state.Rotation, bounds: bounds, crop: crop, size: full.Size()})
	}
	return canvas, nil
}

func (r *reader) scaledPage(page, width int) (image.Image, error) {
	key := scaledPageKey{page: page, width: width, rotation: r.state.Rotation, inverted: r.state.Inverted && r.book.CanInvertPage(page)}
	if img, ok := r.scaledPages[key]; ok {
		return img, nil
	}
	img, err := r.book.Page(page)
	if err != nil {
		return nil, err
	}
	img = render.Transform(img, r.state.Rotation, 1, 0.5, 0.5)
	img = scaleToWidth(img, width)
	if key.inverted {
		img = render.Invert(img)
	}
	r.scaledPages[key] = img
	r.scaledOrder = append(r.scaledOrder, key)
	for len(r.scaledOrder) > 4 {
		oldest := r.scaledOrder[0]
		r.scaledOrder = r.scaledOrder[1:]
		delete(r.scaledPages, oldest)
	}
	return img, nil
}

func (r *reader) clearScaledPages() {
	clear(r.scaledPages)
	r.scaledOrder = r.scaledOrder[:0]
}

func scaleToWidth(src image.Image, width int) image.Image {
	bounds := src.Bounds()
	if bounds.Dx() < 1 || bounds.Dy() < 1 || bounds.Dx() <= width {
		return src
	}
	height := max(1, int(float64(bounds.Dy())*float64(width)/float64(bounds.Dx())))
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, bounds, xdraw.Src, nil)
	return dst
}

func drawFit(dst *image.RGBA, box image.Rectangle, src image.Image) image.Rectangle {
	b := src.Bounds()
	if b.Dx() < 1 || b.Dy() < 1 || box.Dx() < 1 || box.Dy() < 1 {
		return image.Rectangle{}
	}
	scale := min(float64(box.Dx())/float64(b.Dx()), float64(box.Dy())/float64(b.Dy()))
	w := max(1, int(float64(b.Dx())*scale))
	h := max(1, int(float64(b.Dy())*scale))
	x, y := box.Min.X+(box.Dx()-w)/2, box.Min.Y+(box.Dy()-h)/2
	bounds := image.Rect(x, y, x+w, y+h)
	xdraw.ApproxBiLinear.Scale(dst, bounds, src, b, xdraw.Src, nil)
	return bounds
}

// pagePoint uses the last painted layout, not a page turn awaiting a repaint.
// View fractions keep Retina scaling out of the event coordinates.
func (r *reader) pagePoint(fx, fy float64) (int, float64, float64, bool) {
	if !(fx >= 0 && fx < 1 && fy >= 0 && fy < 1) {
		return 0, 0, 0, false
	}
	x, y := fx*float64(r.frameSize.X), fy*float64(r.frameSize.Y)
	for _, p := range r.shown {
		if p.bounds.Empty() || p.size.X < 1 || p.size.Y < 1 || x < float64(p.bounds.Min.X) || x >= float64(p.bounds.Max.X) || y < float64(p.bounds.Min.Y) || y >= float64(p.bounds.Max.Y) {
			continue
		}
		px := (float64(p.crop.Min.X) + (x-float64(p.bounds.Min.X))*float64(p.crop.Dx())/float64(p.bounds.Dx())) / float64(p.size.X)
		py := (float64(p.crop.Min.Y) + (y-float64(p.bounds.Min.Y))*float64(p.crop.Dy())/float64(p.bounds.Dy())) / float64(p.size.Y)
		switch p.rotation & 3 {
		case 1:
			px, py = py, 1-px
		case 2:
			px, py = 1-px, 1-py
		case 3:
			px, py = 1-py, px
		}
		return p.page, px, py, true
	}
	return 0, 0, 0, false
}

func (r *reader) click(fx, fy float64, done func(book.Link, bool)) {
	r.mu.Lock()
	page, x, y, ok := r.pagePoint(fx, fy)
	b := r.book
	r.mu.Unlock()
	if !ok || !b.Selectable(page) {
		done(book.Link{}, false)
		return
	}
	// The book and point outlive the window handle; no AppKit or reader lock
	// is held while a PDF converter runs.
	go func() {
		b.PrepareText(page)
		link, ok := b.LinkAt(page, x, y)
		if link.URL != "" {
			link.URL = book.CleanURL(link.URL)
		}
		done(link, ok)
	}()
}

func (r *reader) goTo(page int) {
	r.state.Page = min(r.book.Len()-1, max(0, page))
	r.state.Offset, r.state.Scroll, r.pendingScroll = 0, 0, 0
	r.state.Zoom, r.state.CenterX, r.state.CenterY = 1, 0.5, 0.5
}

func readerFor(handle C.uintptr_t) *reader {
	return cgo.Handle(handle).Value().(*reader)
}

//export cbzr_go_native_render
func cbzr_go_native_render(handle C.uintptr_t, width, height C.int, length *C.size_t, blockedScroll *C.double) unsafe.Pointer {
	r := readerFor(handle)
	r.mu.Lock()
	defer r.mu.Unlock()
	frame := r.frame(int(width), int(height))
	*blockedScroll = C.double(r.blockedScroll)
	if frame == nil {
		*length = 0
		return nil
	}
	*length = C.size_t(len(frame.Pix))
	return C.CBytes(frame.Pix)
}

//export cbzr_go_native_click
func cbzr_go_native_click(handle C.uintptr_t, x, y C.double, view unsafe.Pointer, generation C.uint64_t) {
	readerFor(handle).click(float64(x), float64(y), func(link book.Link, ok bool) {
		page := C.int(-1)
		var url *C.char
		if ok {
			page = C.int(link.Page)
			if link.URL != "" {
				url = C.CString(link.URL)
				defer C.free(unsafe.Pointer(url))
			}
		}
		C.cbzr_native_link_done(view, generation, page, url)
	})
}

//export cbzr_go_native_go_to
func cbzr_go_native_go_to(handle C.uintptr_t, page C.int) {
	r := readerFor(handle)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.goTo(int(page))
}

//export cbzr_go_native_is_webtoon
func cbzr_go_native_is_webtoon(handle C.uintptr_t) C.int {
	r := readerFor(handle)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state.Webtoon {
		return 1
	}
	return 0
}

//export cbzr_go_native_scroll
func cbzr_go_native_scroll(handle C.uintptr_t, pixels C.double) {
	r := readerFor(handle)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.state.Webtoon {
		return
	}
	r.pendingScroll += float64(pixels)
}

//export cbzr_go_native_turn
func cbzr_go_native_turn(handle C.uintptr_t, delta C.int) {
	r := readerFor(handle)
	r.mu.Lock()
	defer r.mu.Unlock()
	step := 1
	if r.state.Spread {
		step = 2
	}
	r.state.Page = min(r.book.Len()-1, max(0, r.state.Page+int(delta)*step))
	r.state.Offset = 0
	r.pendingScroll, r.state.Scroll = 0, 0
}

//export cbzr_go_native_event
func cbzr_go_native_event(handle C.uintptr_t, event C.int) {
	r := readerFor(handle)
	r.mu.Lock()
	defer r.mu.Unlock()
	switch int(event) {
	case eventClear:
		r.action = ActionClear
	case eventSave:
		r.action = ActionSave
	case eventReturn:
		r.action = ActionReturn
	case eventToggleInversion:
		r.state.Inverted = !r.state.Inverted
	case eventToggleWebtoon:
		r.pendingScroll, r.state.Scroll = 0, 0
		r.state.Webtoon = !r.state.Webtoon
		r.state.Spread = false
		r.state.Offset = 0
		r.state.Zoom, r.state.CenterX, r.state.CenterY = 1, 0.5, 0.5
		r.clearScaledPages()
	case eventRotate:
		r.pendingScroll, r.state.Scroll = 0, 0
		r.state.Rotation = (r.state.Rotation + 1) & 3
		r.clearScaledPages()
	case eventFirst:
		r.pendingScroll, r.state.Scroll = 0, 0
		r.state.Page, r.state.Offset = 0, 0
	case eventLast:
		r.pendingScroll, r.state.Scroll = 0, 0
		r.state.Page = r.book.Len() - 1
		if r.state.Webtoon {
			r.state.Offset = 1
		} else {
			r.state.Offset = 0
		}
	case eventToggleSpread:
		if !r.state.Webtoon {
			r.state.Spread = !r.state.Spread
			r.state.Zoom, r.state.CenterX, r.state.CenterY = 1, 0.5, 0.5
		}
	case eventZoomIn:
		if !r.state.Webtoon {
			r.state.Zoom = min(8, r.state.Zoom*1.25)
		}
	case eventZoomOut:
		if !r.state.Webtoon {
			r.state.Zoom = max(1, r.state.Zoom/1.25)
			if r.state.Zoom <= 1.001 {
				r.state.Zoom, r.state.CenterX, r.state.CenterY = 1, 0.5, 0.5
			}
		}
	case eventResetView:
		r.state.Zoom, r.state.CenterX, r.state.CenterY = 1, 0.5, 0.5
	case eventPanUp:
		r.pan(0, -1)
	case eventPanDown:
		r.pan(0, 1)
	case eventPanLeft:
		r.pan(-1, 0)
	case eventPanRight:
		r.pan(1, 0)
	}
}

func (r *reader) pan(dx, dy float64) {
	if r.state.Webtoon || r.state.Zoom <= 1.001 {
		return
	}
	step := 0.15 / r.state.Zoom
	half := 0.5 / r.state.Zoom
	r.state.CenterX = min(1-half, max(half, r.state.CenterX+dx*step))
	r.state.CenterY = min(1-half, max(half, r.state.CenterY+dy*step))
}
