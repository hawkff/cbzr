package book

import (
	"reflect"
	"testing"
)

func docxFixture(t *testing.T) []memEntry {
	t.Helper()
	const w = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
	return []memEntry{
		{"[Content_Types].xml", []byte(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`)},
		{"word/document.xml", []byte(`<w:document ` + w + ` xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"><w:body>
<w:p><w:pPr><w:pStyle w:val="Ttulo1"/></w:pPr><w:r><w:t>Heading One</w:t></w:r></w:p>
<w:p><w:r><w:t xml:space="preserve">Plain </w:t></w:r><w:r><w:rPr><w:b/><w:i w:val="0"/></w:rPr><w:t>bold</w:t></w:r><w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText> PAGEREF _Toc1 </w:instrText></w:r><w:r><w:tab/><w:t>text</w:t></w:r></w:p>
<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>item</w:t></w:r></w:p>
<w:p><w:r><w:rPr><w:i/></w:rPr><w:t>Italic</w:t></w:r></w:p>
<w:p><w:r><w:drawing><a:blip r:embed="rId2"/></w:drawing></w:r></w:p>
<w:p><w:r><w:drawing><a:blip r:embed="rId9"/></w:drawing></w:r><w:r><w:t>After</w:t></w:r></w:p>
<w:p><w:r><mc:AlternateContent><mc:Choice><w:t>Choice</w:t></mc:Choice><mc:Fallback><w:t>Fallback</w:t></mc:Fallback></mc:AlternateContent></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Cell</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:sectPr/></w:body></w:document>`)},
		{"word/styles.xml", []byte(`<w:styles ` + w + `><w:style w:styleId="Ttulo1"><w:name w:val="heading 1"/></w:style><w:style w:styleId="Normal"><w:name w:val="Normal"/></w:style></w:styles>`)},
		{"word/_rels/document.xml.rels", []byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://example.org/" TargetMode="External"/></Relationships>`)},
		{"word/media/image1.png", epubImage(t)},
	}
}

func TestOpenDOCX(t *testing.T) {
	path := writeEPUB(t, docxFixture(t), ".docx")
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.Title != "book" || b.Len() != 3 {
		t.Fatalf("book: %q %d", b.Title, b.Len())
	}
	if got := b.PageText(0); !reflect.DeepEqual(got, []string{"Heading One", "Plain bold text", "\u2022 item", "Italic"}) {
		t.Fatalf("first page: %#v", got)
	}
	if b.IsTextPage(1) || b.CanInvertPage(1) || b.pages[1].Name() != "word/media/image1.png" {
		t.Fatal("picture page")
	}
	if img, err := b.Page(1); err != nil || img.Bounds().Dx() != 3 {
		t.Fatalf("picture: %v", err)
	}
	if got := b.PageText(2); !reflect.DeepEqual(got, []string{"After", "Choice", "Cell"}) {
		t.Fatalf("last page: %#v", got)
	}
	chapters, err := b.Chapters()
	if err != nil || !reflect.DeepEqual(chapters, []Chapter{{"Heading One", 0}}) {
		t.Fatalf("chapters: %#v, %v", chapters, err)
	}
	bundled, err := epubBundledFonts()
	if err != nil {
		t.Fatal(err)
	}
	lines := b.pages[0].(epubTextPage).lines
	for i, style := range map[int]int{0: 1, 1: 0, 3: 2} {
		if lines[i].runs[0].font != bundled[style] {
			t.Fatalf("line %d %q uses the wrong style", i, lines[i].text)
		}
	}
	renamed, err := Open(writeEPUB(t, docxFixture(t), ".epub"))
	if err != nil {
		t.Fatalf("DOCX contents must win over the extension: %v", err)
	}
	defer renamed.Close()
	if renamed.Len() != 3 {
		t.Fatalf("renamed DOCX pages = %d", renamed.Len())
	}
}
