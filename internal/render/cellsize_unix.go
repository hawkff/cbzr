//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package render

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// terminal holds what the startup probe learned. Detect fills it before the
// TUI starts; nothing changes it later.
var terminal struct {
	cw, ch float64 // cell size the terminal reported (XTWINOPS)
	sixel  bool    // its device attributes list sixel graphics
	colors int     // sixel color registers it reported (XTSMGRAPHICS)
}

// CellSize returns the pixel size of one terminal cell. The XTWINOPS
// answer from the probe comes from the terminal itself, while TIOCGWINSZ
// pixel fields pass through muxes (tmux, cmux) that often report
// whole-window geometry for a differently-sized pane. TIOCGWINSZ follows
// font changes, though, so a smaller size from it wins: sixel pixels map one
// to one onto the screen, and cells taken too large push an image past its
// rows.
// shortcut: a font that grows after startup keeps the startup size, and so
// does one that shrinks under a mux with whole-window geometry; fixing that
// means asking XTWINOPS again and reading the reply through Bubble Tea.
func CellSize() (cw, ch float64) {
	icw, ich, ok := ioctlCellSize()
	switch {
	case terminal.cw > 0 && ok:
		return min(terminal.cw, icw), min(terminal.ch, ich)
	case terminal.cw > 0:
		return terminal.cw, terminal.ch
	case ok:
		return icw, ich
	}
	return 8, 16
}

// ioctlCellSize reads the cell size from TIOCGWINSZ; tests replace it.
var ioctlCellSize = func() (float64, float64, bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return 0, 0, false
	}
	defer tty.Close()
	ws, err := unix.IoctlGetWinsize(int(tty.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 || ws.Row == 0 || ws.Xpixel == 0 || ws.Ypixel == 0 {
		return 0, 0, false
	}
	return float64(ws.Xpixel) / float64(ws.Col), float64(ws.Ypixel) / float64(ws.Row), true
}

// probe asks the terminal for its cell size (XTWINOPS "CSI 16 t") and its
// device attributes (DA1), which list sixel graphics as 4. Terminals answer
// in order and every one answers DA1, so its reply ends the wait. A sixel
// terminal, or any terminal when sixel is set, is then asked how many color
// registers it has. probe must run before the TUI owns stdin.
func probe(sixel bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer tty.Close()
	fd := int(tty.Fd())

	old, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return
	}
	raw := *old
	raw.Lflag &^= unix.ICANON | unix.ECHO
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 3 // read returns after 300ms of silence
	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &raw); err != nil {
		return
	}
	defer unix.IoctlSetTermios(fd, ioctlWriteTermios, old) //nolint:errcheck

	reply := ask(tty, "\x1b[16t\x1b[c")
	if w, h, ok := parseCellReply(reply); ok {
		terminal.cw, terminal.ch = w, h
	}
	terminal.sixel = hasSixel(reply)
	if terminal.sixel || sixel {
		terminal.colors = colorRegisters(ask(tty, "\x1b[?1;1;0S\x1b[c"))
	}
}

// deviceAttributes matches a DA1 reply, "ESC [ ? class ; attributes c".
var deviceAttributes = regexp.MustCompile(`\x1b\[\?([0-9;]*)c`)

// ask writes query and collects replies until the DA1 reply arrives.
func ask(tty *os.File, query string) []byte {
	if _, err := tty.WriteString(query); err != nil {
		return nil
	}
	var buf []byte
	tmp := make([]byte, 256)
	for range 8 { // bounded: VTIME caps each read at 300ms
		n, err := tty.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if deviceAttributes.Match(buf) || err != nil || n == 0 || len(buf) > 4096 {
			break
		}
	}
	return buf
}

// hasSixel reports whether a DA1 reply lists sixel graphics (4) after the
// terminal class.
func hasSixel(reply []byte) bool {
	m := deviceAttributes.FindSubmatch(reply)
	if m == nil {
		return false
	}
	attrs := strings.Split(string(m[1]), ";")
	return slices.Contains(attrs[1:], "4")
}

// colorRegisters reads an XTSMGRAPHICS reply, "ESC [ ? 1 ; 0 ; count S".
func colorRegisters(reply []byte) int {
	m := regexp.MustCompile(`\x1b\[\?1;0;([0-9]+)S`).FindSubmatch(reply)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// parseCellReply extracts "ESC [ 6 ; height ; width t".
func parseCellReply(b []byte) (cw, ch float64, ok bool) {
	s := string(b)
	i := strings.LastIndex(s, "\x1b[6;")
	if i < 0 {
		return 0, 0, false
	}
	rest := s[i+4:]
	j := strings.IndexByte(rest, 't')
	if j < 0 {
		return 0, 0, false
	}
	var h, w int
	if _, err := fmt.Sscanf(rest[:j+1], "%d;%dt", &h, &w); err != nil || h <= 0 || w <= 0 {
		return 0, 0, false
	}
	return float64(w), float64(h), true
}
