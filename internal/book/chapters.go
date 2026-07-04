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

// Chapters returns chapter markers from ComicInfo.xml bookmarks, falling
// back to top-level folders inside the archive. Nil when neither exists.
func (b *Book) Chapters() []Chapter {
	b.chapOnce.Do(func() {
		b.chaps = b.comicInfoChapters()
		if b.chaps == nil {
			b.chaps = b.folderChapters()
		}
	})
	return b.chaps
}

func (b *Book) comicInfoChapters() []Chapter {
	if b.rc == nil {
		return nil
	}
	for _, f := range b.rc.File {
		if !strings.EqualFold(filepath.Base(f.Name), "comicinfo.xml") {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return nil
		}
		var ci comicInfo
		err = xml.NewDecoder(r).Decode(&ci)
		r.Close()
		if err != nil {
			return nil
		}
		var chs []Chapter
		for _, p := range ci.Pages.Page {
			if p.Bookmark == "" || p.Image < 0 || p.Image >= len(b.pages) {
				continue
			}
			chs = append(chs, Chapter{Title: p.Bookmark, Page: p.Image})
		}
		sort.Slice(chs, func(i, j int) bool { return chs[i].Page < chs[j].Page })
		return chs
	}
	return nil
}

func (b *Book) folderChapters() []Chapter {
	dirs := map[string]bool{}
	var chs []Chapter
	cur := "\x00"
	for i, pg := range b.pages {
		d := ""
		if k := strings.IndexByte(pg.Name, '/'); k >= 0 {
			d = pg.Name[:k]
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
