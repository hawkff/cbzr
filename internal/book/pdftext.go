package book

import (
	"context"
	"math"
	"net/url"
	"strconv"
	"strings"
)

// pdfTextLayer reads the words of a page with pdftotext in the crop box
// space pdftoppm renders, and the link rectangles with pdftohtml. A block of
// pdftotext is a paragraph; its lines carry a trailing space until the last.
func pdfTextLayer(ctx context.Context, path string, page int) []textLine {
	n := strconv.Itoa(page)
	// pdftohtml parses the document again; let it run alongside pdftotext.
	linkXML := make(chan []byte, 1)
	go func() {
		out, _ := runToolContext(ctx, "pdftohtml", "-xml", "-i", "-q", "-zoom", "1", "-f", n, "-l", n, "-stdout", path)
		linkXML <- out
	}()
	out, err := runToolContext(ctx, "pdftotext", "-cropbox", "-bbox-layout", "-f", n, "-l", n, path, "-")
	if err != nil {
		return nil
	}
	doc, err := parseXML(out, maxDocumentTokens)
	if err != nil {
		return nil
	}
	pg := doc.child("body").child("doc").child("page")
	w, _ := strconv.ParseFloat(pg.attr("width"), 64)
	h, _ := strconv.ParseFloat(pg.attr("height"), 64)
	if w <= 0 || h <= 0 {
		return nil
	}
	links := pdfLinks(ctx, <-linkXML, path, page, w, h)
	var lines []textLine
	for _, flow := range pg.children {
		for _, block := range flow.children {
			var rows []*epubNode
			for _, row := range block.children {
				if row.name.Local == "line" {
					rows = append(rows, row)
				}
			}
			for r, row := range rows {
				line := textLine{top: math.Inf(1), bottom: math.Inf(-1), end: r == len(rows)-1}
				var text strings.Builder
				count := 0
				for _, word := range row.children {
					s := pdfWordText(word.allText())
					if word.name.Local != "word" || s == "" {
						continue
					}
					if count > 0 {
						text.WriteString(" ")
						count++
					}
					x0, _ := strconv.ParseFloat(word.attr("xMin"), 64)
					x1, _ := strconv.ParseFloat(word.attr("xMax"), 64)
					y0, _ := strconv.ParseFloat(word.attr("yMin"), 64)
					y1, _ := strconv.ParseFloat(word.attr("yMax"), 64)
					runes := len([]rune(s))
					line.boxes = append(line.boxes, textBox{x0: x0 / w, x1: x1 / w, index: count, count: runes})
					text.WriteString(s)
					count += runes
					line.top = min(line.top, y0/h)
					line.bottom = max(line.bottom, y1/h)
				}
				if len(line.boxes) == 0 {
					continue
				}
				if !line.end {
					text.WriteString(" ")
				}
				line.text = text.String()
				line.links = pdfLineLinks(line, links)
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// pdfWordText drops the invisible characters PDFs hide in words.
func pdfWordText(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u200b', '\u200c', '\u200d', '\ufeff', '\u00ad':
			return -1
		}
		return r
	}, s)
}

type pdfLink struct {
	box  Box
	href string
}

// pdfLineLinks joins adjacent words whose centers fall in the same link
// rectangle into one link range.
func pdfLineLinks(line textLine, links []pdfLink) []epubLink {
	var out []epubLink
	y := (line.top + line.bottom) / 2
	for _, box := range line.boxes {
		x := (box.x0 + box.x1) / 2
		href := ""
		for _, link := range links {
			if x >= link.box.X0 && x <= link.box.X1 && y >= link.box.Y0 && y <= link.box.Y1 {
				href = link.href
				break
			}
		}
		target, internal, ok := pdfLinkTarget(href)
		if !ok {
			continue
		}
		if last := len(out) - 1; last >= 0 && out[last].href == target && out[last].internal == internal && box.index == out[last].end+1 {
			out[last].end = box.index + box.count
			continue
		}
		out = append(out, epubLink{box.index, box.index + box.count, target, internal})
	}
	return out
}

// pdfLinkTarget keeps web and mail URLs and turns the page references
// pdftohtml writes as name.html#N into "#N".
func pdfLinkTarget(href string) (target string, internal, ok bool) {
	if href == "" {
		return "", false, false
	}
	if u, err := url.Parse(href); err == nil && u.IsAbs() {
		switch u.Scheme {
		case "http", "https", "mailto":
			return href, false, true
		}
		return "", false, false
	}
	// The document name precedes the page and may itself contain '#'.
	if i := strings.LastIndexByte(href, '#'); i >= 0 {
		if _, err := strconv.Atoi(href[i+1:]); err == nil {
			return href[i:], true, true
		}
	}
	return "", false, false
}

// pdfLinks returns the link rectangles of a page as crop box fractions, read
// from the pdftohtml XML of that page. pdftohtml works in the media box, so
// pdfinfo supplies the offset when the boxes differ in size.
func pdfLinks(ctx context.Context, out []byte, path string, page int, cropW, cropH float64) []pdfLink {
	doc, err := parseXML(out, maxDocumentTokens)
	if err != nil {
		return nil
	}
	pg := doc.child("page")
	mediaW, _ := strconv.ParseFloat(pg.attr("width"), 64)
	mediaH, _ := strconv.ParseFloat(pg.attr("height"), 64)
	dx, dy := 0.0, 0.0
	if math.Abs(mediaW-cropW) > 1 || math.Abs(mediaH-cropH) > 1 {
		dx, dy = pdfCropOffset(ctx, path, page)
	}
	var links []pdfLink
	var hrefs func(*epubNode) []string
	hrefs = func(n *epubNode) []string {
		var found []string
		if n.name.Local == "a" && n.attr("href") != "" {
			found = append(found, n.attr("href"))
		}
		for _, c := range n.children {
			found = append(found, hrefs(c)...)
		}
		return found
	}
	for _, t := range pg.children {
		if t.name.Local != "text" {
			continue
		}
		left, _ := strconv.ParseFloat(t.attr("left"), 64)
		top, _ := strconv.ParseFloat(t.attr("top"), 64)
		width, _ := strconv.ParseFloat(t.attr("width"), 64)
		height, _ := strconv.ParseFloat(t.attr("height"), 64)
		box := Box{(left - dx) / cropW, (top - dy) / cropH, (left + width - dx) / cropW, (top + height - dy) / cropH}
		for _, href := range hrefs(t) {
			links = append(links, pdfLink{box, href})
		}
	}
	return links
}

// pdfCropOffset returns how far the crop box origin sits from the media box
// origin in display space: left and top, in points.
func pdfCropOffset(ctx context.Context, path string, page int) (dx, dy float64) {
	n := strconv.Itoa(page)
	out, err := runToolContext(ctx, "pdfinfo", "-f", n, "-l", n, "-box", path)
	if err != nil {
		return 0, 0
	}
	boxes := map[string][4]float64{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 7 || fields[0] != "Page" {
			continue
		}
		var box [4]float64
		for i := range box {
			if box[i], err = strconv.ParseFloat(fields[3+i], 64); err != nil {
				break
			}
		}
		if err == nil {
			boxes[fields[2]] = box
		}
	}
	media, crop := boxes["MediaBox:"], boxes["CropBox:"]
	if media == [4]float64{} || crop == [4]float64{} {
		return 0, 0
	}
	return crop[0] - media[0], media[3] - crop[3]
}
