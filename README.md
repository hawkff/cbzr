# cbzr

Read CBZ/CBR comics and EPUB books in your terminal.

## Building and dependencies

- Go 1.26.4+ to build cbzr.
- cgo and Xcode command-line tools for native macOS builds.
- [Tesseract](https://github.com/tesseract-ocr/tesseract) for OCR search.

## Formats

- cbzr reads .cbz/.zip and .cbr/.rar archives. It detects the format from the
  file signature, so an archive still opens when its extension is wrong.
- cbzr sorts comic archive images in natural order (`p2` before `p10`).
  For EPUBs, it follows the package spine and document order.
- cbzr decodes JPEG, PNG, GIF, WebP, and BMP page images.
- cbzr limits decompressed page data to 64 MiB and image dimensions to
  32 million pixels. ComicInfo.xml must fit within 1 MiB.

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
| `tab` | chapter menu (EPUB headings/titles, ComicInfo.xml bookmarks or archive folders) |
| `b` | toggle bookmark on this page |
| `F` | bookmarks menu (persisted in the user config dir) |
| `/` in menus | fuzzy filter (subsequence match) |
| `r` / `d` in bookmarks | rename / delete mark |
| `S` | screenshot page to PNG (`CBZR_SHOT_DIR` or cwd) |
| `R` | rotate 90° cw |
| `i` | toggle inversion in the active pane |
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

OCR search scans pages in the background and caches results; `n`/`p` jump
between hits and wrap. Opening another book, toggling split with `v`, enabling
split with `O`, closing a pane, or entering the native reader cancels the search.

## Web reader

`e` starts a server on `127.0.0.1` using a random port from 50000 through
59999, then opens the current book and page. The browser lists its keybindings
in the header. It opens with
the active terminal pane's inversion setting. The index at `/` lists open books.

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
  { url = "*.{cbz,cbr,epub}", use = "cbzr" },
  { mime = "application/epub+zip", use = "cbzr" },
  { mime = "application/vnd.comicbook+zip", use = "cbzr" },
  { mime = "application/vnd.comicbook-rar", use = "cbzr" },
]
```

`url` globs match case-insensitively, so `*.CBZ` is covered. To keep the
default openers in the "open with" menu, use `use = ["cbzr", "open"]`.
