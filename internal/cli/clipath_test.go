package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// The hint names the line for the user's shell, and nothing when the
// folder is on PATH.
func TestCLIPathHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".local", "bin")
	for _, path := range []string{dir, "/usr/bin:" + dir + "/", "/usr/bin::" + dir} {
		if got := cliPathHint(dir, path, "/bin/zsh", "darwin"); got != "" {
			t.Errorf("on PATH %q: %q", path, got)
		}
	}
	cases := []struct{ shell, goos, want string }{
		{"/bin/zsh", "darwin", `Add this line to ~/.zprofile: export PATH="$HOME/.local/bin:$PATH"`},
		{"/bin/bash", "darwin", "~/.bash_profile"},
		{"/bin/bash", "linux", "~/.profile"},
		{"/bin/sh", "linux", "~/.profile"},
		{"/opt/homebrew/bin/fish", "darwin", "Run: fish_add_path ~/.local/bin"},
	}
	for _, c := range cases {
		got := cliPathHint(dir, "/usr/bin:/bin", c.shell, c.goos)
		if !strings.HasPrefix(got, "~/.local/bin is not on your PATH") || !strings.Contains(got, c.want) {
			t.Errorf("%s on %s: %q", c.shell, c.goos, got)
		}
	}
}

// On Windows the hint is the pwsh line that adds omaterm's folder to the
// user PATH, with the folder quoted for pwsh. (PATH is split by the host's
// separator, so the on-PATH case is left to TestCLIPathHint.)
func TestCLIPathHintWindows(t *testing.T) {
	dir := `C:\Users\it's me\.local\state\omaterm\bin`
	got := cliPathHint(dir, `C:\Windows`, "", "windows")
	want := `[Environment]::SetEnvironmentVariable("Path", [Environment]::GetEnvironmentVariable("Path", "User") + ";" + 'C:\Users\it''s me\.local\state\omaterm\bin', "User")`
	if !strings.Contains(got, want) || strings.Contains(got, "~/.profile") {
		t.Errorf("hint = %q", got)
	}
}
