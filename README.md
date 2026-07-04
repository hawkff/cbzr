# cbzr

Terminal .cbz reader. Two books side by side, vim keys, images drawn in the
terminal, optional browser hand-off.

```
cbzr one.cbz            one book
cbzr one.cbz two.cbz    split screen
cbzr                    file picker
cbzr -renderer=halfblock ...
```

## Rendering

Follows yazi's adapter idea: probe the terminal, pick the best backend.

- `kitty`: kitty graphics protocol with Unicode placeholders (U=1). Images are
  transmitted out of band; the TUI only prints placeholder cells, so bubbletea
  repaints and split-screen joins work without clipping regions. Used on
  kitty, ghostty, WezTerm; wrapped in passthrough sequences under tmux.
- `halfblock`: U+2580 with truecolor fg/bg, two pixels per cell. Works in any
  24-bit terminal.

## Keys

| key | action |
|---|---|
| `h` `l`, `←` `→`, `j` `k`, space, `n` `p` | prev / next page (counts: `5l`) |
| `g` / `G` | first / last page (`42G` → page 42) |
| `tab`, `w` | switch pane |
| `s`, `v` | toggle split (keeps active pane) |
| `o` / `O` | open file in pane / in split |
| `x` | close pane |
| `b` | open current book in browser |
| `r` | re-render |
| `?` | help |
| `q` | quit |

Mouse: wheel turns pages in the pane under the cursor, click focuses a pane.

## Browser

`b` binds `127.0.0.1` on a random port in 50000-59999 and opens the current
book at the current page. The web reader has the same `h`/`l`/`g`/`G` keys.
`/` lists both open books.

## Build

Built via namespace (`nsc build --output-local=dist .`), which also runs
gofmt, go vet, and cross-compiles darwin/arm64 + linux/amd64.

## Format notes

- Pages sort in natural order (`p2` before `p10`), regardless of zip order.
- jpeg, png, gif, webp, bmp pages; AppleDouble (`._*`) entries are skipped.
- .cbr (rar) is not supported.
