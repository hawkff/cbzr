package render

import (
	"fmt"
	"image"
	"math"

	xdraw "golang.org/x/image/draw"
)

// PageLoader returns one decoded page by zero-based index.
type PageLoader func(int) (image.Image, error)

// ComposeWebtoon builds a viewport from consecutive pages. Wide pages shrink
// to the viewport; narrow pages keep their native size. Offset is the
// fractional vertical position in page. scrollRows may contain fractional
// terminal rows and may cross page boundaries.
func ComposeWebtoon(load PageLoader, pageCount, page int, offset, scrollRows float64, cols, rows, rot int, cw, ch float64) (*image.RGBA, int, float64, error) {
	if pageCount < 1 {
		return nil, 0, 0, fmt.Errorf("webtoon: no pages")
	}
	page = min(pageCount-1, max(0, page))
	cols, rows = max(1, cols), max(1, rows)
	cw, ch = max(1, cw), max(1, ch)
	viewW := max(1, int(math.Round(float64(cols)*cw)))
	viewH := max(1, int(math.Round(float64(rows)*ch)))

	type pageImage struct {
		img           image.Image
		width, height int
	}
	get := func(i int) (pageImage, error) {
		img, err := load(i)
		if err != nil {
			return pageImage{}, err
		}
		img = Transform(img, rot, 1, 0.5, 0.5)
		b := img.Bounds()
		if b.Dx() < 1 || b.Dy() < 1 {
			return pageImage{}, fmt.Errorf("webtoon: page %d has invalid dimensions", i+1)
		}
		scale := min(1, float64(viewW)/float64(b.Dx()))
		width := max(1, int(math.Round(float64(b.Dx())*scale)))
		height := max(1, int(math.Round(float64(b.Dy())*scale)))
		return pageImage{img: img, width: width, height: height}, nil
	}

	current, err := get(page)
	if err != nil {
		return nil, page, offset, err
	}
	pageH := current.height
	position := min(1, max(0, offset))*float64(pageH) + float64(scrollRows)*ch
	for position >= float64(pageH) && page < pageCount-1 {
		position -= float64(pageH)
		page++
		current, err = get(page)
		if err != nil {
			return nil, page, 0, err
		}
		pageH = current.height
	}
	for position < 0 && page > 0 {
		page--
		current, err = get(page)
		if err != nil {
			return nil, page, 0, err
		}
		pageH = current.height
		position += float64(pageH)
	}
	if page == 0 {
		position = max(0, position)
	}

	// Backfill from preceding pages to reduce empty space below the strip.
	available := float64(pageH) - position
	for next := page + 1; available < float64(viewH) && next < pageCount; next++ {
		p, loadErr := get(next)
		if loadErr != nil {
			return nil, page, 0, loadErr
		}
		available += float64(p.height)
	}
	if available < float64(viewH) {
		position -= float64(viewH) - available
		for position < 0 && page > 0 {
			page--
			current, err = get(page)
			if err != nil {
				return nil, page, 0, err
			}
			pageH = current.height
			position += float64(pageH)
		}
		if page == 0 {
			position = max(0, position)
		}
	}
	offset = position / float64(pageH)

	topPage, topOffset := page, offset
	viewport := image.NewRGBA(image.Rect(0, 0, viewW, viewH))
	y := 0
	for y < viewH {
		top := 0
		if y == 0 {
			top = min(pageH, max(0, int(math.Round(position))))
		}
		x := (viewW - current.width) / 2
		dst := image.Rect(x, y-top, x+current.width, y-top+pageH)
		xdraw.ApproxBiLinear.Scale(viewport, dst, current.img, current.img.Bounds(), xdraw.Src, nil)
		y += pageH - top
		if y >= viewH || page+1 >= pageCount {
			break
		}
		page++
		current, err = get(page)
		if err != nil {
			return nil, page, 0, err
		}
		pageH = current.height
		position = 0
	}

	return viewport, topPage, topOffset, nil
}
