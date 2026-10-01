package book

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func requireTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}

func TestDecodePPM(t *testing.T) {
	ppm := "P6\n2 1\n255\n\xff\x00\x00\x00\x00\xff"
	config, format, err := image.DecodeConfig(strings.NewReader(ppm))
	if err != nil || "image/"+format != ppmMIME || config.Width != 2 || config.Height != 1 {
		t.Fatalf("config: %+v %s, %v", config, format, err)
	}
	img, _, err := image.Decode(strings.NewReader(ppm))
	if err != nil || img.Bounds() != image.Rect(0, 0, 2, 1) {
		t.Fatalf("decoded page: %v", err)
	}
	r, _, _, _ := img.At(0, 0).RGBA()
	_, _, b, _ := img.At(1, 0).RGBA()
	if r != 0xffff || b != 0xffff {
		t.Fatal("pixel colors changed")
	}
	for _, bad := range []string{"P5\n2 1\n255\n\xff\xff", "P6\n2 1\n255\n\xff", "P6\n0 1\n255\n", "P6\n99999 99999\n255\n"} {
		if _, _, err := image.Decode(strings.NewReader(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	b2 := &Book{pages: []entry{memEntry{"page.ppm", []byte(ppm)}}}
	data, mime, err := b2.PageBytes(0)
	if err != nil || mime != "image/png" {
		t.Fatalf("served PPM as %s, %v", mime, err)
	}
	if served, err := png.Decode(bytes.NewReader(data)); err != nil || served.Bounds() != image.Rect(0, 0, 2, 1) {
		t.Fatalf("served PNG: %v", err)
	}
}

func TestOpenPDF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "two.pdf")
	pdf := "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R 4 0 R]/Count 2>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]/CropBox[0 0 100 100]>>endobj\n4 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 100 200]>>endobj\ntrailer<</Root 1 0 R>>\n"
	if err := os.WriteFile(path, []byte(pdf), 0o644); err != nil {
		t.Fatal(err)
	}
	shifted := filepath.Join(t.TempDir(), "shifted.pdf")
	if err := os.WriteFile(shifted, append([]byte(strings.Repeat("junk\n", 100)), pdf...), 0o644); err != nil {
		t.Fatal(err)
	}
	xmlShifted := filepath.Join(t.TempDir(), "xml-shifted.pdf")
	if err := os.WriteFile(xmlShifted, []byte("<junk>\n"+pdf), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("missing tools", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		for _, name := range []string{path, shifted, xmlShifted} {
			if _, err := Open(name); err == nil || !strings.Contains(err.Error(), "pdfinfo is not on PATH") {
				t.Fatalf("%s without pdfinfo: %v", filepath.Base(name), err)
			}
		}
	})
	requireTools(t, "pdfinfo", "pdftoppm", "pdftohtml")
	for _, name := range []string{shifted, xmlShifted} {
		b, err := Open(name)
		if err != nil {
			t.Fatal(err)
		}
		b.Close()
		if b.Len() != 2 {
			t.Fatalf("%s has %d pages", name, b.Len())
		}
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Title != "two" || b.Len() != 2 || b.HasText() || !b.CanInvertPage(0) {
		t.Fatalf("book: %q %d", b.Title, b.Len())
	}
	if chapters, err := b.Chapters(); err != nil || chapters != nil {
		t.Fatalf("chapters: %#v, %v", chapters, err)
	}
	img, err := b.Page(1)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds() != image.Rect(0, 0, 1000, 2000) {
		t.Fatalf("rendered %v", img.Bounds())
	}
	data, mime, err := b.PageBytes(0)
	if err != nil || mime != "image/png" {
		t.Fatalf("page bytes: %s, %v", mime, err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != 2000 || config.Height != 2000 {
		t.Fatalf("CropBox dimensions: %+v, %v", config, err)
	}
	if _, _, err := b.PageBytes(2); err == nil {
		t.Fatal("served a page past the count")
	}
}

func TestOpenDJVU(t *testing.T) {
	requireTools(t, "c44", "djvused", "ddjvu")
	dir := t.TempDir()
	source := filepath.Join(dir, "page.ppm")
	if err := os.WriteFile(source, []byte("P6\n64 32\n255\n"+strings.Repeat("\xff\x00\x00", 64*32)), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "scan.djvu")
	if out, err := exec.Command("c44", source, path).CombinedOutput(); err != nil {
		t.Fatalf("c44: %v: %s", err, out)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Title != "scan" || b.Len() != 1 || !b.CanInvertPage(0) {
		t.Fatalf("book: %q %d", b.Title, b.Len())
	}
	img, err := b.Page(0)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w > 2000 || h > 2000 || w < 2*h-2 || w > 2*h+2 {
		t.Fatalf("rendered %dx%d", w, h)
	}
	r, g, _, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()
	if r < 0x8000 || g > 0x8000 {
		t.Fatal("page lost its color")
	}
	if _, mime, err := b.PageBytes(0); err != nil || mime != "image/png" {
		t.Fatalf("page bytes: %s, %v", mime, err)
	}
}

func TestToolDiagnostics(t *testing.T) {
	var diagnostics toolDiagnostics
	payload := bytes.Repeat([]byte("x"), 3*maxToolDiagnostics)
	for range 2 {
		if n, err := diagnostics.Write(payload); err != nil || n != len(payload) {
			t.Fatalf("diagnostic drain: %d, %v", n, err)
		}
	}
	if len(diagnostics) != maxToolDiagnostics || !bytes.Equal(diagnostics, payload[:maxToolDiagnostics]) {
		t.Fatal("diagnostics did not keep a bounded prefix")
	}
	for _, mode := range []string{"success", "failure"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CBZR_TEST_TOOL_OUTPUT", mode)
			out, err := runTool(os.Args[0], "-test.run=^TestToolOutputHelper$")
			if mode == "success" {
				if err != nil || string(out) != "ok" {
					t.Fatalf("noisy tool: %q, %v", out, err)
				}
			} else if err == nil || !strings.HasSuffix(err.Error(), strings.Repeat("x", 200)) {
				t.Fatalf("tool error lost its diagnostic prefix: %v", err)
			}
		})
	}
}

func TestToolOutputHelper(t *testing.T) {
	mode := os.Getenv("CBZR_TEST_TOOL_OUTPUT")
	if mode == "" {
		return
	}
	if _, err := os.Stderr.Write(bytes.Repeat([]byte("x"), 256*maxToolDiagnostics)); err != nil {
		os.Exit(2)
	}
	if _, err := os.Stdout.WriteString("ok"); err != nil {
		os.Exit(2)
	}
	if mode == "failure" {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestOpenDOC(t *testing.T) {
	t.Run("missing tool", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, err := Open("testdata/paragraphs.doc"); err == nil || !strings.Contains(err.Error(), "antiword is not on PATH") {
			t.Fatalf("missing antiword: %v", err)
		}
	})
	requireTools(t, "antiword")
	b, err := Open("testdata/paragraphs.doc")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Title != "paragraphs" || b.Len() != 1 || !b.HasText() || !b.CanInvertPage(0) {
		t.Fatalf("book: %q %d", b.Title, b.Len())
	}
	if got := b.PageText(0); !reflect.DeepEqual(got, []string{"Hello paragraph one.", "Second paragraph here."}) {
		t.Fatalf("paragraphs: %#v", got)
	}
}

// linkedPDF writes two pages of Helvetica text with a URI link over "visit
// example" and a page link over "next page". crop trims the first page's
// crop box so the media and crop boxes differ.
func linkedPDF(t *testing.T, name string, crop bool) string {
	t.Helper()
	first := "BT /F1 12 Tf 10 80 Td (Hello reader) Tj 0 -20 Td (visit example) Tj 0 -20 Td (next page) Tj ET"
	second := "BT /F1 12 Tf 10 80 Td (Second) Tj ET"
	box := ""
	if crop {
		box = "/CropBox[5 10 200 95]"
	}
	pdf := "%PDF-1.4\n" +
		"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
		"2 0 obj<</Type/Pages/Kids[3 0 R 6 0 R]/Count 2>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]" + box + "/Resources<</Font<</F1 4 0 R>>>>/Contents 5 0 R/Annots[7 0 R 8 0 R]>>endobj\n" +
		"4 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\n" +
		"5 0 obj<</Length " + strconv.Itoa(len(first)) + ">>stream\n" + first + "\nendstream\nendobj\n" +
		"6 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]/Resources<</Font<</F1 4 0 R>>>>/Contents 9 0 R>>endobj\n" +
		"7 0 obj<</Type/Annot/Subtype/Link/Rect[10 56 90 72]/Border[0 0 0]/A<</S/URI/URI(https://example.com/a?utm_source=pdf)>>>>endobj\n" +
		"8 0 obj<</Type/Annot/Subtype/Link/Rect[10 36 70 52]/Border[0 0 0]/Dest[6 0 R /Fit]>>endobj\n" +
		"9 0 obj<</Length " + strconv.Itoa(len(second)) + ">>stream\n" + second + "\nendstream\nendobj\n" +
		"trailer<</Root 1 0 R>>\n"
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(pdf), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPDFTextLayerSelectionAndLinks(t *testing.T) {
	requireTools(t, "pdfinfo", "pdftotext", "pdftohtml")
	for _, crop := range []bool{false, true} {
		t.Run(map[bool]string{false: "media box", true: "crop box"}[crop], func(t *testing.T) {
			b, err := Open(linkedPDF(t, "linked.pdf", crop))
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			if !b.Selectable(0) || b.Selectable(2) || b.IsTextPage(0) {
				t.Fatal("PDF pages must be selectable without being text pages")
			}
			if b.lines(0) != nil || b.TextReady(0) {
				t.Fatal("PDF hit testing must not extract a cold layer")
			}
			b.PrepareText(0)
			lines := b.lines(0)
			if len(lines) != 3 || lines[1].text != "visit example" || !lines[1].end || len(lines[1].boxes) != 2 {
				t.Fatalf("lines: %#v", lines)
			}
			mid := func(line textLine, word int) (float64, float64) {
				box := line.boxes[word]
				return (box.x0 + box.x1) / 2, (line.top + line.bottom) / 2
			}
			linkAt := func(line textLine, word int) (Link, bool) {
				x, y := mid(line, word)
				return b.LinkAt(0, x, y)
			}
			if link, ok := linkAt(lines[1], 0); !ok || link.URL != "https://example.com/a?utm_source=pdf" || link.Page != -1 {
				t.Fatalf("URI link: %#v %v", link, lines[1].links)
			}
			if link, ok := linkAt(lines[2], 1); !ok || link.URL != "" || link.Page != 1 {
				t.Fatalf("page link: %#v %v", link, lines[2].links)
			}
			if _, ok := linkAt(lines[0], 0); ok {
				t.Fatal("plain words are not links")
			}
			text, boxes := b.Select(0, 0, 0, 1, 1)
			if text != "Hello reader\nvisit example\nnext page" || len(boxes) != 3 || boxes[0].X0 <= 0 || boxes[0].Y1 <= boxes[0].Y0 || boxes[2].Y1 > 1 {
				t.Fatalf("page selection: %q %v", text, boxes)
			}
			x, y := mid(lines[0], 1)
			if text, _ := b.Select(0, x-0.05, y, 1, 1); text != "reader\nvisit example\nnext page" {
				t.Fatalf("selection from a word: %q", text)
			}
			b.PrepareText(1)
			if text, _ := b.Select(1, 0, 0, 1, 1); text != "Second" {
				t.Fatalf("second page: %q", text)
			}
			if b.layers[0] == nil || len(b.layerOrder) != 2 {
				t.Fatalf("layer cache: %v", b.layerOrder)
			}
		})
	}
	if _, internal, ok := pdfLinkTarget("Tricky name.html#12"); !internal || !ok {
		t.Fatal("page reference with spaces")
	}
	if target, internal, ok := pdfLinkTarget("issue#2.html#12"); target != "#12" || !internal || !ok {
		t.Fatalf("page reference behind a '#' in the document name: %q", target)
	}
	if _, _, ok := pdfLinkTarget("file:///etc/passwd"); ok {
		t.Fatal("file link survived")
	}
	if text := pdfWordText("skylining@live.com\u200b"); text != "skylining@live.com" {
		t.Fatalf("invisible characters: %q", text)
	}
}
