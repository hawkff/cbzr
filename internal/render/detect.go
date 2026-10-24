package render

import (
	"os"
	"os/exec"
	"strings"
)

// supportsKitty reports whether the terminal — or, under tmux, the terminal
// hosting the tmux client — likely speaks the kitty graphics protocol with
// Unicode placeholders (kitty, ghostty).
func supportsKitty() bool {
	// shortcut: zellij keeps kitty's TERM but drops its graphics, and a terminal
	// started from inside zellij inherits ZELLIJ too; -r kitty covers that case.
	if os.Getenv("ZELLIJ") != "" {
		return false
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
