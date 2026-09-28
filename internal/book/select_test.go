package book

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestTextLayerCacheIsBounded(t *testing.T) {
	b := &Book{}
	for range maxTextLayers + 1 {
		b.pages = append(b.pages, epubTextPage{lines: []epubLine{{text: "text"}}})
	}
	for i := range b.pages {
		b.PrepareText(i)
		if !b.TextReady(i) {
			t.Fatal("prepared layer is missing")
		}
	}
	if len(b.layers) != maxTextLayers || len(b.layerOrder) != maxTextLayers || b.TextReady(0) {
		t.Fatal("text cache did not evict the oldest layer")
	}
	b.PrepareText(0)
	if !b.TextReady(0) || b.TextReady(1) || len(b.layers) != maxTextLayers {
		t.Fatal("text cache did not reload an evicted layer")
	}
	b.PrepareText(-1)
	b.PrepareText(b.Len())
	if b.lines(-1) != nil || b.lines(b.Len()) != nil {
		t.Fatal("invalid page has a text layer")
	}
}

func TestPDFPreparationSharesWorkAndHitsDoNotExtract(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("shell helpers need sh")
	}
	dir := t.TempDir()
	count := filepath.Join(dir, "calls")
	t.Setenv("CBZR_TEST_TEXT_CALLS", count)
	for name, script := range map[string]string{
		"pdftotext": "printf 'call\\n' >> \"$CBZR_TEST_TEXT_CALLS\"\n" +
			"sleep 0.2\n" +
			"printf '%s' '<html><body><doc><page width=\"200\" height=\"100\"><flow><block><line><word xMin=\"10\" yMin=\"10\" xMax=\"80\" yMax=\"20\">Hello</word></line></block></flow></page></doc></body></html>'\n",
		"pdftohtml": "printf '%s' '<pdf2xml><page width=\"200\" height=\"100\"/></pdf2xml>'\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!"+sh+"\n"+script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	b := &Book{pages: []entry{toolPage{path: "synthetic.pdf", page: 1}}}
	b.LinkAt(0, 0.1, 0.15)
	b.Select(0, 0, 0, 1, 1)
	if _, err := os.Stat(count); !os.IsNotExist(err) || b.TextReady(0) {
		t.Fatal("cold hit testing started an extraction")
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() { b.PrepareText(0) })
	}
	wg.Wait()
	calls, err := os.ReadFile(count)
	if err != nil || string(calls) != "call\n" {
		t.Fatalf("shared extraction calls: %q, %v", calls, err)
	}
	if text, _ := b.Select(0, 0, 0, 1, 1); text != "Hello" {
		t.Fatalf("prepared selection = %q", text)
	}
	// A failed extraction stays cached instead of retrying on mouse motion.
	if err := os.WriteFile(filepath.Join(dir, "pdftotext"), []byte("#!"+sh+"\nprintf 'call\\n' >> \"$CBZR_TEST_TEXT_CALLS\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	failed := &Book{pages: []entry{toolPage{path: "synthetic.pdf", page: 1}}}
	failed.PrepareText(0)
	failed.PrepareText(0)
	failed.LinkAt(0, 0.1, 0.15)
	calls, err = os.ReadFile(count)
	if err != nil || strings.Count(string(calls), "call\n") != 2 || !failed.TextReady(0) {
		t.Fatalf("failed extraction retried: %q, %v", calls, err)
	}
}
