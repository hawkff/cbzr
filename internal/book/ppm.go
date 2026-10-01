package book

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
)

// ppmMIME is the type the page pipeline sees for converter output. Browsers
// do not display it; PageBytes converts it to PNG.
const ppmMIME = "image/x-portable-pixmap"

// pdftoppm and ddjvu write binary PPM, which decodes in one pass. PNG would
// cost a compression round trip on every page turn.
func init() { image.RegisterFormat("x-portable-pixmap", "P6", decodePPM, decodePPMConfig) }

func ppmHeader(r *bufio.Reader) (w, h int, err error) {
	var magic string
	var depth int
	if _, err := fmt.Fscan(r, &magic, &w, &h, &depth); err != nil || magic != "P6" || depth != 255 || w < 1 || h < 1 || w > maxPagePixels/h {
		return 0, 0, errors.New("unsupported PPM page")
	}
	_, err = r.ReadByte() // one whitespace byte precedes the pixels
	return w, h, err
}

func decodePPMConfig(r io.Reader) (image.Config, error) {
	w, h, err := ppmHeader(bufio.NewReader(r))
	return image.Config{ColorModel: color.RGBAModel, Width: w, Height: h}, err
}

func decodePPM(r io.Reader) (image.Image, error) {
	br := bufio.NewReader(r)
	w, h, err := ppmHeader(br)
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	row := make([]byte, 3*w)
	for y := range h {
		if _, err := io.ReadFull(br, row); err != nil {
			return nil, errors.New("truncated PPM page")
		}
		pix := img.Pix[y*img.Stride : (y+1)*img.Stride]
		for x := range w {
			pix[4*x], pix[4*x+1], pix[4*x+2], pix[4*x+3] = row[3*x], row[3*x+1], row[3*x+2], 255
		}
	}
	return img, nil
}
