package book

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestPDFOutlineDestinations(t *testing.T) {
	requireTools(t, "pdfinfo", "pdftohtml")
	pdf := `%PDF-1.4
1 0 obj<</Type/Catalog/Pages 2 0 R/Outlines 5 0 R/Names<</Dests<</Names[(later)[4 0 R /Fit]]>>>>>>endobj
2 0 obj<</Type/Pages/Kids[3 0 R 4 0 R]/Count 2>>endobj
3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 100 100]>>endobj
4 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 100 100]>>endobj
5 0 obj<</Type/Outlines/First 6 0 R/Last 8 0 R/Count 3>>endobj
6 0 obj<</Title(Part & one)/Parent 5 0 R/Dest[3 0 R /Fit]/First 7 0 R/Last 7 0 R/Count 1/Next 8 0 R>>endobj
7 0 obj<</Title<FEFF00430061006600E9>/Parent 6 0 R/A<</S/GoTo/D(later)>>>>endobj
8 0 obj<</Title(Web)/Parent 5 0 R/Prev 6 0 R/A<</S/URI/URI(https://example.com/)>>>>endobj
trailer<</Root 1 0 R>>
`
	path := filepath.Join(t.TempDir(), "renamed.cbz")
	if err := os.WriteFile(path, []byte(pdf), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	want := []Chapter{{"Part & one", 0, 0}, {"Café", 1, 1}}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if got, err := b.Chapters(); err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("outline = %#v, %v", got, err)
			}
		})
	}
	wg.Wait()
	if len(b.cache) != 0 || len(b.encoded) != 0 {
		t.Fatal("outline extraction rendered pages")
	}
	t.Setenv("PATH", t.TempDir())
	if got, err := b.Chapters(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("cached outline = %#v, %v", got, err)
	}
	cold := newBook(path, false)
	cold.pages = []entry{toolPage{path: path, page: 1}}
	defer cold.Close()
	if _, err := cold.Chapters(); err == nil || !strings.Contains(err.Error(), "pdftohtml is not on PATH") {
		t.Fatalf("missing outline tool: %v", err)
	}
}

func TestPDFOutlineRejectsInvalidTargets(t *testing.T) {
	data := []byte(`<pdf2xml><outline><item page="2">Last</item><outline><item page="1">First &amp; nested</item><item page="0">Zero</item></outline><item page="3">Past end</item><item page="oops">Bad</item><item>External</item><item page="1"> </item><item>Part</item><outline><item page="2">Inside part</item></outline></outline></pdf2xml>`)
	want := []Chapter{{"Last", 1, 0}, {"First & nested", 0, 1}, {"Part", 1, 0}, {"Inside part", 1, 1}}
	if got, err := pdfOutline(data, 2); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("outline = %#v, %v", got, err)
	}
	for _, data := range []string{`<pdf2xml>`, `<html/>`} {
		if _, err := pdfOutline([]byte(data), 2); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}

func TestDJVUOutline(t *testing.T) {
	names := djvuPageNames("     I      100  shared.djvu\n   1 P      100  cover.djvu\n   2 P      120  scan 02.djvu F=saved.djvu T=ii\n")
	data := `(bookmarks ("Part \"one\"" "#1" ("Caf\303\251" "#scan%2002.djvu")) ("Group" "" ("Appendix" "?page=ii")) ("File" "#saved.djvu") ("Web" "https://example.com/#1") ("Missing" "#absent") ("Zero" "#0") ("Past end" "#3") ("Relative" "#+1"))`
	want := []Chapter{{"Part \"one\"", 0, 0}, {"Café", 1, 1}, {"Group", 1, 0}, {"Appendix", 1, 1}, {"File", 1, 0}}
	if got, err := djvuOutline([]byte(data), 2, names); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("outline = %#v, %v", got, err)
	}
	for _, data := range []string{
		`(bookmarks ("Truncated" "#1")`, `(bookmarks (oops "#1"))`, `(bookmarks ("Bad\q" "#1"))`, `(bookmarks) extra`,
		`(bookmarks ` + strings.Repeat(`("Nested" "#1" `, maxEPUBXMLDepth+1) + strings.Repeat(")", maxEPUBXMLDepth+2),
	} {
		if _, err := djvuOutline([]byte(data), 2, nil); err == nil {
			t.Fatal("accepted malformed or unbounded outline")
		}
	}
}

