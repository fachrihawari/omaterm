package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Fetch, pull and push against a real local remote, including the first
// push of a branch that has no upstream yet.
func TestRemoteOps(t *testing.T) {
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %s\n%s", args, dir, err, out)
		}
	}

	bare := t.TempDir()
	root := t.TempDir()
	run(bare, "init", "--bare", "-q", "-b", "main")
	run(root, "init", "-q", "-b", "main")
	run(root, "config", "user.email", "t@example.com")
	run(root, "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(root, "add", ".")
	run(root, "commit", "-q", "-m", "first")
	run(root, "remote", "add", "origin", bare)

	if !HasRemote(root) {
		t.Fatal("HasRemote = false with an origin remote")
	}
	// The first push has no upstream yet and must still work.
	if out, err := Push(root); err != nil {
		t.Fatalf("first push: %v\n%s", err, out)
	}
	if !HasUpstream(root) {
		t.Fatal("push did not set the upstream")
	}
	if out, err := Push(root); err != nil {
		t.Fatalf("second push: %v\n%s", err, out)
	}
	if _, err := Fetch(root); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	// A commit made elsewhere, then pulled in.
	other := t.TempDir()
	run(other, "clone", "-q", bare, ".")
	run(other, "config", "user.email", "t@example.com")
	run(other, "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(other, "b.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(other, "add", ".")
	run(other, "commit", "-q", "-m", "second")
	run(other, "push", "-q")

	if out, err := Pull(root); err != nil {
		t.Fatalf("pull: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); err != nil {
		t.Fatalf("pull did not bring the other commit: %v", err)
	}

	// A directory with no remote says so, so the panel can gray the buttons.
	if HasRemote(t.TempDir()) {
		t.Fatal("HasRemote = true for a directory with no remote")
	}
}
