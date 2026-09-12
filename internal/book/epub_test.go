package book

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"golang.org/x/image/math/fixed"
)

func epubFixture() []testEntry {
	return []testEntry{
		{"mimetype", []byte("application/epub+zip")},
		{"META-INF/container.xml", []byte(`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="OPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)},
		{"OPS/book.opf", []byte(`<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>A Text Book</dc:title></metadata><manifest><item id="last" href="text/a.xhtml" media-type="application/xhtml+xml"/><item id="first" href="text/z.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="first"/><itemref idref="last" linear="no"/></spine></package>`)},
		{"OPS/text/a.xhtml", []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Notes</title></head><body><p>Last note.</p></body></html>`)},
		{"OPS/text/z.xhtml", []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><head><title>First document</title></head><body><h1>First heading</h1><p>Hello <em>reader</em> &amp; friends.</p><p>Second paragraph.</p></body></html>`)},
	}
}
func writeEPUB(t *testing.T, entries []testEntry, ext string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "book"+ext)
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for _, e := range entries {
		w, err := z.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}
func replaceEPUB(entries []testEntry, name, old, replacement string) {
	for i := range entries {
		if entries[i].name == name {
			entries[i].data = []byte(strings.ReplaceAll(string(entries[i].data), old, replacement))
		}
	}
}
func textEPUBPage(t *testing.T, b *Book, i int) string {
	t.Helper()
	p, ok := b.pages[i].(epubTextPage)
	if !ok {
		t.Fatalf("page %d is not text", i)
	}
	var lines []string
	for _, line := range p.lines {
		lines = append(lines, strings.TrimRight(line.text, " "))
	}
	return strings.Join(lines, "\n")
}
func TestOpenEPUBTextSpineAndPages(t *testing.T) {
	for _, version := range []string{"2.0", "3.0"} {
		t.Run(version, func(t *testing.T) {
			entries := epubFixture()
			replaceEPUB(entries, "OPS/book.opf", `version="3.0"`, `version="`+version+`"`)
			name := writeEPUB(t, entries, ".epub")
			b, err := Open(name)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			if b.Title != "A Text Book" || b.Len() != 2 || b.Path != name {
				t.Fatalf("book: %q %d %q", b.Title, b.Len(), b.Path)
			}
			if got := textEPUBPage(t, b, 0); got != "First heading\nHello reader & friends.\nSecond paragraph." {
				t.Fatalf("first text: %q", got)
			}
			if got := textEPUBPage(t, b, 1); got != "Last note." {
				t.Fatalf("notes: %q", got)
			}
			chapters, err := b.Chapters()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(chapters, []Chapter{{"First heading", 0}, {"Notes", 1}}) {
				t.Fatalf("chapters: %#v", chapters)
			}
			img, err := b.Page(0)
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds() != image.Rect(0, 0, epubPageWidth, epubPageHeight) {
				t.Fatal("wrong text dimensions")
			}
			ink := false
			for y := epubMargin; y < 150 && !ink; y++ {
				for x := epubMargin; x < epubPageWidth-epubMargin; x++ {
					r, _, _, _ := img.At(x, y).RGBA()
					if r < 0xffff {
						ink = true
						break
					}
				}
			}
			if !ink {
				t.Fatal("text raster is blank")
			}
			data, mime, err := b.PageBytes(0)
			if err != nil || mime != "image/png" {
				t.Fatalf("encoded page: %s %v", mime, err)
			}
			reopened, err := Open(name)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			again, _, err := reopened.PageBytes(0)
			if err != nil || !bytes.Equal(data, again) || reopened.Len() != b.Len() {
				t.Fatal("pagination or raster changed on reopen")
			}
			if b.IsTextPage(-1) || b.IsTextPage(b.Len()) {
				t.Fatal("out-of-range text page")
			}
			var wg sync.WaitGroup
			for range 3 {
				wg.Go(func() {
					if _, _, err := b.PageBytes(0); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
		})
	}
}
func epubImage(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 3, 5))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func TestOpenEPUBMixedImagesAndRelativeReferences(t *testing.T) {
	entries := epubFixture()
	replaceEPUB(entries, "OPS/book.opf", "</manifest>", `<item id="picture" href="images/panel%201" media-type="image/png"/></manifest>`)
	replaceEPUB(entries, "OPS/text/z.xhtml", "<body>", `<body><p>Before<img src="../images/panel%201#view"/>After</p><svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><image xlink:href="../images/panel%201"/></svg>`)
	entries = append(entries, testEntry{"OPS/images/panel 1", epubImage(t)}, testEntry{"unused.png", epubImage(t)})
	b, err := Open(writeEPUB(t, entries, ".zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Len() != 6 {
		t.Fatalf("pages = %d", b.Len())
	}
	for i := -1; i <= b.Len(); i++ {
		if b.CanInvertPage(i) != b.IsTextPage(i) {
			t.Fatalf("EPUB inversion eligibility differs from text page status at %d", i)
		}
	}
	if textEPUBPage(t, b, 0) != "Before" || textEPUBPage(t, b, 2) != "After" || !strings.HasPrefix(textEPUBPage(t, b, 4), "First heading") || textEPUBPage(t, b, 5) != "Last note." {
		t.Fatal("DOM/spine order changed")
	}
	for _, i := range []int{1, 3} {
		if b.IsTextPage(i) || b.pages[i].Name() != "OPS/images/panel 1" {
			t.Fatalf("image page %d", i)
		}
		img, err := b.Page(i)
		if err != nil || img.Bounds() != image.Rect(0, 0, 3, 5) {
			t.Fatalf("image %d: %v", i, err)
		}
		if _, mime, err := b.PageBytes(i); err != nil || mime != "image/png" {
			t.Fatalf("image %d MIME: %s, %v", i, mime, err)
		}
	}
}
func TestOpenEPUBImageOnlySpine(t *testing.T) {
	entries := epubFixture()
	replaceEPUB(entries, "OPS/book.opf", `href="text/z.xhtml" media-type="application/xhtml+xml"`, `href="picture" media-type="image/png"`)
	replaceEPUB(entries, "OPS/book.opf", `<itemref idref="last" linear="no"/>`, "")
	entries = append(entries, testEntry{"OPS/picture", epubImage(t)})
	b, err := Open(writeEPUB(t, entries, ".epub"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Len() != 1 || b.IsTextPage(0) || b.CanInvertPage(0) {
		t.Fatal("image-only EPUB must keep its original colors")
	}
	img, err := b.Page(0)
	if err != nil || img.Bounds() != image.Rect(0, 0, 3, 5) {
		t.Fatalf("spine image: %v", err)
	}
	if _, mime, err := b.PageBytes(0); err != nil || mime != "image/png" {
		t.Fatalf("spine image MIME: %s, %v", mime, err)
	}
}
func TestEPUBPaths(t *testing.T) {
	for ref, want := range map[string]string{"../images/a%20b.png#part": "OPS/images/a b.png", "#here": "OPS/text/ch.xhtml", "../../cover.png": "cover.png"} {
		got, err := epubPath("OPS/text/ch.xhtml", ref)
		if err != nil || got != want {
			t.Fatalf("%q = %q, %v", ref, got, err)
		}
	}
	for _, ref := range []string{"../../../escape", "%2e%2e/%2e%2e/%2e%2e/escape", "https://example.org/a", "//example.org/a", "/a", "%2fa", "a%5cb", "file:a", "a?x=1", "a?", "bad%zz", "a%00b", "C:/a"} {
		if _, err := epubPath("OPS/text/ch.xhtml", ref); err == nil {
			t.Errorf("accepted %q", ref)
		}
	}
}
func TestOpenEPUBRejectsInvalidContent(t *testing.T) {
	cases := []struct{ name, file, old, replacement, want string }{
		{"missing spine", "OPS/book.opf", `<itemref idref="first"/><itemref idref="last" linear="no"/>`, "", "spine"},
		{"missing id", "OPS/book.opf", `idref="first"`, `idref="absent"`, "missing manifest"},
		{"duplicate id", "OPS/book.opf", `id="last"`, `id="first"`, "duplicate manifest"},
		{"external manifest", "OPS/book.opf", `text/z.xhtml`, `https://example.org/ch.xhtml`, "local resource"},
		{"missing resource", "OPS/book.opf", `text/z.xhtml`, `text/missing.xhtml`, "missing manifest resource"},
		{"unsupported spine", "OPS/book.opf", `application/xhtml+xml`, `text/html`, "unsupported spine"},
		{"empty spine item", "OPS/text/z.xhtml", `<h1>First heading</h1><p>Hello <em>reader</em> &amp; friends.</p><p>Second paragraph.</p>`, "", "no readable content"},
		{"malformed XML", "OPS/text/z.xhtml", `</html>`, "", "XML"},
		{"multiple roots", "OPS/text/z.xhtml", `</html>`, `</html><extra/>`, "one complete root"},
		{"script", "OPS/text/z.xhtml", `<body>`, `<body><script>alert(1)</script>`, "unsupported"},
		{"remote image", "OPS/text/z.xhtml", `<body>`, `<body><img src="https://example.org/p.png"/>`, "local resource"},
		{"remote head link", "OPS/text/z.xhtml", `<head>`, `<head><link href="https://example.org/style.css"/>`, "local resource"},
		{"escaped image", "OPS/text/z.xhtml", `<body>`, `<body><img src="../../../p.png"/>`, "escapes"},
		{"unlisted image", "OPS/text/z.xhtml", `<body>`, `<body><img src="panel.png"/>`, "not in manifest"},
		{"svg drawing", "OPS/text/z.xhtml", `<body>`, `<body><svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`, "SVG drawing"},
		{"XML base", "OPS/text/z.xhtml", `<body>`, `<body xml:base="../">`, "xml:base"},
		{"all auxiliary", "OPS/book.opf", `<itemref idref="first"/>`, `<itemref idref="first" linear="no"/>`, "linear content"},
		{"wrong namespace", "OPS/book.opf", `http://www.idpf.org/2007/opf`, `urn:wrong`, "namespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entries := epubFixture()
			replaceEPUB(entries, tc.file, tc.old, tc.replacement)
			b, err := Open(writeEPUB(t, entries, ".epub"))
			if err == nil {
				b.Close()
				t.Fatal("accepted invalid EPUB")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v, want %q", err, tc.want)
			}
		})
	}
	for _, name := range []string{"../escape", "/absolute", "OPS/../alias", "OPS\\bad"} {
		t.Run(name, func(t *testing.T) {
			entries := append(epubFixture(), testEntry{name, []byte("x")})
			if b, err := Open(writeEPUB(t, entries, ".epub")); err == nil {
				b.Close()
				t.Fatal("accepted unsafe ZIP entry")
			}
		})
	}
	entries := append(epubFixture(), epubFixture()[2])
	if b, err := Open(writeEPUB(t, entries, ".epub")); err == nil {
		b.Close()
		t.Fatal("accepted duplicate ZIP entry")
	}
	if b, err := Open(writeEPUB(t, []testEntry{{"cover.png", epubImage(t)}}, ".epub")); err == nil {
		b.Close()
		t.Fatal("accepted missing container")
	}
}
func TestEPUBEncryptionMetadata(t *testing.T) {
	for _, required := range []bool{false, true} {
		entries := epubFixture()
		ref := "OPS/fonts/font.otf"
		if required {
			ref = "OPS/text/z.xhtml"
		}
		entries = append(entries, testEntry{"OPS/fonts/font.otf", []byte("obfuscated font")}, testEntry{"META-INF/encryption.xml", []byte(`<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><EncryptedData xmlns="http://www.w3.org/2001/04/xmlenc#"><EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/><CipherData><CipherReference URI="` + ref + `"/></CipherData></EncryptedData></encryption>`)})
		replaceEPUB(entries, "OPS/book.opf", "</manifest>", `<item id="font" href="fonts/font.otf" media-type="font/otf"/></manifest>`)
		b, err := Open(writeEPUB(t, entries, ".epub"))
		if required {
			if err == nil {
				b.Close()
				t.Fatal("accepted encrypted required text")
			}
			if !strings.Contains(err.Error(), "encrypted") {
				t.Fatal(err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			b.Close()
		}
	}
}
func TestEPUBBoundsAndPagination(t *testing.T) {
	for _, data := range []string{"<r>" + strings.Repeat("<n>", maxEPUBXMLDepth) + strings.Repeat("</n>", maxEPUBXMLDepth) + "</r>", "<r>" + strings.Repeat("<n/>", maxEPUBXMLTokens/2) + "</r>", `<!DOCTYPE r [<!ENTITY x "value">]><r>&x;</r>`} {
		if _, err := parseEPUBXML([]byte(data)); err == nil {
			t.Fatal("accepted XML outside bounds")
		}
	}
	entries := epubFixture()
	entries[1].data = bytes.Repeat([]byte(" "), maxMetadataBytes+1)
	if b, err := Open(writeEPUB(t, entries, ".epub")); err == nil {
		b.Close()
		t.Fatal("accepted oversized metadata")
	}
	e := &epubPackage{files: map[string]entry{"x": testEntry{"x", []byte("<r/>")}}, usedBytes: maxEPUBContentBytes - 3}
	if _, err := e.document("x", maxMetadataBytes); err == nil {
		t.Fatal("accepted aggregate content overflow")
	}
	b := &Book{}
	l, err := newEPUBLayout(b)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("word ", 1200) + strings.Repeat("X", 100)
	if err := l.text(text, false); err != nil {
		t.Fatal(err)
	}
	if err := l.flushPage(); err != nil {
		t.Fatal(err)
	}
	if b.Len() < 2 {
		t.Fatal("long text did not paginate")
	}
	var actual strings.Builder
	for i, p := range b.pages {
		if !b.IsTextPage(i) {
			t.Fatal("not text")
		}
		for _, line := range p.(epubTextPage).lines {
			if line.y > epubPageHeight-epubMargin || len(line.text) == 0 {
				t.Fatal("invalid text line")
			}
			if epubLineWidth(line) > fixed.I(epubPageWidth-2*epubMargin) {
				t.Fatal("long word failed to wrap")
			}
			actual.WriteString(strings.ReplaceAll(line.text, " ", ""))
		}
	}
	if actual.String() != strings.ReplaceAll(text, " ", "") {
		t.Fatal("pagination lost text")
	}
	if err := l.text("\U0010ffff", false); err != nil {
		t.Fatalf("missing glyph must render as notdef: %v", err)
	}
	if err := l.flushPage(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.PageBytes(b.Len() - 1); err != nil {
		t.Fatalf("notdef page raster: %v", err)
	}
	l.book.pages = make([]entry, maxEPUBPages)
	if err := l.addPage(epubTextPage{}); err == nil {
		t.Fatal("accepted page overflow")
	}
	if _, err := openEPUB(testArchive(make([]entry, maxEPUBEntries+1)), "book.epub"); err == nil {
		t.Fatal("accepted entry overflow")
	}
}

func TestEPUBReferencedSVGWrapperAndCycle(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		entries := epubFixture()
		replaceEPUB(entries, "OPS/book.opf", "</manifest>", `<item id="wrapper" href="images/wrapper.svg" media-type="image/svg+xml"/><item id="panel" href="images/panel.png" media-type="image/png"/></manifest>`)
		replaceEPUB(entries, "OPS/text/z.xhtml", "<body>", `<body><img src="../images/wrapper.svg"/>`)
		ref := "panel.png"
		if cycle {
			ref = "wrapper.svg"
		}
		entries = append(entries, testEntry{"OPS/images/wrapper.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><image href="` + ref + `"/></svg>`)}, testEntry{"OPS/images/panel.png", epubImage(t)})
		b, err := Open(writeEPUB(t, entries, ".epub"))
		if cycle {
			if err == nil {
				b.Close()
				t.Fatal("accepted SVG cycle")
			}
			if !strings.Contains(err.Error(), "cycle") {
				t.Fatal(err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			if b.pages[0].Name() != "OPS/images/panel.png" {
				t.Fatal("wrapper lost raster image")
			}
		}
	}
}

func TestEPUBLazyImagesAndDimensions(t *testing.T) {
	counted := new(generatedEntry)
	e := &epubPackage{files: map[string]entry{"panel": counted}}
	b := &Book{}
	l, err := newEPUBLayout(b)
	if err != nil {
		t.Fatal(err)
	}
	resources := map[string]string{"panel": "image/png"}
	for range 2 {
		if err := l.addImage(e, "panel", resources); err != nil {
			t.Fatalf("lazy image indexing: %v", err)
		}
	}
	if counted.read != 0 || b.Len() != 2 {
		t.Fatalf("indexing read %d bytes for %d pages", counted.read, b.Len())
	}
	e.encrypted = map[string]bool{"panel": true}
	if err := l.addImage(e, "panel", resources); err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("encrypted image: %v", err)
	}
	resources["missing"] = "image/png"
	if err := l.addImage(e, "missing", resources); err == nil {
		t.Fatal("accepted missing image")
	}
	resources["panel"] = "image/tiff"
	if err := l.addImage(e, "panel", resources); err == nil || !strings.Contains(err.Error(), "unsupported image") {
		t.Fatalf("unsupported raster media: %v", err)
	}
	e.files["corrupt"] = testEntry{"corrupt", []byte("not an image")}
	resources["corrupt"] = "image/png"
	if err := l.addImage(e, "corrupt", resources); err != nil {
		t.Fatalf("corrupt image indexing: %v", err)
	}
	if _, _, err := b.PageBytes(b.Len() - 1); err == nil {
		t.Fatal("served corrupt image")
	}
	if _, err := b.Page(b.Len() - 1); err == nil {
		t.Fatal("decoded corrupt image")
	}

	huge := make([]byte, 54)
	copy(huge, "BM")
	binary.LittleEndian.PutUint32(huge[10:], 54)
	binary.LittleEndian.PutUint32(huge[14:], 40)
	binary.LittleEndian.PutUint32(huge[18:], 50000)
	binary.LittleEndian.PutUint32(huge[22:], 50000)
	binary.LittleEndian.PutUint16(huge[26:], 1)
	binary.LittleEndian.PutUint16(huge[28:], 24)
	e.files["huge.bmp"] = testEntry{"huge.bmp", huge}
	resources["huge.bmp"] = "image/bmp"
	if err := l.addImage(e, "huge.bmp", resources); err != nil {
		t.Fatalf("oversized image indexing: %v", err)
	}
	if _, _, err := b.PageBytes(b.Len() - 1); err == nil || !strings.Contains(err.Error(), "decoded pixels") {
		t.Fatalf("oversized encoded dimensions: %v", err)
	}
	if _, err := b.Page(b.Len() - 1); err == nil || !strings.Contains(err.Error(), "decoded pixels") {
		t.Fatalf("oversized decoded dimensions: %v", err)
	}
}

func TestEPUBHeadingTargetsFirstTextPage(t *testing.T) {
	img := `<img src="../images/panel"/>`
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><image href="../images/panel"/></svg>`
	for _, tc := range []struct {
		name, heading string
		page          int
	}{
		{"image first", img + "First heading", 1},
		{"SVG first", svg + "First heading", 1},
		{"text first", "First heading" + img + " continued", 0},
		{"soft hyphen first", "\u00ad" + img + "First heading", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := epubFixture()
			replaceEPUB(entries, "OPS/book.opf", "</manifest>", `<item id="panel" href="images/panel" media-type="image/png"/></manifest>`)
			replaceEPUB(entries, "OPS/text/z.xhtml", `<h1>First heading</h1>`, "<h1>"+tc.heading+"</h1>"+img)
			replaceEPUB(entries, "OPS/text/z.xhtml", "</body>", "<h2>Next heading</h2></body>")
			entries = append(entries, testEntry{"OPS/images/panel", epubImage(t)})
			b, err := Open(writeEPUB(t, entries, ".epub"))
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			chapters, err := b.Chapters()
			if err != nil || len(chapters) != 3 {
				t.Fatalf("chapters: %#v, %v", chapters, err)
			}
			if chapters[0].Page != tc.page || !strings.HasPrefix(textEPUBPage(t, b, chapters[0].Page), "First heading") {
				t.Fatalf("first heading target: %#v", chapters[0])
			}
			if chapters[1] != (Chapter{"Next heading", b.Len() - 2}) || chapters[2] != (Chapter{"Notes", b.Len() - 1}) {
				t.Fatalf("chapter target leaked: %#v", chapters)
			}
			imagePage := 0
			if tc.page == 0 {
				imagePage = 1
			}
			if b.pages[imagePage].Name() != "OPS/images/panel" {
				t.Fatal("heading image order changed")
			}
		})
	}
}

func TestEPUBBodyStylesListsAndPre(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("wrap ", 30)) + " end"
	entries := epubFixture()
	replaceEPUB(entries, "OPS/text/z.xhtml", "<p>Second paragraph.</p>", "<p><i>Thought</i></p><h3>Sub</h3><ul><li>one</li><li><p>two</p></li></ul><pre>\nline one\n\tindented\n\nline two\n</pre><p><b>Loud</b></p><p>"+long+"</p>")
	b, err := Open(writeEPUB(t, entries, ".epub"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Len() != 2 {
		t.Fatalf("h3 or lists changed pagination: %d pages", b.Len())
	}
	want := []string{"First heading", "Hello reader & friends.", "Thought", "Sub", "\u2022 one", "\u2022 two", "line one", "\u00a0\u00a0\u00a0\u00a0indented", "\u00a0", "line two", "Loud", long}
	if got := b.PageText(0); !reflect.DeepEqual(got, want) {
		t.Fatalf("paragraphs: %#v", got)
	}
	if !b.HasText() || b.PageText(-1) != nil || !reflect.DeepEqual(b.PageText(1), []string{"Last note."}) {
		t.Fatal("page text lookup")
	}
	chapters, err := b.Chapters()
	if err != nil || !reflect.DeepEqual(chapters, []Chapter{{"First heading", 0}, {"Sub", 0}, {"Notes", 1}}) {
		t.Fatalf("chapters: %#v, %v", chapters, err)
	}
	bundled, err := epubBundledFonts()
	if err != nil {
		t.Fatal(err)
	}
	lines := b.pages[0].(epubTextPage).lines
	for i, style := range map[int]int{0: 1, 1: 0, 2: 2, 10: 1} {
		if lines[i].runs[0].font != bundled[style] {
			t.Fatalf("line %d %q uses the wrong style", i, lines[i].text)
		}
	}
	if _, _, err := b.PageBytes(0); err != nil {
		t.Fatal(err)
	}
}

func TestEPUBLinkLabelsAndLatinNormalization(t *testing.T) {
	entries := epubFixture()
	replaceEPUB(entries, "OPS/text/z.xhtml", "<body>", "<body><p><a href=\"https://publisher.example/legal\">Publisher</a> co\u00adoperate Cafe\u0301</p>")
	b, err := Open(writeEPUB(t, entries, ".epub"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if textEPUBPage(t, b, 0) != "Publisher cooperate Café" {
		t.Fatalf("link/normalization text: %q", textEPUBPage(t, b, 0))
	}
}
