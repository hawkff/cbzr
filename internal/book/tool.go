package book

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// renderedPageSize bounds each side of a rendered PDF or DJVU page.
	renderedPageSize   = "2000"
	maxRenderedPages   = 100_000
	toolTimeout        = 2 * time.Minute
	maxToolDiagnostics = 4 << 10
)

// toolDiagnostics keeps a prefix and drains the rest so stderr cannot block a tool.
type toolDiagnostics []byte

func (d *toolDiagnostics) Write(p []byte) (int, error) {
	*d = append(*d, p[:min(len(p), maxToolDiagnostics-len(*d))]...)
	return len(p), nil
}

// runTool runs an external converter and returns its standard output, which
// may not exceed maxPageBytes.
func runTool(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var diagnostics toolDiagnostics
	cmd.Stderr = &diagnostics
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%s is not on PATH", name)
		}
		return nil, err
	}
	out, readErr := readBounded(stdout, maxPageBytes)
	if readErr != nil {
		cancel() // stop a writer that outgrew the limit before waiting for it
	}
	if err := cmd.Wait(); err != nil && readErr == nil {
		msg := strings.TrimSpace(string(diagnostics))
		if msg == "" {
			msg = err.Error()
		}
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("%s: %s", name, msg)
	}
	if readErr != nil {
		return nil, fmt.Errorf("%s: %w", name, readErr)
	}
	return out, nil
}

// toolPage renders one PDF or DJVU page on access.
type toolPage struct {
	path string
	page int // 1-based
	djvu bool
}

func (p toolPage) Name() string { return "page-" + strconv.Itoa(p.page) + ".png" }

func (p toolPage) Open() (io.ReadCloser, error) {
	n := strconv.Itoa(p.page)
	var data []byte
	var err error
	if p.djvu {
		data, err = runTool("ddjvu", "-format=ppm", "-size="+renderedPageSize+"x"+renderedPageSize, "-page="+n, p.path)
		if err == nil {
			data, err = ppmToPNG(data)
		}
	} else {
		data, err = runTool("pdftoppm", "-png", "-cropbox", "-scale-to", renderedPageSize, "-f", n, "-l", n, "-singlefile", p.path)
	}
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// openRendered counts pages with poppler or djvulibre; pages render on access.
func openRendered(path string, djvu bool) (*Book, error) {
	tool, args := "pdfinfo", []string{path}
	if djvu {
		tool, args = "djvused", []string{"-e", "n", path}
	}
	out, err := runTool(tool, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	count := 0
	if djvu {
		count, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	} else {
		for _, line := range strings.Split(string(out), "\n") {
			if v, ok := strings.CutPrefix(line, "Pages:"); ok {
				count, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
	}
	if count < 1 || count > maxRenderedPages {
		return nil, fmt.Errorf("%s: %s reported %d pages", filepath.Base(path), tool, count)
	}
	b := newBook(path, false)
	for i := 1; i <= count; i++ {
		b.pages = append(b.pages, toolPage{path, i, djvu})
	}
	return b, nil
}

// ppmToPNG converts the binary PPM ddjvu writes into PNG for the page pipeline.
func ppmToPNG(data []byte) ([]byte, error) {
	r := bufio.NewReader(bytes.NewReader(data))
	var magic string
	var w, h, depth int
	if _, err := fmt.Fscan(r, &magic, &w, &h, &depth); err != nil || magic != "P6" || depth != 255 || w < 1 || h < 1 || w > maxPagePixels/h {
		return nil, errors.New("ddjvu wrote no usable PPM page")
	}
	if _, err := r.ReadByte(); err != nil { // one whitespace byte precedes the pixels
		return nil, err
	}
	pix := make([]byte, 3*w*h)
	if _, err := io.ReadFull(r, pix); err != nil {
		return nil, errors.New("ddjvu wrote a truncated PPM page")
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range w * h {
		copy(img.Pix[4*i:], pix[3*i:3*i+3])
		img.Pix[4*i+3] = 255
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// openDOC lays the paragraphs antiword extracts out as text pages.
func openDOC(path string) (*Book, error) {
	out, err := runTool("antiword", "-w", "0", "-m", "UTF-8.txt", path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	body := &epubNode{name: xhtml("body")}
	for _, line := range strings.Split(string(out), "\n") {
		body.children = append(body.children, &epubNode{name: xhtml("p"), children: []*epubNode{{text: line}}})
	}
	return layoutBook(newBook(path, true), &epubPackage{}, body, nil)
}
