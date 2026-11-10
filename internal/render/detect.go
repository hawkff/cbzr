package render

import (
	"os"
	"os/exec"
	"strings"
)

// inHerdr reports whether cbzr runs in a herdr pane with no multiplexer in
// between. herdr names itself in place of the terminal around it and draws
// kitty graphics for its panes, so the probe asks it with a graphics query,
// which herdr accepts unless its terminal.kitty_graphics setting is off. A
// multiplexer inside the pane keeps herdr's name and does not pass the query
// on. GNU screen and a tmux too old to name itself would show it as a status
// line or title.
func inHerdr() bool {
	for _, v := range []string{"STY", "TMUX", "ZELLIJ"} {
		if os.Getenv(v) != "" {
			return false
		}
	}
	return os.Getenv("TERM_PROGRAM") == "herdr"
}

// supportsKitty reports whether the terminal likely speaks the kitty graphics
// protocol with Unicode placeholders, as kitty, ghostty and herdr do. Under
// tmux, the terminal hosting the tmux client counts.
func supportsKitty() bool {
	// shortcut: zellij keeps kitty's TERM but drops its graphics, and a terminal
	// started from inside zellij inherits ZELLIJ too; -r kitty covers that case.
	if os.Getenv("ZELLIJ") != "" {
		return false
	}
	if inHerdr() {
		// shortcut: herdr accepts the query even when the terminal around it
		// has no kitty graphics, and offers no way to ask about that terminal;
		// pages stay blank there until -r halfblock.
		return terminal.kitty
	}
	prog := os.Getenv("TERM_PROGRAM")
	if os.Getenv("STY") != "" {
		// GNU screen keeps the outer TERM_PROGRAM; only its own TERM counts.
		prog = "screen"
	}
	if termSuggestsKitty(os.Getenv("TERM"), prog) {
		return true
	}
	// Markers inherited from the outer terminal, visible inside tmux too.
	// Terminals started from kitty, such as VS Code's, inherit them as well but
	// name themselves in TERM_PROGRAM.
	if prog == "" || prog == "tmux" {
		for _, v := range []string{"KITTY_WINDOW_ID", "KITTY_PID", "GHOSTTY_RESOURCES_DIR", "GHOSTTY_BIN_DIR"} {
			if os.Getenv(v) != "" {
				return true
			}
		}
	}
	if os.Getenv("TMUX") != "" {
		// Ask tmux which terminal the attached client runs in.
		out, err := exec.Command("tmux", "display-message", "-p", "#{client_termname}").Output()
		if err == nil && termSuggestsKitty(strings.TrimSpace(string(out)), "") {
			return true
		}
	}
	return false
}

func termSuggestsKitty(term, prog string) bool {
	return strings.Contains(term, "kitty") ||
		strings.Contains(term, "ghostty") ||
		prog == "ghostty"
}

// Detect probes the terminal and picks a renderer: kitty graphics where the
// terminal speaks them, sixel where its device attributes list it, half
// blocks elsewhere. force names a renderer to use instead. Detect must run
// before the TUI owns the terminal.
func Detect(force string) Renderer {
	// Half blocks need the cell size only for its proportions.
	if _, _, ok := ioctlCellSize(); force != "halfblock" || !ok {
		probe(force == "sixel")
	}
	switch {
	case force == "kitty" || force == "" && supportsKitty():
		return NewKitty()
	case force == "sixel" || force == "" && terminal.sixel:
		return NewSixel()
	}
	return NewHalfBlock()
}
