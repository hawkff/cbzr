package book

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxEPUBEntries       = 10000
	maxEPUBContentBytes  = 16 << 20
	maxEPUBDocumentBytes = 4 << 20
	maxEPUBXMLTokens     = 200000
	maxEPUBXMLDepth      = 128
	maxEPUBPages         = 10000
)

// epubNode keeps mixed XML text and elements in source order.
type epubNode struct {
	name     xml.Name
	attrs    []xml.Attr
	text     string
	children []*epubNode
}

func (n *epubNode) attr(name string) string {
	for _, a := range n.attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
func (n *epubNode) child(name string) *epubNode {
	for _, c := range n.children {
		if c.name.Local == name {
			return c
		}
	}
	return &epubNode{}
}
func (n *epubNode) allText() string {
	var b strings.Builder
	var walk func(*epubNode)
	walk = func(n *epubNode) {
		b.WriteString(n.text)
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func parseEPUBXML(data []byte) (*epubNode, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	// EPUB 2 XHTML uses named HTML entities without an internal DTD.
	d.Entity = xml.HTMLEntity
	root := &epubNode{}
	stack := []*epubNode{root}
	tokens := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("EPUB XML: %w", err)
		}
		tokens++
		if tokens > maxEPUBXMLTokens {
			return nil, fmt.Errorf("EPUB XML exceeds %d tokens", maxEPUBXMLTokens)
		}
		parent := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) > maxEPUBXMLDepth {
				return nil, fmt.Errorf("EPUB XML exceeds depth %d", maxEPUBXMLDepth)
			}
			for _, a := range t.Attr {
				if a.Name.Space == "http://www.w3.org/XML/1998/namespace" && a.Name.Local == "base" {
					return nil, fmt.Errorf("EPUB xml:base is unsupported")
				}
			}
			n := &epubNode{name: t.Name, attrs: t.Attr}
			parent.children = append(parent.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 1 {
				if strings.TrimSpace(string(t)) != "" {
					return nil, fmt.Errorf("EPUB XML text outside root")
				}
				continue
			}
			parent.children = append(parent.children, &epubNode{text: string(t)})
		case xml.Directive:
			if strings.Contains(string(t), "[") {
				return nil, fmt.Errorf("EPUB XML internal DTD is unsupported")
			}
		}
	}
	if len(stack) != 1 || len(root.children) != 1 {
		return nil, fmt.Errorf("EPUB XML needs one complete root")
	}
	return root.children[0], nil
}

func epubPath(base, ref string) (string, error) {
	u, err := url.Parse(ref)
	if err != nil || u.IsAbs() || u.Host != "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery {
		return "", fmt.Errorf("EPUB requires a local resource reference: %q", ref)
	}
	p := u.Path
	if strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00:") {
		return "", fmt.Errorf("EPUB unsafe resource path: %q", ref)
	}
	resolved := base
	if p != "" {
		resolved = path.Join(path.Dir(base), p)
	}
	if resolved == "." || resolved == "" || resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", fmt.Errorf("EPUB resource escapes archive: %q", ref)
	}
	return resolved, nil
}

type epubPackage struct {
	files     map[string]entry
	encrypted map[string]bool
	usedBytes int
}

func (e *epubPackage) read(name string, limit int64) ([]byte, error) {
	if e.encrypted[name] {
		return nil, fmt.Errorf("EPUB required resource is encrypted: %s", name)
	}
	f := e.files[name]
	if f == nil {
		return nil, fmt.Errorf("EPUB missing resource: %s", name)
	}
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return readBounded(r, limit)
}
func (e *epubPackage) document(name string, limit int64) (*epubNode, error) {
	data, err := e.read(name, min(limit, int64(maxEPUBContentBytes-e.usedBytes)))
	if err != nil {
		return nil, fmt.Errorf("EPUB %s: %w", name, err)
	}
	e.usedBytes += len(data)
	return parseEPUBXML(data)
}

type epubItem struct{ name, media string }

