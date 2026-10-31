package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitCmdIn is git in dir, with a name and an email to commit with: a
// runner has none.
func gitCmdIn(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	return cmd
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := gitCmdIn(dir, args...).CombinedOutput(); err != nil {
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
	gitCmdIn(dir, "merge", "other").Run() // conflicts, as wanted

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

// CreateBranch makes the branch at HEAD and switches to it; a name a
// branch has already is refused.
func TestCreateBranch(t *testing.T) {
	root := t.TempDir()
	gitIn(t, root, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "a.txt")
	gitIn(t, root, "commit", "-q", "-m", "a")
	if _, err := CreateBranch(root, "feature/x"); err != nil {
		t.Fatal(err)
	}
	out, err := gitCmdIn(root, "branch", "--show-current").Output()
	if err != nil || strings.TrimSpace(string(out)) != "feature/x" {
		t.Fatalf("on %q, %v", out, err)
	}
	if _, err := CreateBranch(root, "main"); err == nil {
		t.Fatal("a branch that exists was created again")
	}
}
