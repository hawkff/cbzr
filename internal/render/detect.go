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
	if termSuggestsKitty(os.Getenv("TERM"), os.Getenv("TERM_PROGRAM")) {
		return true
	}
	// Markers inherited from the outer terminal, visible inside tmux too.
	for _, v := range []string{"KITTY_WINDOW_ID", "KITTY_PID", "GHOSTTY_RESOURCES_DIR", "GHOSTTY_BIN_DIR"} {
		if os.Getenv(v) != "" {
			return true
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
