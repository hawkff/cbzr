package book

import (
	"fmt"
	"slices"
	"strings"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/harfbuzz"
	"github.com/go-text/typesetting/segmenter"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"
	"golang.org/x/text/unicode/norm"
)

type epubRuby struct {
	start, end int
	reading    string
}

type epubParagraph struct {
	runes  []rune
	rubies []epubRuby
}

func (p *epubParagraph) WriteString(s string) {
	s = norm.NFC.String(strings.ReplaceAll(s, "\u00ad", ""))
	for _, r := range s {
		// Collapse HTML whitespace, not NBSP or other Unicode line-break controls.
		if strings.ContainsRune(" \t\r\n\f", r) {
			if len(p.runes) == 0 || p.runes[len(p.runes)-1] == ' ' {
				continue
			}
			r = ' '
		}
		p.runes = append(p.runes, r)
	}
}
func (p *epubParagraph) String() string { return string(p.runes) }

func (p *epubParagraph) ruby(n *epubNode) error {
	var base strings.Builder
	var inlineText func(*epubNode, *strings.Builder) error
	inlineText = func(n *epubNode, out *strings.Builder) error {
		if n.name.Local == "" {
			out.WriteString(n.text)
			return nil
		}
		if n.name.Space != "http://www.w3.org/1999/xhtml" {
			return fmt.Errorf("EPUB unsupported ruby namespace: %q", n.name.Space)
		}
		switch n.name.Local {
		case "rb", "rt", "rp", "span", "em", "strong", "b", "i", "a":
		default:
			return fmt.Errorf("EPUB unsupported ruby element: %q", n.name.Local)
		}
		for _, child := range n.children {
			if child.name.Local == "rt" || child.name.Local == "rp" {
				return fmt.Errorf("EPUB ruby needs direct rt/rp children")
			}
			if err := inlineText(child, out); err != nil {
				return err
			}
		}
		return nil
	}
	for _, child := range n.children {
		switch child.name.Local {
		case "rt":
			var reading strings.Builder
			if err := inlineText(child, &reading); err != nil {
				return err
			}
			start := len(p.runes)
			p.WriteString(base.String())
			base.Reset()
			var annotation epubParagraph
			annotation.WriteString(reading.String())
			text := strings.Trim(annotation.String(), " ")
			end := len(p.runes)
			for start < end && p.runes[start] == ' ' {
				start++
			}
			for end > start && p.runes[end-1] == ' ' {
				end--
			}
			if end == start || text == "" {
				return fmt.Errorf("EPUB ruby needs base text and a reading")
			}
			p.rubies = append(p.rubies, epubRuby{start, end, text})
		case "rp":
			var ignored strings.Builder
			if err := inlineText(child, &ignored); err != nil {
				return err
			}
		default:
			if err := inlineText(child, &base); err != nil {
				return err
			}
		}
	}
	p.WriteString(base.String())
	return nil
}

func (l *epubLayout) text(text string, heading bool) error {
	var p epubParagraph
	p.WriteString(text)
	return l.paragraph(p, heading)
}

// shape delegates script, bidi, font runs and cluster formation to go-text.
func (l *epubLayout) shape(text []rune, heading bool, size int) ([]shaping.Output, di.Direction, error) {
	faces := l.regular
	if heading {
		faces = l.bold
	}
	inputs := l.segmenter.Split(shaping.Input{Text: text, RunEnd: len(text), Size: fixed.I(size)}, epubFontMap{faces: faces, resolved: map[rune]*font.Face{}})
	inputs = epubClusterFaces(inputs, text, faces)
	outputs := make([]shaping.Output, 0, len(inputs))
	for _, input := range inputs {
		output := l.shaper.Shape(input)
		if err := epubShapedGlyphs(output, text); err != nil {
			original := err
			for _, face := range faces {
				if face == input.Face {
					continue
				}
				alternate := input
				alternate.Face = face
				candidate := l.shaper.Shape(alternate)
				if epubShapedGlyphs(candidate, text) == nil {
					output = candidate
					err = nil
					break
				}
			}
			if err != nil {
				return nil, 0, original
			}
		}
		outputs = append(outputs, output)
	}
	return outputs, outputs[0].Direction, nil
}

