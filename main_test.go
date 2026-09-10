package main

import (
	"archive/zip"
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func checkFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	entries := map[string]string{"page.png": "image intentionally decoded only on page access"}
	if strings.HasSuffix(name, ".epub") {
		text := "Text layout"
		if name == "missing-glyph.epub" {
			text = "&#x10FFFF;"
		}
		entries = map[string]string{
			"META-INF/container.xml": `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
			"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf"><manifest><item id="text" href="text.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="text"/></spine></package>`,
			"text.xhtml":             `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>` + text + `</p></body></html>`,
		}
	}
	for name, data := range entries {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckBooksBatch(t *testing.T) {
	good := checkFixture(t, "book.cbz")
	epub := checkFixture(t, "book.epub")
	missingGlyph := checkFixture(t, "missing-glyph.epub")
	bad := filepath.Join(t.TempDir(), "missing\n\x1b[31m.epub")
	var out, diagnostics bytes.Buffer
	if status := checkBooks([]string{good, bad, good}, &out, &diagnostics); status != 1 {
		t.Fatalf("status = %d", status)
	}
	if strings.Count(out.String(), ": OK") != 2 || !strings.Contains(out.String(), "images not decoded") {
		t.Fatalf("batch did not continue: %q", out.String())
	}
	if !strings.Contains(diagnostics.String(), strconv.Quote(bad)) || strings.ContainsRune(diagnostics.String(), '\x1b') || strings.Count(diagnostics.String(), "\n") != 1 {
		t.Fatalf("unsafe diagnostics: %q", diagnostics.String())
	}
	out.Reset()
	diagnostics.Reset()
	if status := checkBooks([]string{good, epub, good}, &out, &diagnostics); status != 0 || diagnostics.Len() != 0 {
		t.Fatalf("valid batch: status=%d, %q", status, diagnostics.String())
	}
	diagnostics.Reset()
	if status := checkBooks([]string{missingGlyph, epub}, &out, &diagnostics); status != 1 || !strings.Contains(diagnostics.String(), "lacks glyph") {
		t.Fatalf("text layout was not checked: %d, %q", status, diagnostics.String())
	}
	if status := checkBooks(nil, &out, &diagnostics); status != 2 {
		t.Fatalf("empty batch status = %d", status)
	}
}

func TestCheckCLIHelperProcess(t *testing.T) {
	if os.Getenv("CBZR_TEST_CHECK_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			break
		}
	}
	flag.CommandLine = flag.NewFlagSet("cbzr", flag.ExitOnError)
	main()
	os.Exit(0)
}

func TestCheckCLIHeadless(t *testing.T) {
	good := checkFixture(t, "book.cbz")
	epub := checkFixture(t, "book.epub")
	for _, tc := range []struct {
		args   []string
		status int
	}{
		{[]string{"-check", good, epub, good}, 0},
		{[]string{"-check", filepath.Join(t.TempDir(), "absent.epub"), good}, 1},
		{[]string{"-check"}, 2},
	} {
		config := t.TempDir()
		args := append([]string{"-test.run=^TestCheckCLIHelperProcess$", "--"}, tc.args...)
		cmd := exec.Command(os.Args[0], args...)
		cmd.Env = append(os.Environ(), "CBZR_TEST_CHECK_PROCESS=1", "HOME="+config, "XDG_CONFIG_HOME="+config)
		output, err := cmd.CombinedOutput()
		status := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			status = exit.ExitCode()
		}
		if status != tc.status || bytes.ContainsRune(output, '\x1b') {
			t.Fatalf("headless check status=%d, output=%q", status, output)
		}
		entries, err := os.ReadDir(config)
		if err != nil || len(entries) != 0 {
			t.Fatalf("check touched configuration: %v, %v", entries, err)
		}
	}
}