func openEPUB(arc archive, filename string) (*Book, error) {
	e := &epubPackage{files: map[string]entry{}, encrypted: map[string]bool{}}
	if len(arc.Entries()) > maxEPUBEntries {
		return nil, fmt.Errorf("EPUB exceeds %d archive entries", maxEPUBEntries)
	}
	for _, f := range arc.Entries() {
		name := f.Name()
		if hiddenEntry(name) {
			continue
		}
		if name == "" || path.Clean(name) != name || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00:") {
			return nil, fmt.Errorf("EPUB unsafe archive path: %q", name)
		}
		if e.files[name] != nil {
			return nil, fmt.Errorf("EPUB duplicate archive path: %s", name)
		}
		e.files[name] = f
	}
	if e.files["META-INF/encryption.xml"] != nil {
		enc, err := e.document("META-INF/encryption.xml", maxMetadataBytes)
		if err != nil {
			return nil, err
		}
		if enc.name.Local != "encryption" {
			return nil, fmt.Errorf("EPUB invalid encryption metadata")
		}
		var walk func(*epubNode) error
		walk = func(n *epubNode) error {
			if n.name.Local == "EncryptedData" {
				ref := n.child("CipherData").child("CipherReference").attr("URI")
				if ref == "" {
					return fmt.Errorf("EPUB encryption metadata lacks resource URI")
				}
				name, err := epubPath("", ref)
				if err != nil {
					return err
				}
				e.encrypted[name] = true
			}
			for _, c := range n.children {
				if err := walk(c); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(enc); err != nil {
			return nil, err
		}
	}
	container, err := e.document("META-INF/container.xml", maxMetadataBytes)
	if err != nil {
		return nil, err
	}
	if container.name.Local != "container" || container.name.Space != "urn:oasis:names:tc:opendocument:xmlns:container" {
		return nil, fmt.Errorf("EPUB invalid container namespace or root")
	}
	packagePath := ""
	for _, rf := range container.child("rootfiles").children {
		if rf.name.Local == "rootfile" && rf.attr("media-type") == "application/oebps-package+xml" {
			packagePath, err = epubPath("", rf.attr("full-path"))
			break
		}
	}
	if err != nil {
		return nil, err
	}
	if packagePath == "" {
		return nil, fmt.Errorf("EPUB has no package rootfile")
	}
	opf, err := e.document(packagePath, maxMetadataBytes)
	if err != nil {
		return nil, err
	}
	if opf.name.Local != "package" || opf.name.Space != "http://www.idpf.org/2007/opf" {
		return nil, fmt.Errorf("EPUB invalid package namespace or root")
	}
	manifest := map[string]epubItem{}
	resources := map[string]string{}
	for _, item := range opf.child("manifest").children {
		if item.name.Local != "item" {
			continue
		}
		id, href, media := item.attr("id"), item.attr("href"), item.attr("media-type")
		if id == "" || href == "" || media == "" {
			return nil, fmt.Errorf("EPUB manifest item lacks id, href or media-type")
		}
		name, err := epubPath(packagePath, href)
		if err != nil {
			return nil, err
		}
		if e.files[name] == nil {
			return nil, fmt.Errorf("EPUB missing manifest resource: %s", name)
		}
		if _, ok := manifest[id]; ok || resources[name] != "" {
			return nil, fmt.Errorf("EPUB duplicate manifest id or resource: %s", id)
		}
		manifest[id] = epubItem{name, media}
		resources[name] = media
	}
	b := &Book{Path: filename, Title: opf.child("metadata").child("title").allText(), arc: arc, epub: true, cache: make(map[int]image.Image)}
	if b.Title == "" {
		b.Title = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}
	layout, err := newEPUBLayout(b)
	if err != nil {
		return nil, err
	}
	defer layout.close()
	linear := false
	for _, ref := range opf.child("spine").children {
		if ref.name.Local != "itemref" {
			continue
		}
		item, ok := manifest[ref.attr("idref")]
		if !ok {
			return nil, fmt.Errorf("EPUB spine references missing manifest item: %s", ref.attr("idref"))
		}
		switch ref.attr("linear") {
		case "", "yes":
			linear = true
		case "no":
		default:
			return nil, fmt.Errorf("EPUB invalid spine linear value")
		}
		first := len(b.pages)
		switch item.media {
		case "application/xhtml+xml", "image/svg+xml":
			doc, err := e.document(item.name, maxEPUBDocumentBytes)
			if err != nil {
				return nil, err
			}
			if err := layout.document(e, item.name, doc, resources); err != nil {
				return nil, fmt.Errorf("EPUB %s: %w", item.name, err)
			}
		default:
			if !strings.HasPrefix(item.media, "image/") {
				return nil, fmt.Errorf("EPUB unsupported spine media type: %s", item.media)
			}
			if err := layout.addImage(e, item.name, resources); err != nil {
				return nil, err
			}
		}
		if err := layout.flushPage(); err != nil {
			return nil, err
		}
		if len(b.pages) == first {
			return nil, fmt.Errorf("EPUB spine item has no readable content: %s", item.name)
		}
	}
	if !linear || len(b.pages) == 0 {
		return nil, fmt.Errorf("EPUB needs a nonempty spine with linear content")
	}
	// Keep the EPUB chapters indexed with the spine; skip comic metadata fallback.
	b.chapOnce.Do(func() {})
	return b, nil
}
