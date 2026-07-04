// cbzr is a terminal .cbz comic/manga reader.
//
//	cbzr one.cbz            read one book
//	cbzr one.cbz two.cbz    split screen, two books side by side
//	cbzr                    start with the file picker
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"cbzr/internal/bookmarks"
	"cbzr/internal/render"
	"cbzr/internal/server"
	"cbzr/internal/ui"
)

func main() {
	backend := flag.String("renderer", "", "force renderer: kitty | halfblock (default: auto)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: cbzr [flags] [book.cbz [book2.cbz]]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	paths := flag.Args()
	if len(paths) > 2 {
		fmt.Fprintln(os.Stderr, "cbzr: at most two books (split screen)")
		os.Exit(2)
	}

	render.QueryCellSize() // must run before bubbletea owns the tty
	r := render.Detect(*backend)
	srv := server.New()
	defer srv.Close() //nolint:errcheck

	m := ui.New(r, srv, bookmarks.Load(), openBrowser, paths)
	srv.SetBooks(m.Books())

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	final, err := p.Run()
	// Free terminal-side images after the program released the tty.
	for id := uint32(1); id <= 3; id++ {
		if b := r.Delete(id); len(b) > 0 {
			os.Stdout.Write(b) //nolint:errcheck
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cbzr:", err)
		os.Exit(1)
	}
	if fm, ok := final.(ui.Model); ok {
		for _, b := range fm.Books() {
			if b != nil {
				b.Close() //nolint:errcheck
			}
		}
	}
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
