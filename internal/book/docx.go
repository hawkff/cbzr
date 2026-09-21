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
	return layoutBook(b, pkg, docxNode(doc.child("body"), styles, images), resources)
}

// docxNode converts one WordprocessingML element to XHTML.
func docxNode(n *epubNode, styles, images map[string]string) *epubNode {
	if n.name.Local == "" {
		return &epubNode{text: n.text}
	}
	if docxDrop[n.name.Local] {
		return &epubNode{}
	}
	out := &epubNode{name: xhtml("span")}
	if local, ok := docxElements[n.name.Local]; ok {
		out.name.Local = local
	}
	switch n.name.Local {
	case "tab":
		return &epubNode{text: " "}
	case "AlternateContent":
		// Render one branch: the first choice carries the same content as the
		// fallback in richer markup, which the generic walk reads as well.
		if choice := n.child("Choice"); choice.name.Local != "" {
			return docxNode(choice, styles, images)
		}
		return docxNode(n.child("Fallback"), styles, images)
	case "drawing", "pict":
		name := images[docxEmbed(n)]
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
		out.children = append(out.children, docxNode(c, styles, images))
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

// docxEmbed finds the relationship of the first picture in a drawing.
func docxEmbed(n *epubNode) string {
	switch n.name.Local {
	case "blip":
		return n.attr("embed")
	case "imagedata":
		return n.attr("id")
	}
	for _, c := range n.children {
		if id := docxEmbed(c); id != "" {
			return id
		}
	}
	return ""
}
