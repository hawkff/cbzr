package render

import "testing"

func TestSupportsKittyIgnoresInheritedMarkers(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"kitty", map[string]string{"TERM": "xterm-kitty"}, true},
		{"Ghostty with a plain TERM", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "ghostty"}, true},
		{"kitty with a plain TERM", map[string]string{"TERM": "xterm-256color", "KITTY_WINDOW_ID": "1"}, true},
		{"VS Code started from kitty", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "vscode", "KITTY_WINDOW_ID": "1"}, false},
		{"zellij in kitty", map[string]string{"TERM": "xterm-kitty", "KITTY_WINDOW_ID": "1", "ZELLIJ": "0"}, false},
		{"screen in Ghostty", map[string]string{"TERM": "screen", "TERM_PROGRAM": "ghostty", "GHOSTTY_RESOURCES_DIR": "/usr/share/ghostty", "STY": "1.pts-0"}, false},
		{"kitty started from screen", map[string]string{"TERM": "xterm-kitty", "STY": "1.pts-0"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range []string{"TERM", "TERM_PROGRAM", "KITTY_WINDOW_ID", "KITTY_PID", "GHOSTTY_RESOURCES_DIR", "GHOSTTY_BIN_DIR", "TMUX", "ZELLIJ", "STY"} {
				t.Setenv(v, tc.env[v])
			}
			if got := supportsKitty(); got != tc.want {
				t.Fatalf("supportsKitty() = %v, want %v", got, tc.want)
			}
		})
	}
}
