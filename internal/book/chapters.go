package book

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Chapter marks the first page of a chapter.
type Chapter struct {
	Title string
	Page  int // zero-based
	Depth int // zero-based outline level
}

type comicInfo struct {
	Pages struct {
		Page []struct {
			Image    int    `xml:"Image,attr"`
			Bookmark string `xml:"Bookmark,attr"`
		} `xml:"Page"`
	} `xml:"Pages"`
}

// Chapters returns document outlines, headings, comic bookmarks or folder markers.
// PDF and DJVU extraction runs once and stops when the book closes.
func (b *Book) Chapters() ([]Chapter, error) {
	b.chapOnce.Do(func() {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			b.chapErr = os.ErrClosed
			return
		}
		ctx, arc := b.contextLocked(), b.arc
		b.mu.Unlock()
		if len(b.pages) > 0 {
			if p, ok := b.pages[0].(toolPage); ok {
				b.chaps, b.chapErr = p.chapters(ctx, b.Len())
				return
			}
		}
		b.chaps, b.chapErr = b.comicInfoChapters(arc)
		if b.chaps == nil && b.chapErr == nil {
			b.chaps = b.folderChapters()
		}
	})
	return b.chaps, b.chapErr
}

// CurrentChapter returns the nearest preceding destination, regardless of outline order.
// Later entries win ties, so a subsection takes precedence over its parent.
func CurrentChapter(chapters []Chapter, page int) int {
	current := -1
	for i, c := range chapters {
		if c.Page <= page && (current < 0 || c.Page >= chapters[current].Page) {
			current = i
		}
	}
	return current
}

func (b *Book) comicInfoChapters(arc archive) ([]Chapter, error) {
	if arc == nil {
		return nil, nil
	}
	for _, f := range arc.Entries() {
		if !strings.EqualFold(filepath.Base(f.Name()), "comicinfo.xml") {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := readBounded(r, maxMetadataBytes)
		r.Close()
		if err != nil {
			return nil, err
		}
		var ci comicInfo
		err = xml.Unmarshal(data, &ci)
		if err != nil {
			return nil, err
		}
		var chs []Chapter
		for _, p := range ci.Pages.Page {
			if strings.TrimSpace(p.Bookmark) == "" || p.Image < 0 || p.Image >= len(b.pages) {
				continue
			}
			chs = append(chs, Chapter{Title: p.Bookmark, Page: p.Image})
		}
		sort.SliceStable(chs, func(i, j int) bool { return chs[i].Page < chs[j].Page })
		return chs, nil
	}
	return nil, nil
}

func (b *Book) folderChapters() []Chapter {
	dirs := map[string]bool{}
	var chs []Chapter
	cur := "\x00"
	for i, pg := range b.pages {
		d := ""
		if k := strings.IndexByte(pg.Name(), '/'); k >= 0 {
			d = pg.Name()[:k]
		}
		dirs[d] = true
		if d != cur {
			cur = d
			title := d
			if title == "" {
				title = "(root)"
			}
			chs = append(chs, Chapter{Title: title, Page: i})
		}
	}
	if len(dirs) < 2 {
		return nil
	}
	return chs
}
