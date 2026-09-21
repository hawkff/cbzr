package book

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

func TestPPMToPNG(t *testing.T) {
	data, err := ppmToPNG([]byte("P6\n2 1\n255\n\xff\x00\x00\x00\x00\xff"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil || img.Bounds() != image.Rect(0, 0, 2, 1) {
		t.Fatalf("decoded page: %v", err)
	}
	r, _, _, _ := img.At(0, 0).RGBA()
	_, _, b, _ := img.At(1, 0).RGBA()
	if r != 0xffff || b != 0xffff {
		t.Fatal("pixel colors changed")
	}
	for _, bad := range []string{"P5\n2 1\n255\n\xff\xff", "P6\n2 1\n255\n\xff", "P6\n0 1\n255\n", "P6\n99999 99999\n255\n"} {
		if _, err := ppmToPNG([]byte(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestOpenPDF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "two.pdf")
	pdf := "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R 4 0 R]/Count 2>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]>>endobj\n4 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 100 200]>>endobj\ntrailer<</Root 1 0 R>>\n"
	if err := os.WriteFile(path, []byte(pdf), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("missing tools", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "pdfinfo is not on PATH") {
			t.Fatalf("missing pdfinfo: %v", err)
		}
	})
	requireTools(t, "pdfinfo", "pdftoppm")
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
	if _, mime, err := b.PageBytes(0); err != nil || mime != "image/png" {
		t.Fatalf("page bytes: %s, %v", mime, err)
	}
	if _, err := b.PageBytes(2); err == nil {
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
