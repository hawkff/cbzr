package book

import (
	"fmt"
	"slices"
	"strings"
)

// epubChapters prefers EPUB 3 navigation, then the EPUB 2 NCX, then layout headings.
func (b *Book) epubChapters(e *epubPackage, opf *epubNode, manifest map[string]epubItem) {
	var sources []epubItem
	for _, item := range opf.child("manifest").children {
		if item.name.Local == "item" && slices.Contains(strings.Fields(item.attr("properties")), "nav") {
			sources = append(sources, manifest[item.attr("id")])
		}
	}
	if ncx, ok := manifest[opf.child("spine").attr("toc")]; ok {
		sources = append(sources, ncx)
	}
	var firstErr error
	for _, source := range sources {
		doc, err := e.document(source.name, maxMetadataBytes)
		var root *epubNode
		ncx := source.media == "application/x-dtbncx+xml"
		if err == nil {
			if ncx {
				root = doc.child("navMap")
			} else {
				root = epubTOCNav(doc)
			}
			if root == nil || root.name.Local == "" {
				err = fmt.Errorf("EPUB %s has no table of contents", source.name)
			}
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if chapters := b.epubOutline(root, source.name, 0, ncx); len(chapters) > 0 {
			b.chaps = chapters
			return
		}
	}
	if len(b.chaps) == 0 {
		b.chapErr = firstErr
	}
}

func epubTOCNav(n *epubNode) *epubNode {
	if n.name.Local == "nav" {
		for _, a := range n.attrs {
			if a.Name.Space == "http://www.idpf.org/2007/ops" && a.Name.Local == "type" && slices.Contains(strings.Fields(a.Value), "toc") || a.Name.Local == "role" && slices.Contains(strings.Fields(a.Value), "doc-toc") {
				return n
			}
		}
	}
	for _, c := range n.children {
		if nav := epubTOCNav(c); nav != nil {
			return nav
		}
	}
	return nil
}

func (b *Book) epubOutline(n *epubNode, name string, depth int, ncx bool) []Chapter {
	var chapters []Chapter
	for _, c := range n.children {
		if c.name.Local == "ol" && !ncx {
			chapters = append(chapters, b.epubOutline(c, name, depth, ncx)...)
			continue
		}
		if ncx && c.name.Local != "navPoint" || !ncx && c.name.Local != "li" {
			continue
		}
		label, href := c.child("a").allText(), c.child("a").attr("href")
		if ncx {
			label, href = c.child("navLabel").allText(), c.child("content").attr("src")
		} else if label == "" {
			label = c.child("span").allText()
		}
		children := b.epubOutline(c, name, depth+1, ncx)
		page, found := 0, false
		if target, internal, ok := epubLinkTarget(name, href); ok && internal {
			// An unknown fragment must not jump to the document's first page.
			page, found = b.anchors[target]
		} else if href == "" && len(children) > 0 {
			page, found = children[0].Page, true
		}
		if label != "" && found && page >= 0 && page < b.Len() {
			chapters = append(chapters, Chapter{Title: label, Page: page, Depth: depth})
		}
		chapters = append(chapters, children...)
	}
	return chapters
}
