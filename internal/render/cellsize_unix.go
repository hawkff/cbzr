//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package render

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

var queriedCell struct{ cw, ch float64 }

// CellSize returns the pixel size of one terminal cell. Order: TIOCGWINSZ
// (live), the cached XTWINOPS probe from QueryCellSize, then 8x16.
func CellSize() (cw, ch float64) {
	if cw, ch, ok := ioctlCellSize(); ok {
		return cw, ch
	}
	if queriedCell.cw > 0 {
		return queriedCell.cw, queriedCell.ch
	}
	return 8, 16
}

func ioctlCellSize() (float64, float64, bool) {
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

// QueryCellSize probes the terminal once with XTWINOPS "CSI 16 t" for cases
// where the pty reports no pixel size (tmux panes, some terminals). Must run
// before the TUI owns stdin; the result is cached for CellSize.
func QueryCellSize() {
	if _, _, ok := ioctlCellSize(); ok {
		return
	}
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

	if _, err := tty.WriteString("\x1b[16t"); err != nil {
		return
	}
	var buf []byte
	tmp := make([]byte, 64)
	for i := 0; i < 8; i++ { // bounded: VTIME caps each read at 300ms
		n, err := tty.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if w, h, ok := parseCellReply(buf); ok {
				queriedCell.cw, queriedCell.ch = w, h
				return
			}
		}
		if err != nil || n == 0 || len(buf) > 256 {
			return
		}
	}
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
