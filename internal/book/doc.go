package book

import (
	"encoding/xml"
	"strconv"
)

func docbookNode(n *epubNode, level int) *epubNode {
	if n.name.Local == "" {
		return &epubNode{text: n.text}
	}
	out := &epubNode{name: xhtml("span")}
	if id := n.attr("id"); id != "" {
		out.attrs = append(out.attrs, xml.Attr{Name: xml.Name{Local: "id"}, Value: id})
	}
	switch n.name.Local {
	case "bookinfo", "articleinfo", "info":
		return &epubNode{}
	case "book", "article", "chapter", "preface", "appendix", "section", "sect1", "sect2", "sect3", "sect4", "sect5":
		out.name.Local = "div"
		if n.name.Local != "book" && n.name.Local != "article" {
			level++
		}
	case "title":
		out.name.Local = "h" + strconv.Itoa(max(1, min(level, 6)))
		out.attrs = append(out.attrs, xml.Attr{Name: xml.Name{Local: "data-cbzr-depth"}, Value: strconv.Itoa(max(0, level-1))})
	case "para", "simpara":
		out.name.Local = "p"
	case "literallayout", "programlisting":
		out.name.Local = "pre"
	case "emphasis":
		out.name.Local = "em"
		if n.attr("role") == "bold" {
			out.name.Local = "strong"
		}
	case "itemizedlist":
		out.name.Local = "ul"
	case "orderedlist":
		out.name.Local = "ol"
	case "listitem":
		out.name.Local = "li"
	case "row":
		out.name.Local = "tr"
	case "entry":
		out.name.Local = "td"
	case "ulink", "link":
		out.name.Local = "a"
		href := n.attr("url")
		if n.name.Local == "link" {
			href = "#" + n.attr("linkend")
		}
		out.attrs = append(out.attrs, xml.Attr{Name: xml.Name{Local: "href"}, Value: href})
	}
	for _, c := range n.children {
		if (n.name.Local == "book" || n.name.Local == "article") && c.name.Local == "title" {
			continue
		}
		out.children = append(out.children, docbookNode(c, level))
	}
	return out
}
