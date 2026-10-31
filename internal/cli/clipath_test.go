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
