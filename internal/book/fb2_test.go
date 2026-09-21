package book

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

func fb2Fixture(t *testing.T) string {
	t.Helper()
	return `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
<description><title-info><book-title>Fiction Title</book-title><coverpage><image l:href="#cover"/></coverpage></title-info></description>
<body>
<title><p>Fiction Title</p></title>
<section><title><p>Chapter One</p></title>
<p>Hello <emphasis>reader</emphasis> &amp; friends.</p>
<empty-line/>
<subtitle>* * *</subtitle>
<poem><title><p>Ode</p></title><stanza><v>First verse</v><v>Second verse</v></stanza></poem>
<image l:href="#missing"/>
<image l:href="#vector"/>
<image l:href="#cover"/>
<p>After the picture.</p>
</section>
</body>
<body name="notes"><section id="n1"><title><p>1</p></title><p>A note.</p></section></body>
<binary id="cover" content-type="image/png">` + base64.StdEncoding.EncodeToString(epubImage(t)) + `</binary>
<binary id="broken" content-type="image/png">not base64!</binary>
<binary id="vector" content-type="image/svg+xml">PHN2Zy8+</binary>
</FictionBook>`
}

func checkFB2(t *testing.T, path, greeting string) {
	t.Helper()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Title != "Fiction Title" || b.Len() != 5 || b.Path != path {
		t.Fatalf("book: %q %d %q", b.Title, b.Len(), b.Path)
	}
	for _, i := range []int{0, 3} {
		if b.IsTextPage(i) || b.CanInvertPage(i) || b.pages[i].Name() != "image-0" {
			t.Fatalf("page %d is not the cover", i)
		}
		if img, err := b.Page(i); err != nil || img.Bounds().Dx() != 3 {
			t.Fatalf("cover %d: %v", i, err)
		}
	}
	if got := textEPUBPage(t, b, 1); got != "Fiction Title" {
		t.Fatalf("title page: %q", got)
	}
	want := []string{"Chapter One", greeting + " reader & friends.", "* * *", "Ode", "First verse", "Second verse"}
	if got := b.PageText(2); !reflect.DeepEqual(got, want) {
		t.Fatalf("chapter page: %#v", got)
	}
	if got := b.PageText(4); !reflect.DeepEqual(got, []string{"After the picture.", "1", "A note."}) {
		t.Fatalf("notes page: %#v", got)
	}
	chapters, err := b.Chapters()
	if err != nil || !reflect.DeepEqual(chapters, []Chapter{{"Fiction Title", 1}, {"Chapter One", 2}}) {
		t.Fatalf("chapters: %#v, %v", chapters, err)
	}
	bundled, err := epubBundledFonts()
	if err != nil {
		t.Fatal(err)
	}
	lines := b.pages[2].(epubTextPage).lines
	for i, style := range map[int]int{0: 1, 1: 0, 2: 1, 3: 1, 4: 0} {
		if lines[i].runs[0].font != bundled[style] {
			t.Fatalf("line %d %q uses the wrong style", i, lines[i].text)
		}
	}
}

func TestOpenFB2PlainZippedAndLegacyEncoding(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.fb2")
	if err := os.WriteFile(plain, []byte("\ufeff"+fb2Fixture(t)), 0o644); err != nil {
		t.Fatal(err)
	}
	checkFB2(t, plain, "Hello")
	checkFB2(t, writeEPUB(t, []memEntry{{"book.fb2", []byte(fb2Fixture(t))}}, ".fb2.zip"), "Hello")

	legacy := strings.Replace(fb2Fixture(t), `encoding="utf-8"`, `encoding="windows-1251"`, 1)
	legacy = strings.Replace(legacy, "Hello", "Привет", 1)
	encoded, err := charmap.Windows1251.NewEncoder().Bytes([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "legacy.fb2")
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	checkFB2(t, path, "Привет")

	for _, tc := range []struct {
		name  string
		order unicode.Endianness
		label string
	}{{"wide-le.fb2", unicode.LittleEndian, "UTF-16"}, {"wide-be.fb2", unicode.BigEndian, "utf-16be"}} {
		wide := strings.Replace(fb2Fixture(t), `encoding="utf-8"`, `encoding="`+tc.label+`"`, 1)
		encoded, err := unicode.UTF16(tc.order, unicode.UseBOM).NewEncoder().Bytes([]byte(wide))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, tc.name)
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		checkFB2(t, path, "Hello")
	}
}