func epubShapedGlyphs(output shaping.Output, text []rune) error {
	outlines := map[font.GID]font.GlyphOutline{}
	for _, glyph := range output.Glyphs {
		if glyph.GlyphID == font.EmptyGlyph {
			continue
		}
		if glyph.GlyphID == 0 {
			return fmt.Errorf("EPUB font lacks glyph or usable outline for U+%04X; set CBZR_EPUB_FALLBACK_FONT to an outline font containing it", text[glyph.TextIndex()])
		}
		outline, cached := outlines[glyph.GlyphID]
		ok := true
		if !cached {
			outline, ok = output.Face.GlyphDataOutline(glyph.GlyphID)
			if ok {
				outlines[glyph.GlyphID] = outline
			}
		}
		invisible := epubInvisibleRune(text[glyph.TextIndex()])
		if space, hasSpace := output.Face.NominalGlyph(' '); hasSpace && glyph.GlyphID == space {
			for _, r := range text[glyph.TextIndex() : glyph.TextIndex()+glyph.RunesCount()] {
				invisible = invisible || harfbuzz.IsDefaultIgnorable(r)
			}
		}
		if !ok || len(outline.Segments) == 0 && !invisible {
			return fmt.Errorf("EPUB shaped glyph for U+%04X has no outline; set CBZR_EPUB_FALLBACK_FONT to an outline font containing it", text[glyph.TextIndex()])
		}
	}
	return nil
}

// Keep a grapheme on one face when font segmentation splits its combining marks.
func epubClusterFaces(inputs []shaping.Input, text []rune, faces []*font.Face) []shaping.Input {
	var seg segmenter.Segmenter
	seg.Init(text)
	iter := seg.GraphemeIterator()
	var result []shaping.Input
	index := 0
	for iter.Next() {
		cluster := iter.Grapheme()
		end := cluster.Offset + len(cluster.Text)
		for index < len(inputs) && inputs[index].RunEnd <= cluster.Offset {
			result = append(result, inputs[index])
			index++
		}
		if index >= len(inputs) || inputs[index].RunEnd >= end {
			continue
		}
		input := inputs[index]
		if input.RunStart < cluster.Offset {
			prefix := input
			prefix.RunEnd = cluster.Offset
			result = append(result, prefix)
		}
		input.RunStart, input.RunEnd = cluster.Offset, end
		for _, face := range faces {
			supported := true
			for _, r := range cluster.Text {
				if harfbuzz.IsDefaultIgnorable(r) {
					continue
				}
				gid, ok := face.NominalGlyph(r)
				if !ok || gid == 0 {
					supported = false
					break
				}
				if !epubRuneOutline(face, gid, r) {
					supported = false
					break
				}
			}
			if supported {
				input.Face = face
				break
			}
		}
		result = append(result, input)
		for index < len(inputs) && inputs[index].RunEnd <= end {
			index++
		}
		if index < len(inputs) && inputs[index].RunStart < end {
			inputs[index].RunStart = end
		}
	}
	return append(result, inputs[index:]...)
}

func freezeEPUBLine(line shaping.Line, text string) epubLine {
	line = slices.Clone(line)
	slices.SortFunc(line, func(a, b shaping.Output) int { return int(a.VisualIndex - b.VisualIndex) })
	result := epubLine{text: text}
	var x fixed.Int26_6
	for _, output := range line {
		f := output.Face.Font
		output.Face = nil
		output.Glyphs = slices.Clone(output.Glyphs)
		result.runs = append(result.runs, epubRun{Output: output, font: f, x: x})
		x += output.Advance
	}
	return result
}

func epubLineBounds(line epubLine) (ascent, descent int) {
	for _, run := range line.runs {
		ascent = max(ascent, run.LineBounds.Ascent.Ceil(), run.GlyphBounds.Ascent.Ceil())
		descent = max(descent, (-run.LineBounds.Descent).Ceil(), (-run.GlyphBounds.Descent).Ceil())
	}
	return
}

func epubLineWidth(line epubLine) fixed.Int26_6 {
	var width fixed.Int26_6
	for _, run := range line.runs {
		width = max(width, run.x+run.Advance)
	}
	return width
}

func epubLineFits(line epubLine) bool {
	for _, run := range line.runs {
		x := run.x
		for _, glyph := range run.Glyphs {
			left := x + glyph.XOffset + glyph.XBearing
			if left < -fixed.I(epubMargin) || left+glyph.Width > fixed.I(epubPageWidth-epubMargin) {
				return false
			}
			x += glyph.Advance
		}
	}
	return true
}

