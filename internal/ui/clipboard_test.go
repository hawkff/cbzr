package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCopyReachesLocalClipboardTools(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("clipboard helpers need sh")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	t.Setenv("CBZR_TEST_CLIPBOARD", log)
	for _, tool := range []string{"tmux", "pbcopy", "wl-copy", "xclip"} {
		script := "#!" + sh + "\nprintf '%s %s:' \"$(basename \"$0\")\" \"$*\" >> \"$CBZR_TEST_CLIPBOARD\"\ncat >> \"$CBZR_TEST_CLIPBOARD\"\nprintf '\\n' >> \"$CBZR_TEST_CLIPBOARD\"\n"
		if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1,0")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", ":0")
	copyLocally("copied text")
	want := "tmux load-buffer -w -:copied text\nwl-copy :copied text\n"
	if runtime.GOOS == "darwin" {
		want = "tmux load-buffer -w -:copied text\npbcopy :copied text\n"
	}
	if got, err := os.ReadFile(log); err != nil || string(got) != want {
		t.Fatalf("clipboard tools got %q, %v", got, err)
	}
	if runtime.GOOS != "darwin" {
		// A failing wl-copy falls back to xclip through Xwayland.
		if err := os.WriteFile(filepath.Join(dir, "wl-copy"), []byte("#!"+sh+"\nexit 1\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(log); err != nil {
			t.Fatal(err)
		}
		copyLocally("fallback")
		want = "tmux load-buffer -w -:fallback\nxclip -selection clipboard:fallback\n"
		if got, err := os.ReadFile(log); err != nil || string(got) != want {
			t.Fatalf("fallback got %q, %v", got, err)
		}
	}
	// Without tmux or a display, only the terminal's OSC 52 copy remains.
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"TMUX", "WAYLAND_DISPLAY", "DISPLAY"} {
		t.Setenv(v, "")
	}
	copyLocally("ignored")
	if _, err := os.Stat(log); runtime.GOOS != "darwin" && !os.IsNotExist(err) {
		t.Fatal("a session without a display ran a clipboard tool")
	}
}
