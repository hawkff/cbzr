# cbzr

A terminal reader for CBZ/CBR comics and text or comic EPUBs, with single-page,
two-page spread, continuous webtoon, split-screen, and native macOS modes.
For EPUB text, use Kitty/Ghostty graphics, the browser, or the native reader.

```
cbzr book.cbz             one book
cbzr novel.epub           text or comic EPUB
cbzr one.cbz two.cbr      split screen
cbzr                      empty pane; press o for the file picker
cbzr -renderer=halfblock  force the fallback renderer
```

## Build

```
go build -o cbzr .
```

Building cbzr requires Go 1.26.4+. The native frontend requires macOS with cgo
and Xcode command-line tools. GitHub Actions checks that build on macOS.
Docker's cross-compiled Darwin binaries use the terminal frontend only.

OCR search also requires
[tesseract](https://github.com/tesseract-ocr/tesseract)
(`brew install tesseract` / `apt install tesseract-ocr`).

## Formats

- cbzr reads .cbz/.zip and .cbr/.rar archives. It detects the format from the
  file signature, so an archive still opens when its extension is wrong.
- cbzr sorts comic archive images in natural order (`p2` before `p10`).
  For EPUBs, it follows the package spine and document order.
- cbzr decodes JPEG, PNG, GIF, WebP, and BMP page images.
- cbzr limits decompressed page data to 64 MiB and image dimensions to
  32 million pixels. ComicInfo.xml must fit within 1 MiB.

### EPUB

cbzr reads a subset of EPUB 2/3: XHTML text and raster-image comics in ZIP
containers. It reads the first OPF package listed in `META-INF/container.xml`,
uses its title, and includes auxiliary (`linear="no"`) spine items in order.
It opens EPUBs with a different extension when they contain that container file.

Text uses bundled Go fonts on fixed 900×1200 image pages, with wrapped
paragraphs and bold headings. Inline and block images occupy separate pages
at their position in the text. Raster images keep their original dimensions.
For simple SVG wrappers, cbzr puts each referenced raster image on a separate
page and ignores SVG positioning and sizing. The chapter menu (`tab`) uses
headings, or the document title when it has no headings. It does not use EPUB
nav/NCX anchors.

Use `e` for the browser, `f` for native macOS, or Kitty/Ghostty with the Kitty
renderer to read text pages. The halfblock renderer shows guidance on text
pages and still displays comic images. Leave webtoon mode with `t` to step
past a text page in halfblock. Bookmarks, saved positions and screenshots use
the generated page numbers; text search still requires OCR.

cbzr ignores XHTML CSS, embedded fonts and inline emphasis styling. It reflows
whitespace and lists without preserving table geometry or fixed-layout
placement. cbzr displays hyperlink labels without navigation and does not
follow manifest fallback chains. Scripts, SVG drawings/transforms/style
attributes, audio/video, MathML and encrypted required resources produce
errors. Unused obfuscated fonts do not prevent reading. cbzr does not fetch
EPUB resources over the network; external image/content references and remote
stylesheet links produce errors. Internal DTDs and `xml:base` are unsupported.

cbzr keeps the bundled Go fonts for text and uses an installed fallback for
missing glyphs: Apple Symbols on macOS, Segoe UI Symbol on Windows, or DejaVu
Sans when available on Linux/BSD. Set `CBZR_EPUB_FALLBACK_FONT` to a TTF/OTF
file (up to 32 MiB) to choose another fallback. Font data stays fixed for the
process; restart cbzr after changing it. Switching fallback fonts can change
page numbers.

cbzr renders unshaped Latin, Greek and Cyrillic text, normalizes it to NFC,
and removes soft hyphens. Glyphs absent from both fonts, remaining combining
marks and scripts that need shaping still produce errors.

EPUB limits: 10,000 archive files and generated pages; 1 MiB per metadata file;
4 MiB per content document; 16 MiB of XML across opening; 128 XML nesting
levels and SVG resource hops; 200,000 XML tokens per document. The page byte
and pixel limits above also apply. cbzr lays out text while opening the book
and renders page images on demand. It checks raster-image bytes on page access;
corrupt or oversized images produce page errors rather than preventing opening.

## Rendering

cbzr probes the terminal and chooses a rendering backend.

- `kitty`: uses the Kitty graphics protocol with Unicode placeholders (`U=1`).
  cbzr sends images out of band and prints placeholder cells for Bubble Tea
  to repaint and join split panes. This backend runs in Kitty and Ghostty.
  Under tmux 3.3 or newer, cbzr enables passthrough and detects the outer
  terminal. It uses XTWINOPS replies for cell dimensions on Unix.
- `halfblock`: uses U+2580 with truecolor foreground and background pixels.
  Each cell displays two vertical pixels. Use this backend in a 24-bit color
  terminal with U+2580 support.

## Terminal keys

| key | action |
|---|---|
| `j` / `k` | next / previous page or spread; in webtoon mode, scroll one viewport (`2j` scrolls two) |
| `J` / `K` | next / previous page or spread; in webtoon mode, scroll half a viewport |
| `g` / `G` | first / last page (`42G` → page 42) |
| `f` | open the active book in the native macOS reader (fullscreen); `f` returns to the terminal |
| `z` | toggle terminal focus mode: hide titles and the status bar |
| `t` | toggle continuous webtoon mode with eased scrolling (single pane); wide pages shrink to fit, narrow pages keep their size |
| `w` | switch pane |
| `v` | toggle split (keeps active pane) |
| `s` | two-page spread: pages N and N+1 side by side (single pane) |
| `tab` | chapter menu (EPUB headings/titles, ComicInfo.xml bookmarks or archive folders) |
| `b` | toggle bookmark on this page |
| `F` | bookmarks menu (persisted in the user config dir) |
| `/` in menus | fuzzy filter (subsequence match) |
| `r` / `d` in bookmarks | rename / delete mark |
| `S` | screenshot page to PNG (`CBZR_SHOT_DIR` or cwd) |
| `R` | rotate 90° cw |
| `+` / `-` / `0` | zoom in / out / reset |
| arrows | pan while zoomed |
| `/` | OCR search via tesseract (`CBZR_OCR_LANG`, default `eng`) |
| `n` / `p` | next / prev search hit |
| `o` / `O` | open file in pane / in split |
| `x` | close pane |
| `e` | open current book in browser |
| `r` | re-render |
| `?` | help |
| `q` / `ctrl+c` | clear saved positions for open books and quit |
| `Q` | save positions for open books and quit |

The mouse wheel scrolls in webtoon mode and turns pages outside it.
Left-click a pane to focus it. The native macOS reader accepts numeric
prefixes for `j`, `k`, `J`, and `K`. It supports `g`, `G`, `t`,
`s`, `R`, `+`, `-`, `0`, arrow keys, `f`, `q`, and `Q`.

Reopen a book at the same path to resume its saved page and webtoon offset.
cbzr stores positions in the user configuration directory. Closing the
native window clears saved positions for the open books, as `q` does.

OCR search scans pages in the background and caches results; `n`/`p` jump
between hits and wrap. Opening another book, toggling split with `v`, enabling
split with `O`, closing a pane, or entering the native reader cancels the search.

## Browser

`e` starts a server on `127.0.0.1` using a random port from 50000 through
59999, then opens the current book and page. The web reader uses
`h`/`l`/`g`/`G` for navigation. The index at `/` lists open books.
