package book

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	return runToolContext(context.Background(), name, args...)
}

func runToolContext(ctx context.Context, name string, args ...string) ([]byte, error) {
	return runToolLimited(ctx, maxPageBytes, name, args...)
}

func runToolLimited(ctx context.Context, limit int64, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
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
	out, readErr := readBounded(stdout, limit)
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

func (p toolPage) Name() string { return "page-" + strconv.Itoa(p.page) + ".ppm" }

func (p toolPage) Open() (io.ReadCloser, error) {
	return p.open(context.Background())
}

// open renders the page as binary PPM, which the registered decoder reads in
// one pass; see ppm.go.
func (p toolPage) open(ctx context.Context) (io.ReadCloser, error) {
	n := strconv.Itoa(p.page)
	args := []string{"pdftoppm", "-cropbox", "-scale-to", renderedPageSize, "-f", n, "-l", n, "-singlefile", p.path}
	if p.djvu {
		args = []string{"ddjvu", "-format=ppm", "-size=" + renderedPageSize + "x" + renderedPageSize, "-page=" + n, p.path}
	}
	data, err := runToolContext(ctx, args[0], args[1:]...)
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

// openDOC keeps the headings in antiword's DocBook output.
func openDOC(path string) (*Book, error) {
	out, err := runTool("antiword", "-x", "db", path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	doc, err := parseXML(out, maxDocumentTokens)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	body := &epubNode{name: xhtml("body"), children: []*epubNode{docbookNode(doc, 0)}}
	return layoutBook(newBook(path, true), &epubPackage{}, body, nil)
}
