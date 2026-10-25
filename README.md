# cbzr

Read CBZ/CBR comics and EPUB, FB2, DOCX, DOC, PDF and DJVU books in your
terminal.

## Building and dependencies

- Go 1.26.10+ to build cbzr.
- cgo and Xcode command-line tools for native macOS builds.
- [Tesseract](https://github.com/tesseract-ocr/tesseract) for OCR search.
- [Poppler](https://poppler.freedesktop.org/) (`pdfinfo`, `pdftoppm`; `pdftohtml`
  for outlines and links, `pdftotext` for search and text selection) for PDF,
  [DjVuLibre](https://djvu.sourceforge.net/) (`djvused`, `ddjvu`) for DJVU and
  `antiword` for DOC. Comics, EPUB, FB2 and DOCX need no external tools.

## Linux

On Linux, use the terminal or browser reader. The native fullscreen window
requires macOS. Build without cgo:

```sh
CGO_ENABLED=0 go build -o cbzr .
./cbzr book.cbz
```

With a Go older than 1.26.10, such as Debian 13's, `GOTOOLCHAIN=auto` fetches
the right toolchain. Debian sets it by default.

The optional packages add the document converters, OCR, a browser launcher,
clipboard tools for terminals without OSC 52, and fonts for symbols and CJK
text in books:

| system | packages |
|---|---|
| Debian, Ubuntu | `poppler-utils djvulibre-bin antiword tesseract-ocr xdg-utils wl-clipboard xclip fonts-dejavu-core fonts-noto-core fonts-noto-cjk` |
| Fedora | `poppler-utils djvulibre antiword tesseract tesseract-langpack-eng xdg-utils wl-clipboard xclip dejavu-sans-fonts google-noto-sans-symbols-2-fonts google-noto-sans-cjk-fonts` |
| Arch | `poppler djvulibre antiword tesseract tesseract-data-eng xdg-utils wl-clipboard xclip ttf-dejavu noto-fonts noto-fonts-cjk` |
| Alpine | `poppler-utils djvulibre antiword tesseract-ocr tesseract-ocr-data-eng xdg-utils wl-clipboard xclip font-dejavu font-noto-symbols font-noto-cjk` |

Comics, EPUB, FB2 and DOCX work without these packages. For pixel graphics,
use Kitty or Ghostty, or a terminal with sixel graphics such as foot, WezTerm
or Konsole. Other truecolor terminals use the half-block renderer.
cbzr runs inside the terminal and needs no X server: in a Wayland session it
copies through `wl-copy` and opens links through `xdg-open`.

## Formats

- cbzr reads .cbz/.zip and .cbr/.rar archives, solid RAR included. It
  detects every format from the file signature, so a file still opens when
  its extension is wrong.
- cbzr sorts comic archive images in natural order (`p2` before `p10`).
  For EPUBs, it follows the package spine and document order.
- cbzr lays EPUB text out into fixed pages with the bundled Go fonts
  (regular, bold, italic) and system fonts for other scripts. Set
  `CBZR_EPUB_FALLBACK_FONT` to a TTF/OTF/TTC to replace the system font
  search. `h1` and `h2` headings start a page; lists, `pre` blocks and ruby
  readings keep their shape. MathML shows its alternative text, and audio,
  video and embedded objects show their fallback text.
- cbzr lays FB2 (plain or zipped) out the same way. The cover comes first,
  section titles start pages and fill the chapter menu, and note bodies
  follow the main text. cbzr keeps emphasis and embedded images and reads
  windows-1251 or koi8-r encodings.
- cbzr keeps DOCX heading styles, bold and italic runs, list bullets, tables
  as one paragraph per cell, and embedded pictures. It reads DOC through
  antiword's DocBook output to keep headings.
- cbzr renders PDF and DJVU pages on access with `pdftoppm` and `ddjvu`,
  scaled to fit 2000×2000 pixels. `-check` only counts their pages.
- cbzr decodes JPEG, PNG, GIF, WebP, and BMP page images.
- cbzr limits decompressed page data to 64 MiB and image dimensions to
  32 million pixels. Reaching a page in a solid RAR may decode at most 100
  times the archive's size on disk, all volumes counted, or 1 GiB for small
  archives. ComicInfo.xml must fit within 1 MiB; FB2 files and DOCX
  documents within 32 MiB.

## Rendering

At startup cbzr asks the terminal for its cell size and device attributes,
then picks the first backend in this list that the terminal supports.

- `kitty`: uses the Kitty graphics protocol with Unicode placeholders (`U=1`).
  cbzr sends images out of band and prints placeholder cells for Bubble Tea
  to repaint and join split panes. This backend runs in Kitty and Ghostty.
  Under tmux 3.3 or newer, cbzr enables passthrough and detects the outer
  terminal. It uses XTWINOPS replies for cell dimensions. zellij,
  GNU screen and terminals started from Kitty inherit Kitty's variables, and
  cbzr ignores them there. Pass `-r kitty` when detection misses, for example
  over SSH.
- `sixel`: draws pages as sixel images. cbzr picks it when the terminal's
  device attributes list sixel graphics, as in xterm started with
  `-ti vt340`, foot, WezTerm, Konsole and mlterm. Grayscale pages keep their
  exact gray levels; color pages get a median-cut palette sized to the
  terminal's color registers, up to 256. Text written over a sixel image
  erases the pixels under it, so cbzr draws an image again after any frame
  that rewrites one of its rows. In focus mode the bottom row stays empty,
  because the terminal moves the cursor below an image and an image on the
  last row would scroll the screen. Pass `-r sixel` when detection misses.
- `halfblock`: uses U+2580 with truecolor foreground and background pixels.
  Each cell displays two vertical pixels. Use this backend in a 24-bit color
  terminal with U+2580 support. Text pages of EPUB, FB2, DOCX and DOC books
  appear as plain text in this backend, and arrow keys scroll a page that
  overflows the pane. Webtoon mode shows text pages only with `kitty` or
  `sixel`.

## Keybindings

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
| `tab` | chapter menu with document outlines, tables of contents or headings |
| `b` | toggle bookmark on this page |
| `F` | bookmarks menu (persisted in the user config dir) |
| `/` in menus | fuzzy filter (subsequence match) |
| `r` / `d` in bookmarks | rename / delete mark |
| `S` | screenshot page to PNG (`CBZR_SHOT_DIR` or cwd) |
| `R` | rotate 90° cw |
| `i` | toggle inversion in the active pane (text pages in books; whole pages in comics, PDF and DJVU) |
| `+` / `-` / `0` | zoom in / out / reset |
| arrows | pan while zoomed; scroll a plain-text page that overflows the pane |
| `/` | search book text and PDF/DJVU text layers; OCR other pages with tesseract (`CBZR_OCR_LANG`, default `eng`) |
| `n` / `p` | next / prev search hit |
| `o` / `O` | open file in pane / in split |
| `x` | close pane |
| `e` | open current book in browser |
| `r` | re-render |
| `?` | help |
| `q` / `ctrl+c` | clear saved positions for open books and quit |
| `Q` | save positions for open books and quit |

Search reads book text and the text layers of PDF and DJVU files, and OCRs
the other pages in the background. It caches what it reads. Phrases match
across line breaks, and `n`/`p` jump between hits and wrap. The final status
counts pages it could not read, such as scans without tesseract. Opening
another book, toggling split with `v`, enabling split with `O`, closing a
pane, or entering the native reader cancels the search.

The chapter menu reads PDF and DJVU outlines, EPUB 3 navigation and EPUB 2
NCX tables of contents, FB2 section titles, and DOC/DOCX headings. DOCX
headings include inherited styles and paragraph outline levels. EPUBs without
a usable table of contents fall back to headings and document titles. Comic
archives use ComicInfo.xml bookmarks or the folders that hold their pages.

Nested entries keep their order and indentation. Select an entry to jump to
its page. PDF and DJVU outlines load in the background; missing tools or broken
metadata produce an error when you open the menu. The reader does not infer
chapter targets from a printed contents page.

## Mouse

On book and PDF pages, drag to select text. cbzr highlights the selection
and copies it to the clipboard when you release the button. Besides sending
OSC 52, cbzr runs `tmux load-buffer -w` inside tmux and the first of
`pbcopy`, `wl-copy` or `xclip` that works. That covers tmux's default
`set-clipboard`, GNOME Terminal and macOS Terminal, which drop OSC 52. Click
a link to follow it: web and mail links open in the browser, references
inside the book turn to their page. Hovering over a link shows its
destination in the status bar and right-click copies it. Opened and copied
links lose tracking parameters such as `utm_*`, `fbclid`, `gclid` and `si`.

Book pages carry their text from the layout. PDF pages get theirs from
`pdftotext` and `pdftohtml` shortly after they appear, so scanned PDFs
without a text layer have nothing to select. Selection needs an unrotated
page outside webtoon mode. Terminals without pixel graphics show book text as
plain text; hold shift there to use the terminal's own selection. DJVU pages
have no text layer yet.

## Web reader

`e` starts a server on `127.0.0.1` using a random port from 50000 through
59999, then opens the current book and page through a one-time link. The
link redirects to a URL with a random token, so the token never appears on
a command line, and the token URL's root lists the open books. On Linux,
where it can read `/proc/net/tcp`, the server also turns away connections
from other users. The browser lists its
keybindings in the header and opens with the active terminal pane's
inversion setting. Over SSH, forward the port with
`ssh -L PORT:127.0.0.1:PORT` and open the link from the status bar; it works
once, and `e` makes a new one.

## Yazi integration

Add cbzr as an opener in `~/.config/yazi/yazi.toml`. Open one file to read
it, or select two for a split. `block = true` hands the terminal to cbzr
until you quit.

```toml
[opener]
cbzr = [
  { run = 'cbzr %s', block = true, desc = "Read in cbzr" },
]

[open]
prepend_rules = [
  { url = "*.{cbz,cbr,epub,fb2,docx,doc,pdf,djvu,djv}", use = "cbzr" },
  { url = "*.fb2.zip", use = "cbzr" },
  { mime = "application/epub+zip", use = "cbzr" },
  { mime = "application/vnd.comicbook+zip", use = "cbzr" },
  { mime = "application/vnd.comicbook-rar", use = "cbzr" },
]
```

`url` globs match case-insensitively, so `*.CBZ` is covered. To keep the
default openers in the "open with" menu, use `use = ["cbzr", "open"]`.
