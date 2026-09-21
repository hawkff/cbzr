package book

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// docxDrop lists elements whose text must not render: field codes and
// tracked deletions and moves.
var docxDrop = map[string]bool{"instrText": true, "delText": true, "del": true, "moveFrom": true}

var docxElements = map[string]string{"body": "body", "p": "p", "tbl": "div", "tr": "tr", "tc": "td", "br": "br", "cr": "br"}

// openDOCX lays word/document.xml out through the XHTML layout. Heading
// levels come from the style names and pictures from the relationships.
func openDOCX(arc archive, path string) (*Book, error) {
	files := map[string]entry{}
	for _, e := range arc.Entries() {
		files[e.Name()] = e
	}
	data, err := readEntry(files["word/document.xml"], maxDocumentBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	doc, err := parseXML(data, maxDocumentTokens)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	styles := map[string]string{} // style id -> built-in name
	if data, err := readEntry(files["word/styles.xml"], maxDocumentBytes); err == nil {
		if sheet, err := parseXML(data, maxDocumentTokens); err == nil {
			for _, s := range sheet.children {
				if s.name.Local == "style" {
					styles[s.attr("styleId")] = strings.ToLower(s.child("name").attr("val"))
				}
			}
		}
	}
	pkg := &epubPackage{files: map[string]entry{}}
	resources := map[string]string{}
	images := map[string]string{} // relationship id -> package name
	if data, err := readEntry(files["word/_rels/document.xml.rels"], maxDocumentBytes); err == nil {
		if rels, err := parseXML(data, maxDocumentTokens); err == nil {
			for _, r := range rels.children {
				base, target := "word/document.xml", r.attr("Target")
				if strings.HasPrefix(target, "/") {
					base, target = "", target[1:]
				}
				name, err := epubPath(base, target)
				if r.name.Local != "Relationship" || r.attr("TargetMode") == "External" || err != nil || files[name] == nil || !IsImagePath(name) {
					continue
				}
				id := "image-" + strconv.Itoa(len(images))
				pkg.files[id] = files[name]
				resources[id] = imageMedia(name)
				images[r.attr("Id")] = id
			}
		}
	}
	b := newBook(path, true)
	b.arc = arc
	return layoutBook(b, pkg, docxNode(doc.child("body"), styles, images, []*epubNode{doc}), resources)
}

// docxNode converts one WordprocessingML element to XHTML.
func docxNode(n *epubNode, styles, images map[string]string, ancestors []*epubNode) *epubNode {
	if n.name.Local == "" {
		return &epubNode{text: n.text}
	}
	if docxDrop[n.name.Local] {
		return &epubNode{}
	}
	ancestors = append(ancestors, n)
	out := &epubNode{name: xhtml("span")}
	if local, ok := docxElements[n.name.Local]; ok {
		out.name.Local = local
	}
	switch n.name.Local {
	case "tab":
		return &epubNode{text: " "}
	case "AlternateContent":
		return docxNode(docxAlternate(n, ancestors), styles, images, ancestors)
	case "drawing", "pict":
		name := images[docxEmbed(n, ancestors)]
		if name == "" {
			return &epubNode{}
		}
		return imageNode(name)
	case "p":
		props := n.child("pPr")
		style := styles[props.child("pStyle").attr("val")]
		level, heading := strings.CutPrefix(style, "heading ")
		switch {
		case style == "title":
			out.name.Local = "h1"
		case heading && len(level) == 1 && level[0] >= '1' && level[0] <= '6':
			out.name.Local = "h" + level
		case props.child("numPr").name.Local != "":
			out.name.Local = "li"
		}
	}
	for _, c := range n.children {
		if c.name.Local == "" && n.name.Local != "t" {
			continue // XML indentation is not document text.
		}
		out.children = append(out.children, docxNode(c, styles, images, ancestors))
	}
	if n.name.Local == "r" {
		props := n.child("rPr")
		for _, style := range []string{"i", "b"} {
			if docxOn(props.child(style)) {
				out = &epubNode{name: xhtml(style), children: []*epubNode{out}}
			}
		}
	}
	return out
}

// docxOn reports whether a run property such as w:b or w:i is switched on.
func docxOn(n *epubNode) bool {
	switch n.attr("val") {
	case "0", "false", "off":
		return false
	}
	return n.name.Local != ""
}

// docxAlternate selects the first choice whose required namespaces we read.
func docxAlternate(n *epubNode, ancestors []*epubNode) *epubNode {
	for _, c := range n.children {
		if c.name.Local != "Choice" {
			continue
		}
		requires := strings.Fields(c.attr("Requires"))
		supported := len(requires) > 0
		for _, prefix := range requires {
			switch docxNamespace(append(ancestors, c), prefix) {
			case "http://schemas.openxmlformats.org/wordprocessingml/2006/main",
				"http://purl.oclc.org/ooxml/wordprocessingml/main",
				"http://schemas.openxmlformats.org/drawingml/2006/main",
				"http://purl.oclc.org/ooxml/drawingml/main",
				"http://schemas.openxmlformats.org/drawingml/2006/picture",
				"http://purl.oclc.org/ooxml/drawingml/picture":
			default:
				supported = false
			}
		}
		if supported {
			return c
		}
	}
	return n.child("Fallback")
}

func docxNamespace(ancestors []*epubNode, prefix string) string {
	for i := len(ancestors) - 1; i >= 0; i-- {
		for _, a := range ancestors[i].attrs {
			if a.Name.Space == "xmlns" && a.Name.Local == prefix {
				return a.Value
			}
		}
	}
	return ""
}

// docxEmbed finds the relationship of the first picture in a selected branch.
func docxEmbed(n *epubNode, ancestors []*epubNode) string {
	switch n.name.Local {
	case "AlternateContent":
		branch := docxAlternate(n, ancestors)
		return docxEmbed(branch, append(ancestors, branch))
	case "blip":
		return n.attr("embed")
	case "imagedata":
		return n.attr("id")
	}
	for _, c := range n.children {
		if id := docxEmbed(c, append(ancestors, c)); id != "" {
			return id
		}
	}
	return ""
}
