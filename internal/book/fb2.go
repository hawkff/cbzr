package book

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

const (
	maxDocumentBytes  = 32 << 20
	maxDocumentTokens = 2_000_000
)

// memEntry serves bytes decoded from a document, such as FB2 binaries.
type memEntry struct {
	name string
	data []byte
}

func (e memEntry) Name() string                 { return e.name }
func (e memEntry) Open() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(e.data)), nil }

func xhtml(local string) xml.Name {
	return xml.Name{Space: "http://www.w3.org/1999/xhtml", Local: local}
}

func imageNode(name string) *epubNode {
	return &epubNode{name: xhtml("img"), attrs: []xml.Attr{{Name: xml.Name{Local: "src"}, Value: name}}}
}

// layoutBook paginates one XHTML body with the EPUB layout.
func layoutBook(b *Book, pkg *epubPackage, body *epubNode, resources map[string]string) (*Book, error) {
	l, err := newEPUBLayout(b)
	if err != nil {
		return nil, err
	}
	html := &epubNode{name: xhtml("html"), children: []*epubNode{body}}
	if err := l.document(pkg, "", html, resources); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(b.Path), err)
	}
	if err := l.flushPage(); err != nil {
		return nil, err
	}
	if b.Len() == 0 {
		return nil, fmt.Errorf("%s: no readable content", filepath.Base(b.Path))
	}
	// Chapters come from the headings; skip the comic metadata fallback.
	b.chapOnce.Do(func() {})
	return b, nil
}

// fb2Elements maps FictionBook elements to the XHTML elements the layout knows.
var fb2Elements = map[string]string{
	"p": "p", "v": "p", "text-author": "p", "date": "p",
	"body": "div", "section": "div", "poem": "div", "stanza": "div", "annotation": "div", "coverpage": "div", "table": "div",
	"epigraph": "blockquote", "cite": "blockquote",
	"emphasis": "em", "strong": "strong", "empty-line": "br",
	"tr": "tr", "th": "th", "td": "td",
}

func openFB2File(path string) (*Book, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := readBounded(f, maxDocumentBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return openFB2(data, path)
}

// openFB2 lays a FictionBook out through the XHTML layout: elements map to
// XHTML, binaries become the image manifest and every body joins one document.
func openFB2(data []byte, path string) (*Book, error) {
	doc, err := parseXML(data, maxDocumentTokens)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if doc.name.Local != "FictionBook" {
		return nil, fmt.Errorf("%s: not a FictionBook document", filepath.Base(path))
	}
	b := newBook(path, true)
	info := doc.child("description").child("title-info")
	if title := info.child("book-title").allText(); title != "" {
		b.Title = title
	}
	pkg := &epubPackage{files: map[string]entry{}}
	resources := map[string]string{}
	images := map[string]string{} // binary id -> package name
	for _, n := range doc.children {
		if n.name.Local != "binary" {
			continue
		}
		media := strings.ToLower(n.attr("content-type"))
		if media == "image/jpg" {
			media = "image/jpeg"
		}
		payload, err := base64.StdEncoding.DecodeString(strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, n.allText()))
		if n.attr("id") == "" || err != nil || !imageExts["."+strings.TrimPrefix(media, "image/")] || !strings.HasPrefix(media, "image/") {
			continue // references to a broken or non-raster binary drop with it
		}
		name := "image-" + strconv.Itoa(len(images))
		pkg.files[name] = memEntry{name, payload}
		resources[name] = media
		images[n.attr("id")] = name
	}
	body := &epubNode{name: xhtml("body")}
	body.children = append(body.children, fb2Node(info.child("coverpage"), "title-info", images, false))
	for _, n := range doc.children {
		if n.name.Local == "body" {
			body.children = append(body.children, fb2Node(n, "FictionBook", images, n.attr("name") != ""))
		}
	}
	return layoutBook(b, pkg, body, resources)
}

// fb2Node converts one FictionBook element to XHTML. Section and body titles
// become page-breaking headings; other titles and subtitles stay bold text.
func fb2Node(n *epubNode, parent string, images map[string]string, notes bool) *epubNode {
	if n.name.Local == "" {
		return &epubNode{text: n.text}
	}
	out := &epubNode{name: xhtml("span")}
	if local, ok := fb2Elements[n.name.Local]; ok {
		out.name.Local = local
	}
	switch n.name.Local {
	case "image":
		name := images[strings.TrimPrefix(n.attr("href"), "#")]
		if name == "" {
			return &epubNode{}
		}
		return imageNode(name)
	case "title", "subtitle":
		out.name.Local = "p"
		if n.name.Local == "title" && !notes && (parent == "section" || parent == "body") {
			out.name.Local = "h2"
			break
		}
		bold := &epubNode{name: xhtml("b")}
		for _, c := range n.children {
			bold.children = append(bold.children, fb2Node(c, n.name.Local, images, notes))
		}
		out.children = []*epubNode{bold}
		return out
	}
	for _, c := range n.children {
		out.children = append(out.children, fb2Node(c, n.name.Local, images, notes))
	}
	return out
}
