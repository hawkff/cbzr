// cbzr is a terminal reader for comic archives, EPUB, FB2, DOCX, DOC, PDF and DJVU.
//
//	cbzr one.cbz            read one book
//	cbzr one.cbz two.cbr    split screen, two books side by side
//	cbzr                    start with an empty pane; press o to open
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"cbzr/internal/book"
	"cbzr/internal/bookmarks"
	"cbzr/internal/native"
	"cbzr/internal/progress"
	"cbzr/internal/render"
	"cbzr/internal/server"
	"cbzr/internal/ui"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

const usage = `cbzr reads comics and books in the terminal.

usage:
  cbzr [flags] [book [book2]]   one book, or two side by side
  cbzr -c book [book ...]       check books and exit
  cbzr                          start empty; press o to open a file

formats: .cbz/.zip, .cbr/.rar, .epub, .fb2/.fb2.zip, .docx (built in);
         .pdf (poppler), .djvu (djvulibre), .doc (antiword);
         the format is detected by signature

flags:
  -r, -renderer kitty|halfblock   force a renderer (default: detect)
  -c, -check                      index books and lay out text without opening
                                  a reader; images decode on page access
  -v, -version                    print the version and exit
  -h, -help                       print this help and exit

environment:
  CBZR_SHOT_DIR             where S saves screenshots (default: current dir)
  CBZR_OCR_LANG             tesseract language for / search (default: eng)
  CBZR_EPUB_FALLBACK_FONT   TTF/OTF/TTC with glyphs the bundled fonts lack;
                            replaces the system font search

EPUB, FB2, DOCX and DOC text renders as page images in Kitty, Ghostty and
tmux 3.3+ with passthrough, and as plain text in other terminals (webtoon
mode needs the kitty renderer). PDF and DJVU pages render through pdftoppm
and ddjvu on access. e opens the browser reader, f the native macOS window.

Positions and bookmarks live in the user config dir (~/.config/cbzr on
Linux, ~/Library/Application Support/cbzr on macOS). Press ? in the reader
for the key list.
`

func main() {
	if runtime.GOOS == "darwin" {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	var backend string
	var showVersion, check bool
	flag.StringVar(&backend, "renderer", "", "")
	flag.StringVar(&backend, "r", "", "")
	flag.BoolVar(&showVersion, "version", false, "")
	flag.BoolVar(&showVersion, "v", false, "")
	flag.BoolVar(&check, "check", false, "")
	flag.BoolVar(&check, "c", false, "")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()
	if showVersion {
		fmt.Println("cbzr", version)
		return
	}

	paths := flag.Args()
	if check {
		os.Exit(checkBooks(paths, os.Stdout, os.Stderr))
	}
	if len(paths) > 2 {
		fmt.Fprintln(os.Stderr, "cbzr: at most two books (split screen)")
		os.Exit(2)
	}

	render.QueryCellSize() // must run before bubbletea owns the tty
	r := render.Detect(backend)
	srv := new(server.Server)
	defer srv.Close() //nolint:errcheck

	positions := progress.Load()
	m := ui.New(r, srv, bookmarks.Load(), positions, openBrowser, native.Available(), paths)

	for {
		srv.SetBooks(m.Books())
		p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithFPS(120))
		final, err := p.Run()
		// Free terminal-side images after the program released the tty.
		for id := uint32(1); id <= 6; id++ {
			if b := r.Delete(id); len(b) > 0 {
				os.Stdout.Write(b) //nolint:errcheck
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "cbzr:", err)
			os.Exit(1)
		}
		fm, ok := final.(ui.Model)
		if !ok {
			return
		}
		if !fm.NativeRequested() {
			finishTerminal(fm)
			return
		}

		result, runErr := native.Run(fm.NativeState())
		if runErr != nil {
			fmt.Fprintln(os.Stderr, "cbzr: native reader:", runErr)
		}
		if runErr != nil && result.State.Path == "" {
			m = fm.ApplyNativeState(fm.NativeState())
			continue
		}
		if result.Action == native.ActionReturn {
			m = fm.ApplyNativeState(result.State)
			continue
		}
		var saveErr error
		if result.Action == native.ActionSave {
			saveErr = fm.SaveNativeProgress(result.State)
		} else {
			saveErr = fm.ClearProgress()
		}
		if saveErr != nil {
			fmt.Fprintln(os.Stderr, "cbzr: update progress:", saveErr)
		}
		closeBooks(fm)
		return
	}
}

func checkBooks(paths []string, stdout, stderr io.Writer) int {
	if len(paths) == 0 {
		fmt.Fprintln(stderr, "cbzr: -check needs at least one book path")
		return 2
	}
	status := 0
	for _, path := range paths {
		b, err := book.Open(path)
		if err != nil {
			fmt.Fprintf(stderr, "cbzr: check %q: %q\n", path, err.Error())
			status = 1
			continue
		}
		pages := b.Len()
		if err := b.Close(); err != nil {
			fmt.Fprintf(stderr, "cbzr: close %q: %q\n", path, err.Error())
			status = 1
			continue
		}
		fmt.Fprintf(stdout, "%q: OK (%d pages; indexing/text layout only, images not decoded)\n", path, pages)
	}
	return status
}

func finishTerminal(m ui.Model) {
	if m.SaveOnQuit() {
		if err := m.SaveProgress(); err != nil {
			fmt.Fprintln(os.Stderr, "cbzr: save progress:", err)
		}
	} else if err := m.ClearProgress(); err != nil {
		fmt.Fprintln(os.Stderr, "cbzr: clear progress:", err)
	}
	closeBooks(m)
}

func closeBooks(m ui.Model) {
	for _, b := range m.Books() {
		if b != nil {
			b.Close() //nolint:errcheck
		}
	}
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