func TestDJVUOutlineDestinations(t *testing.T) {
	requireTools(t, "c44", "djvm", "djvused")
	dir := t.TempDir()
	source := filepath.Join(dir, "page.ppm")
	if err := os.WriteFile(source, []byte("P6\n64 32\n255\n"+strings.Repeat("\xff\xff\xff", 64*32)), 0o644); err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(dir, "first.djvu"), filepath.Join(dir, "second.djvu")
	for _, page := range []string{first, second} {
		if out, err := exec.Command("c44", source, page).CombinedOutput(); err != nil {
			t.Fatalf("c44: %v: %s", err, out)
		}
	}
	path := filepath.Join(dir, "book.djvu")
	if out, err := exec.Command("djvm", "-c", path, first, second).CombinedOutput(); err != nil {
		t.Fatalf("djvm: %v: %s", err, out)
	}
	script := filepath.Join(dir, "outline.dsed")
	if err := os.WriteFile(script, []byte("set-outline\n(bookmarks (\"First\" \"#1\" (\"Caf\\303\\251\" \"#second.djvu\")))\n.\nsave\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("djvused", "-f", script, path).CombinedOutput(); err != nil {
		t.Fatalf("djvused: %v: %s", err, out)
	}
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got, err := b.Chapters(); err != nil || !reflect.DeepEqual(got, []Chapter{{"First", 0, 0}, {"Café", 1, 1}}) {
		t.Fatalf("DJVU outline = %#v, %v", got, err)
	}
}

func TestEPUBNavigationSources(t *testing.T) {
	const nav = `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="landmarks"><ol><li><a href="text/z.xhtml">Wrong list</a></li></ol></nav><nav epub:type="toc"><ol><li><span>Part</span><ol><li><a href="text/z.xhtml#first%3Aid">Navigation title</a></li><li><a href="text/z.xhtml#deep">Deep target</a></li></ol></li><li><a href="text/a.xhtml">Afterword</a></li><li><a href="text/z.xhtml#missing">Broken</a></li><li><a href="https://example.com/">Web</a></li><li><a href="../../../outside">Outside</a></li></ol></nav></body></html>`
	const ncx = `<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap><navPoint><navLabel><text>NCX title</text></navLabel><content src="text/z.xhtml#first%3Aid"/><navPoint><navLabel><text>NCX nested</text></navLabel><content src="text/a.xhtml"/></navPoint></navPoint></navMap></ncx>`
	for _, source := range []string{"nav", "ncx", "both", "broken nav", "broken both", "invalid targets"} {
		t.Run(source, func(t *testing.T) {
			entries := epubFixture()
			replaceEPUB(entries, "OPS/text/z.xhtml", "<h1>", `<h1 id="first:id">`)
			replaceEPUB(entries, "OPS/text/z.xhtml", "</body>", `<p>`+strings.Repeat("word ", 800)+`<span id="deep"/>Tail</p></body>`)
			navData, ncxData := nav, ncx
			if strings.HasPrefix(source, "broken") {
				navData = "<html>"
			}
			if source == "broken both" {
				ncxData = "<ncx>"
			}
			if source == "invalid targets" {
				navData = strings.ReplaceAll(nav, "text/", "missing/")
			}
			if source != "ncx" {
				replaceEPUB(entries, "OPS/book.opf", "</manifest>", `<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="scripted nav"/></manifest>`)
				entries = append(entries, memEntry{"OPS/nav.xhtml", []byte(navData)})
			}
			if source != "nav" && source != "invalid targets" {
				replaceEPUB(entries, "OPS/book.opf", "</manifest>", `<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/></manifest>`)
				replaceEPUB(entries, "OPS/book.opf", "<spine>", `<spine toc="ncx">`)
				entries = append(entries, memEntry{"OPS/toc.ncx", []byte(ncxData)})
			}
			b, err := Open(writeEPUB(t, entries, ".epub"))
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			last := b.Len() - 1
			want := []Chapter{{"Part", 0, 0}, {"Navigation title", 0, 1}, {"Deep target", b.anchors["OPS/text/z.xhtml#deep"], 1}, {"Afterword", last, 0}}
			switch source {
			case "ncx", "broken nav":
				want = []Chapter{{"NCX title", 0, 0}, {"NCX nested", last, 1}}
			case "broken both", "invalid targets":
				want = []Chapter{{"First heading", 0, 0}, {"Notes", last, 0}}
			}
			if got, err := b.Chapters(); err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("chapters = %#v, %v; want %#v", got, err, want)
			}
			if b.anchors["OPS/text/z.xhtml#deep"] <= 0 {
				t.Fatal("fixture did not paginate the fragment target")
			}
		})
	}
}

