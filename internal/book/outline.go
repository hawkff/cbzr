package book

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"text/scanner"
)

func (p toolPage) chapters(ctx context.Context, pages int) ([]Chapter, error) {
	if !p.djvu {
		// Poppler includes the complete outline even when only one page is converted.
		out, err := runToolLimited(ctx, maxEPUBDocumentBytes, "pdftohtml", "-xml", "-i", "-q", "-enc", "UTF-8", "-f", "1", "-l", "1", "-stdout", p.path)
		if err != nil {
			return nil, err
		}
		return pdfOutline(out, pages)
	}
	out, err := runToolLimited(ctx, maxMetadataBytes, "djvused", "-u", "-e", "print-outline", p.path)
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil, err
	}
	listing, err := runToolLimited(ctx, maxEPUBDocumentBytes, "djvused", "-u", "-e", "ls", p.path)
	if err != nil {
		return nil, err
	}
	return djvuOutline(out, pages, djvuPageNames(string(listing)))
}

func pdfOutline(data []byte, pages int) ([]Chapter, error) {
	doc, err := parseEPUBXML(data)
	if err != nil {
		return nil, err
	}
	if doc.name.Local != "pdf2xml" {
		return nil, fmt.Errorf("pdftohtml returned no PDF metadata")
	}
	var walk func(*epubNode, int) []Chapter
	walk = func(n *epubNode, depth int) []Chapter {
		var chapters []Chapter
		parent := -1
		for _, c := range n.children {
			switch c.name.Local {
			case "item":
				parent = -1
				page, err := strconv.Atoi(c.attr("page"))
				if title := c.allText(); title != "" && (c.attr("page") == "" || err == nil && page > 0 && page <= pages) {
					parent = len(chapters)
					chapters = append(chapters, Chapter{Title: title, Page: page - 1, Depth: depth})
				}
			case "outline":
				children := walk(c, depth+1)
				if parent >= 0 && chapters[parent].Page < 0 && len(children) > 0 {
					chapters[parent].Page = children[0].Page
				}
				chapters = append(chapters, children...)
			}
		}
		valid := chapters[:0]
		for _, c := range chapters {
			if c.Page >= 0 {
				valid = append(valid, c)
			}
		}
		return valid
	}
	return walk(doc.child("outline"), 0), nil
}

var djvuPageLine = regexp.MustCompile(`(?m)^\s*(\d+) P\s+\d+  (.*)$`)

func djvuPageNames(listing string) map[string]int {
	names := map[string]int{}
	for _, match := range djvuPageLine.FindAllStringSubmatch(listing, -1) {
		page, err := strconv.Atoi(match[1])
		if err != nil || page < 1 {
			continue
		}
		id, title, _ := strings.Cut(strings.TrimSuffix(match[2], "\r"), " T=")
		id, saved, _ := strings.Cut(id, " F=")
		for _, name := range []string{id, saved, title} {
			if name != "" {
				names[name] = page - 1
			}
		}
	}
	return names
}

func djvuOutline(data []byte, pages int, names map[string]int) ([]Chapter, error) {
	var s scanner.Scanner
	s.Init(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	s.Mode = scanner.ScanIdents | scanner.ScanStrings
	var parseErr error
	s.Error = func(_ *scanner.Scanner, message string) { parseErr = fmt.Errorf("DJVU outline: %s", message) }
	readString := func() string {
		if s.Scan() != scanner.String {
			parseErr = fmt.Errorf("DJVU outline needs a quoted title and destination")
			return ""
		}
		value, err := strconv.Unquote(s.TokenText())
		if err != nil {
			parseErr = fmt.Errorf("DJVU outline: %w", err)
		}
		return value
	}
	if s.Scan() != '(' || s.Scan() != scanner.Ident || s.TokenText() != "bookmarks" {
		return nil, fmt.Errorf("DJVU outline lacks bookmarks")
	}
	count := 0
	var walk func(int) []Chapter
	walk = func(depth int) []Chapter {
		var chapters []Chapter
		for parseErr == nil {
			token := s.Scan()
			if token == ')' {
				return chapters
			}
			count++
			if token != '(' || depth >= maxEPUBXMLDepth || count > maxEPUBEntries {
				parseErr = fmt.Errorf("DJVU outline is malformed or exceeds its size limit")
				break
			}
			title, ref := readString(), readString()
			page := djvuDestination(ref, names)
			children := walk(depth + 1)
			if ref == "" && len(children) > 0 {
				page = children[0].Page
			}
			if title = strings.Join(strings.Fields(title), " "); title != "" && page >= 0 && page < pages {
				chapters = append(chapters, Chapter{Title: title, Page: page, Depth: depth})
			}
			chapters = append(chapters, children...)
		}
		return chapters
	}
	chapters := walk(0)
	if parseErr != nil {
		return nil, parseErr
	}
	if s.Scan() != scanner.EOF {
		return nil, fmt.Errorf("DJVU outline has trailing data")
	}
	return chapters, parseErr
}

func djvuDestination(ref string, names map[string]int) int {
	var name string
	var err error
	switch {
	case strings.HasPrefix(ref, "#"):
		name, err = url.PathUnescape(ref[1:])
	case strings.HasPrefix(ref, "?"):
		var query url.Values
		query, err = url.ParseQuery(ref[1:])
		name = query.Get("page")
	default:
		return -1
	}
	if err != nil || name == "" || strings.HasPrefix(name, "+") || strings.HasPrefix(name, "-") {
		return -1
	}
	if page, err := strconv.Atoi(name); err == nil {
		if page > 0 {
			return page - 1
		}
		return -1
	}
	if page, ok := names[name]; ok {
		return page
	}
	return -1
}
