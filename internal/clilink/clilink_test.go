package clilink

import (
	"os"
	"path/filepath"
	"testing"
)

// writeExe writes an executable of content at path.
func writeExe(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The Windows link is the same file as the executable, and is made again
// once the executable is replaced.
func TestLinkExeFollowsTheFile(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "app", "omaterm.exe")
	bin := filepath.Join(dir, "bin")
	os.Mkdir(filepath.Dir(exe), 0o755)
	writeExe(t, exe, "one")
	link := filepath.Join(bin, "omaterm.exe")

	if err := linkExe(exe, link); err != nil {
		t.Fatal(err)
	}
	if !same(exe, link) {
		t.Fatal("the link is not the executable")
	}
	if err := linkExe(exe, link); err != nil {
		t.Fatal(err)
	}

	// An update renames a new executable over the old one.
	writeExe(t, exe+".new", "two")
	if err := os.Rename(exe+".new", exe); err != nil {
		t.Fatal(err)
	}
	if same(exe, link) {
		t.Fatal("the link should be on the old file after an update")
	}
	if err := linkExe(exe, link); err != nil {
		t.Fatal(err)
	}
	if !same(exe, link) {
		t.Fatal("the link was not made again after the update")
	}
	if b, _ := os.ReadFile(link); string(b) != "two" {
		t.Errorf("the link holds %q, want the new executable", b)
	}
}

// Where a hard link can't be made, the executable is copied.
func TestCopyFileIsExecutable(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "from")
	to := filepath.Join(dir, "to")
	writeExe(t, from, "copied")
	if err := copyFile(from, to); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(to)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(to); string(b) != "copied" || st.Mode()&0o100 == 0 {
		t.Errorf("copy holds %q with mode %v", b, st.Mode())
	}
}

// Target on Unix reads the symlink back; a missing link is "".
func TestTargetIsEmptyWithoutLink(t *testing.T) {
	if got := Target(t.TempDir()); got != "" {
		t.Errorf("Target without a link = %q", got)
	}
}
