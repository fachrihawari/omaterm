package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// Names with spaces and accents are the files' own (they came quoted, so
// Stage and Discard found nothing); a merge conflict shows as one, not
// as an untracked file; and git's warnings are not taken for files.
func TestStatusNames(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("base\n"), 0o644)
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "base")
	gitIn(t, dir, "switch", "-q", "-c", "other")
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("other\n"), 0o644)
	gitIn(t, dir, "commit", "-q", "-am", "other")
	gitIn(t, dir, "switch", "-q", "main")
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("main\n"), 0o644)
	gitIn(t, dir, "commit", "-q", "-am", "main")
	cmd := exec.Command("git", "merge", "other")
	cmd.Dir = dir
	cmd.Run() // conflicts, as wanted

	os.WriteFile(filepath.Join(dir, "a b.txt"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "café.txt"), nil, 0o644)
	locked := filepath.Join(dir, "locked")
	os.Mkdir(locked, 0o000)
	defer os.Chmod(locked, 0o755)

	staged, changes, err := Status(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range append(staged, changes...) {
		got[f.Path] = f.Status
	}
	if got["a b.txt"] != "U" || got["café.txt"] != "U" {
		t.Errorf("names with a space or an accent: %v", got)
	}
	if got["c.txt"] != "!" || len(staged) != 0 {
		t.Errorf("the conflict reads %q (staged %v)", got["c.txt"], staged)
	}
	for path := range got {
		if strings.Contains(path, "warning") || strings.Contains(path, "could not") {
			t.Errorf("a warning taken for a file: %q", path)
		}
	}
	if err := Stage(dir, "a b.txt"); err != nil {
		t.Errorf("staging a name with a space: %v", err)
	}
}
