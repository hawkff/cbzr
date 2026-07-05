# cbzr

Terminal comic reader for .cbz and .cbr. Two books side by side, vim keys,
images drawn in the terminal, optional browser hand-off.

```
cbzr book.cbz             one book
cbzr one.cbz two.cbr      split screen
cbzr                      file picker
cbzr -renderer=halfblock  force the fallback renderer
```

## Build

```
go build -o cbzr .
```

Requires Go 1.26+. For OCR search, install [tesseract](https://github.com/tesseract-ocr/tesseract)
(`brew install tesseract` / `apt install tesseract-ocr`).

## Formats

- .cbz/.zip and .cbr/.rar. Format is detected by signature, so mislabeled
  archives (a rar named .cbz) still open.
- Pages sort in natural order (`p2` before `p10`) regardless of archive order.
- Page images: jpeg, png, gif, webp, bmp.

## Rendering

Follows yazi's adapter idea: probe the terminal, pick the best backend.

- `kitty`: kitty graphics protocol with Unicode placeholders (U=1). Images are
  transmitted out of band; the TUI only prints placeholder cells, so bubbletea
  repaints and split-screen joins work without clipping regions. Used on
  kitty and ghostty. Under tmux, cbzr detects the outer terminal, enables
  allow-passthrough (tmux >= 3.3), and probes the cell pixel size via
  XTWINOPS, so pages stay sharp instead of falling back to half blocks.
- `halfblock`: U+2580 with truecolor fg/bg, two pixels per cell. Works in any
  24-bit terminal.

## Keys

| key | action |
|---|---|
| `j` / `k` | next / prev page (counts: `5j`) |
| `g` / `G` | first / last page (`42G` → page 42) |
| `w` | switch pane |
| `v` | toggle split (keeps active pane) |
| `s` | two-page spread: pages N and N+1 side by side (single pane) |
| `tab` | chapter menu (ComicInfo.xml bookmarks or archive folders) |
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
| `q` | quit |

Mouse: wheel turns pages in the pane under the cursor, click focuses a pane.

OCR search scans pages in the background and caches results; `n`/`p` jump
between hits and wrap.

## Browser

`e` binds `127.0.0.1` on a random port in 50000-59999 and opens the current
book at the current page. The web reader has the same `h`/`l`/`g`/`G` keys.
`/` lists both open books.
