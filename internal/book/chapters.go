package book

import (
	"encoding/xml"
	"path/filepath"
	"sort"
	"strings"
)

// Chapter marks the first page of a chapter.
type Chapter struct {
	Title string
	Page  int
}

type comicInfo struct {
	Pages struct {
		Page []struct {
			Image    int    `xml:"Image,attr"`
			Bookmark string `xml:"Bookmark,attr"`
		} `xml:"Page"`
	} `xml:"Pages"`
}

// Chapters returns EPUB headings/document titles, ComicInfo.xml bookmarks,
// or top-level archive folder markers. Metadata errors do not fall back to folders.
func (b *Book) Chapters() ([]Chapter, error) {
	b.chapOnce.Do(func() {
		b.chaps, b.chapErr = b.comicInfoChapters()
		if b.chaps == nil && b.chapErr == nil {
			b.chaps = b.folderChapters()
		}
	})
	return b.chaps, b.chapErr
}

func (b *Book) comicInfoChapters() ([]Chapter, error) {
	if b.arc == nil {
		return nil, nil
	}
	for _, f := range b.arc.Entries() {
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
			if p.Bookmark == "" || p.Image < 0 || p.Image >= len(b.pages) {
				continue
			}
			chs = append(chs, Chapter{Title: p.Bookmark, Page: p.Image})
		}
		sort.Slice(chs, func(i, j int) bool { return chs[i].Page < chs[j].Page })
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
