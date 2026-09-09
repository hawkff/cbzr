// cbzr is a terminal reader for .cbz, .cbr and .epub.
//
//	cbzr one.cbz            read one book
//	cbzr one.cbz two.cbr    split screen, two books side by side
//	cbzr                    start with an empty pane; press o to open
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"cbzr/internal/bookmarks"
	"cbzr/internal/native"
	"cbzr/internal/progress"
	"cbzr/internal/render"
	"cbzr/internal/server"
	"cbzr/internal/ui"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if runtime.GOOS == "darwin" {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	backend := flag.String("renderer", "", "force renderer: kitty | halfblock (default: auto)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: cbzr [flags] [book [book2]]\nformats: .cbz/.zip, .cbr/.rar, .epub\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("cbzr", version)
		return
	}

	paths := flag.Args()
	if len(paths) > 2 {
		fmt.Fprintln(os.Stderr, "cbzr: at most two books (split screen)")
		os.Exit(2)
	}

	render.QueryCellSize() // must run before bubbletea owns the tty
	r := render.Detect(*backend)
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