func (l *epubLayout) paragraph(p epubParagraph, heading bool) error {
	if len(p.runes) > 0 && p.runes[len(p.runes)-1] == ' ' {
		p.runes = p.runes[:len(p.runes)-1]
	}
	if len(p.runes) == 0 {
		return nil
	}
	size, leading := 32, 6
	if heading {
		size, leading = 40, 8
	}
	outputs, direction, err := l.shape(p.runes, heading, size)
	if err != nil {
		return err
	}
	var wrapper shaping.LineWrapper
	wrapper.Prepare(shaping.WrapConfig{Direction: direction}, p.runes, shaping.NewSliceIterator(outputs))
	start, rubyIndex := 0, 0
	for done := false; !done; {
		var wrapped shaping.WrappedLine
		wrapped, done = wrapper.WrapNextLine(epubPageWidth - 2*epubMargin)
		line := freezeEPUBLine(wrapped.Line, string(p.runes[start:wrapped.NextLine]))
		if epubLineWidth(line) > fixed.I(epubPageWidth-2*epubMargin) || !epubLineFits(line) {
			return fmt.Errorf("EPUB text cluster exceeds page width")
		}
		ascent, descent := epubLineBounds(line)
		annotationHeight := 0
		for rubyIndex < len(p.rubies) && p.rubies[rubyIndex].start < wrapped.NextLine {
			ruby := p.rubies[rubyIndex]
			rubyIndex++
			// A reading belongs above the first wrapped fragment of its base, once.
			left, right := fixed.I(epubPageWidth), fixed.Int26_6(0)
			for _, run := range line.runs {
				x := run.x
				for _, glyph := range run.Glyphs {
					if glyph.TextIndex() < ruby.end && glyph.TextIndex()+glyph.RunesCount() > ruby.start {
						left = min(left, x)
						right = max(right, x+glyph.Advance)
					}
					x += glyph.Advance
				}
			}
			if right <= left {
				return fmt.Errorf("EPUB ruby base has no visible width")
			}
			width := max(right-left, fixed.I(64))
			left = max(0, min(left-(width-(right-left))/2, fixed.I(epubPageWidth-2*epubMargin)-width))
			right = left + width
			annotation, height, err := l.annotation(ruby.reading, left, right, heading)
			if err != nil {
				return err
			}
			// Stack readings in separate bands so adjacent or bidi spans cannot overlap.
			for i := range annotation {
				annotation[i].y += annotationHeight
			}
			line.annotations = append(line.annotations, annotation...)
			annotationHeight += height
		}
		height := annotationHeight + ascent + descent + leading
		if height > epubPageHeight-2*epubMargin {
			return fmt.Errorf("EPUB text line and ruby exceed page height")
		}
		if l.y+height > epubPageHeight-epubMargin {
			if err := l.flushPage(); err != nil {
				return err
			}
		}
		for i := range line.annotations {
			line.annotations[i].y += l.y
		}
		line.y = l.y + annotationHeight + ascent
		l.y += height
		l.lines = append(l.lines, line)
		start = wrapped.NextLine
	}
	l.y += 18
	return nil
}

func (l *epubLayout) annotation(text string, left, right fixed.Int26_6, heading bool) ([]epubLine, int, error) {
	runes := []rune(text)
	outputs, direction, err := l.shape(runes, heading, 16)
	if err != nil {
		return nil, 0, err
	}
	var wrapper shaping.LineWrapper
	wrapper.Prepare(shaping.WrapConfig{Direction: direction}, runes, shaping.NewSliceIterator(outputs))
	var lines []epubLine
	height, start := 0, 0
	for done := false; !done; {
		var wrapped shaping.WrappedLine
		wrapped, done = wrapper.WrapNextLineF(right - left)
		line := freezeEPUBLine(wrapped.Line, string(runes[start:wrapped.NextLine]))
		width := epubLineWidth(line)
		if width > right-left {
			return nil, 0, fmt.Errorf("EPUB ruby reading cluster exceeds base width")
		}
		for i := range line.runs {
			line.runs[i].x += left + (right-left-width)/2
		}
		if !epubLineFits(line) {
			return nil, 0, fmt.Errorf("EPUB ruby reading exceeds page width")
		}
		ascent, descent := epubLineBounds(line)
		line.y = height + ascent
		height += ascent + descent + 2
		if height > epubPageHeight-2*epubMargin {
			return nil, 0, fmt.Errorf("EPUB ruby reading exceeds page height")
		}
		lines = append(lines, line)
		start = wrapped.NextLine
	}
	return lines, height + 2, nil
}