func TestEPUBImageSpineNavigation(t *testing.T) {
	entries := epubFixture()
	replaceEPUB(entries, "OPS/book.opf", `href="text/z.xhtml" media-type="application/xhtml+xml"`, `href="panel.png" media-type="image/png"`)
	replaceEPUB(entries, "OPS/book.opf", "</manifest>", `<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest>`)
	entries = append(entries, memEntry{"OPS/panel.png", epubImage(t)}, memEntry{"OPS/nav.xhtml", []byte(`<html><body><nav role="doc-toc"><ol><li><a href="panel.png">Cover</a></li></ol></nav></body></html>`)})
	b, err := Open(writeEPUB(t, entries, ".epub"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got, err := b.Chapters(); err != nil || !reflect.DeepEqual(got, []Chapter{{"Cover", 0, 0}}) {
		t.Fatalf("image outline = %#v, %v", got, err)
	}
}

func TestEPUBSVGSpineNavigation(t *testing.T) {
	entries := epubFixture()
	replaceEPUB(entries, "OPS/book.opf", `href="text/z.xhtml" media-type="application/xhtml+xml"`, `href="wrapper.svg" media-type="image/svg+xml"`)
	replaceEPUB(entries, "OPS/book.opf", "</manifest>", `<item id="panel" href="panel.png" media-type="image/png"/><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest>`)
	entries = append(entries,
		memEntry{"OPS/panel.png", epubImage(t)},
		memEntry{"OPS/wrapper.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><image id="cover" href="panel.png"/></svg>`)},
		memEntry{"OPS/nav.xhtml", []byte(`<html><body><nav role="doc-toc"><ol><li><a href="wrapper.svg#cover">Cover</a></li></ol></nav></body></html>`)},
	)
	b, err := Open(writeEPUB(t, entries, ".epub"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got, err := b.Chapters(); err != nil || !reflect.DeepEqual(got, []Chapter{{"Cover", 0, 0}}) {
		t.Fatalf("SVG outline = %#v, %v", got, err)
	}
}

func TestDOCXOutlineLevels(t *testing.T) {
	entries := docxFixture(t)
	entries[1].data = []byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:pPr><w:pStyle w:val="Custom"/></w:pPr><w:r><w:t>Inherited</w:t></w:r></w:p>
<w:p><w:pPr><w:outlineLvl w:val="8"/></w:pPr><w:r><w:t>Level nine</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Custom"/><w:outlineLvl w:val="9"/></w:pPr><w:r><w:t>Not a heading</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Cycle"/></w:pPr><w:r><w:t>Cyclic style</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Heading7"/></w:pPr><w:r><w:t>Built-in fallback</w:t></w:r></w:p>
</w:body></w:document>`)
	entries[2].data = []byte(`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:style w:styleId="Custom"><w:name w:val="Custom heading"/><w:basedOn w:val="Base"/></w:style><w:style w:styleId="Base"><w:pPr><w:outlineLvl w:val="0"/></w:pPr></w:style><w:style w:styleId="Cycle"><w:basedOn w:val="Cycle"/></w:style></w:styles>`)
	b, err := Open(writeEPUB(t, entries, ".docx"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	want := []Chapter{{"Inherited", 0, 0}, {"Level nine", 0, 8}, {"Built-in fallback", 0, 6}}
	if got, err := b.Chapters(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("outline = %#v, %v", got, err)
	}
}

func TestDOCAndFB2NestedChapters(t *testing.T) {
	doc, err := parseXML([]byte(`<book><chapter><title>First</title><para>Text</para><sect1><title>Nested</title><para>More text</para></sect1></chapter></book>`), maxDocumentTokens)
	if err != nil {
		t.Fatal(err)
	}
	docBook, err := layoutBook(newBook("book.doc", true), &epubPackage{}, &epubNode{name: xhtml("body"), children: []*epubNode{docbookNode(doc, 0)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fb2, err := openFB2([]byte(`<FictionBook><body><section><title><p>First</p></title><p>Text</p><section><title><p>Nested</p></title><p>More text</p></section></section></body></FictionBook>`), "book.fb2")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []*Book{docBook, fb2} {
		defer b.Close()
		want := []Chapter{{"First", 0, 0}, {"Nested", 1, 1}}
		if got, err := b.Chapters(); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s chapters = %#v, %v", b.Path, got, err)
		}
	}
}

func TestComicChapterFallbacks(t *testing.T) {
	pages := []entry{memEntry{name: "First/1.png"}, memEntry{name: "Last/2.png"}}
	for _, tc := range []struct {
		metadata string
		want     []Chapter
		err      bool
	}{
		{"", []Chapter{{"First", 0, 0}, {"Last", 1, 0}}, false},
		{`<ComicInfo><Pages><Page Image="1" Bookmark="Second"/><Page Image="0" Bookmark="First"/><Page Image="1" Bookmark="Same page"/><Page Image="99" Bookmark="Invalid"/></Pages></ComicInfo>`, []Chapter{{"First", 0, 0}, {"Second", 1, 0}, {"Same page", 1, 0}}, false},
		{`<ComicInfo>`, nil, true},
	} {
		b := &Book{pages: pages}
		if tc.metadata != "" {
			b.arc = testArchive{memEntry{"ComicInfo.xml", []byte(tc.metadata)}}
		}
		got, err := b.Chapters()
		b.Close()
		if (err != nil) != tc.err || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("chapters = %#v, %v", got, err)
		}
	}
	chapters := []Chapter{{"Later", 5, 0}, {"Earlier", 1, 0}, {"Nested", 5, 1}, {"Appendix", 3, 0}}
	for page, want := range map[int]int{0: -1, 1: 1, 4: 3, 5: 2, 99: 2} {
		if got := CurrentChapter(chapters, page); got != want {
			t.Fatalf("current at %d = %d, want %d", page, got, want)
		}
	}
}

func TestLongHeadingTargetsItsFirstPage(t *testing.T) {
	entries := epubFixture()
	replaceEPUB(entries, "OPS/text/z.xhtml", "First heading", strings.Repeat("Long heading ", 500))
	b, err := Open(writeEPUB(t, entries, ".epub"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	chapters, err := b.Chapters()
	if err != nil || b.Len() < 3 || len(chapters) < 1 || chapters[0].Page != 0 {
		t.Fatalf("long heading destination = %#v, %v", chapters, err)
	}
}
