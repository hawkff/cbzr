package render

import "testing"

func TestSupportsKittyIgnoresInheritedMarkers(t *testing.T) {
	saved := terminal
	t.Cleanup(func() { terminal = saved })
	for _, tc := range []struct {
		name     string
		env      map[string]string
		answered bool // the terminal accepted a kitty graphics query
		want     bool
	}{
		{"kitty", map[string]string{"TERM": "xterm-kitty"}, false, true},
		{"Ghostty with a plain TERM", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "ghostty"}, false, true},
		{"kitty with a plain TERM", map[string]string{"TERM": "xterm-256color", "KITTY_WINDOW_ID": "1"}, false, true},
		{"VS Code started from kitty", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "vscode", "KITTY_WINDOW_ID": "1"}, false, false},
		{"zellij in kitty", map[string]string{"TERM": "xterm-kitty", "KITTY_WINDOW_ID": "1", "ZELLIJ": "0"}, false, false},
		{"screen in Ghostty", map[string]string{"TERM": "screen", "TERM_PROGRAM": "ghostty", "GHOSTTY_RESOURCES_DIR": "/usr/share/ghostty", "STY": "1.pts-0"}, false, false},
		{"kitty started from screen", map[string]string{"TERM": "xterm-kitty", "STY": "1.pts-0"}, false, true},
		{"herdr", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "herdr"}, true, true},
		{"herdr with kitty graphics off", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "herdr"}, false, false},
		{"herdr started from Ghostty", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "herdr", "GHOSTTY_RESOURCES_DIR": "/usr/share/ghostty"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, v := range []string{"TERM", "TERM_PROGRAM", "KITTY_WINDOW_ID", "KITTY_PID", "GHOSTTY_RESOURCES_DIR", "GHOSTTY_BIN_DIR", "TMUX", "ZELLIJ", "STY"} {
				t.Setenv(v, tc.env[v])
			}
			terminal.kitty = tc.answered
			if got := supportsKitty(); got != tc.want {
				t.Fatalf("supportsKitty() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A multiplexer inside a herdr pane must not get the kitty graphics query.
// GNU screen and tmux show an unknown APC string as a status line or title.
func TestKittyQueryGoesToHerdrPanesOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"herdr", map[string]string{"TERM_PROGRAM": "herdr"}, true},
		{"Ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, false},
		{"no terminal name", nil, false},
		{"screen in herdr", map[string]string{"TERM_PROGRAM": "herdr", "STY": "1.pts-0"}, false},
		{"tmux in herdr", map[string]string{"TERM_PROGRAM": "herdr", "TMUX": "/tmp/tmux-1000/default,1,0"}, false},
		{"zellij in herdr", map[string]string{"TERM_PROGRAM": "herdr", "ZELLIJ": "0"}, false},
	} {
		for _, v := range []string{"TERM_PROGRAM", "STY", "TMUX", "ZELLIJ"} {
			t.Setenv(v, tc.env[v])
		}
		if got := inHerdr(); got != tc.want {
			t.Errorf("%s: inHerdr() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
