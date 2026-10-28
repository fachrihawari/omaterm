package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolate keeps the user's git config out of the tests, and gives git a
// name and an email to commit with.
func isolate(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@example.com")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo is a repository on main with one commit.
func repo(t *testing.T) string {
	t.Helper()
	isolate(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "a.txt"), "one\ntwo\nthree\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func add(t *testing.T, dir, branch string) Info {
	t.Helper()
	w, err := Add(context.Background(), dir, branch, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func clean(t *testing.T, dir string) {
	t.Helper()
	if s := git(t, dir, "status", "--porcelain"); s != "" {
		t.Fatalf("%s isn't clean:\n%s", dir, s)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err == nil {
		t.Fatalf("%s is mid-merge", dir)
	}
}

func TestAdd(t *testing.T) {
	dir := repo(t)
	w := add(t, dir, "feat/x")
	if want := filepath.Join(dir, ".worktrees", "feat-x"); w.Dir != want || w.Main != dir {
		t.Fatalf("Dir %s Main %s, want %s in %s", w.Dir, w.Main, want, dir)
	}
	if w.Base != "main" || w.Branch != "feat/x" || w.Reused {
		t.Fatalf("%+v", w)
	}
	if b := git(t, w.Dir, "branch", "--show-current"); b != "feat/x" {
		t.Fatalf("worktree on %q", b)
	}
	// The main checkout doesn't show the worktrees as files to add.
	clean(t, dir)
	if Of(w.Dir, w.Branch, w.Base) != (Info{Main: dir, Dir: w.Dir, Branch: "feat/x", Base: "main"}) {
		t.Fatalf("Of: %+v", Of(w.Dir, w.Branch, w.Base))
	}

	// Again, the same worktree; an existing branch gets one of its own.
	if again := add(t, dir, "feat/x"); again.Dir != w.Dir || !again.Reused {
		t.Fatalf("again: %+v", again)
	}
	git(t, dir, "branch", "old")
	if old := add(t, dir, "old"); old.Reused || git(t, old.Dir, "branch", "--show-current") != "old" {
		t.Fatalf("existing branch: %+v", old)
	}

	// A folder in the way gets a number.
	os.MkdirAll(filepath.Join(dir, ".worktrees", "y"), 0o755)
	if y := add(t, dir, "y"); filepath.Base(y.Dir) != "y-2" {
		t.Fatalf("beside a folder: %s", y.Dir)
	}
}

// A project in a folder of its repository starts in the same folder of
// the worktree, which goes beside the repository's main checkout.
func TestAddFromFolder(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, "apps", "web", "index.html"), "hi\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "web")
	w := add(t, filepath.Join(dir, "apps", "web"), "w")
	if w.Main != dir || w.Rel != filepath.Join("apps", "web") {
		t.Fatalf("%+v", w)
	}
	if _, err := os.Stat(filepath.Join(w.Dir, w.Rel, "index.html")); err != nil {
		t.Fatal(err)
	}
	// One made from a worktree goes beside the main checkout too.
	if v := add(t, w.Dir, "v"); filepath.Dir(v.Dir) != filepath.Join(dir, ".worktrees") || v.Base != "w" {
		t.Fatalf("from a worktree: %+v", v)
	}
}

func TestAddRefuses(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	if _, err := Add(ctx, dir, "main", "", false); err == nil || !strings.Contains(err.Error(), "checked out in") {
		t.Errorf("main: %v", err)
	}
	if _, err := Add(ctx, dir, "x", "nope", false); err == nil || !strings.Contains(err.Error(), "no branch nope") {
		t.Errorf("no base: %v", err)
	}
	if _, err := Add(ctx, dir, "a b", "", false); err == nil {
		t.Error("a name with a space")
	}
	if _, err := Add(ctx, t.TempDir(), "x", "", false); err == nil || !strings.Contains(err.Error(), "not in a git repository") {
		t.Errorf("not a repository: %v", err)
	}
	empty := t.TempDir()
	git(t, empty, "init", "-q", "-b", "main")
	if _, err := Add(ctx, empty, "x", "", false); err == nil || !strings.Contains(err.Error(), "no commits") {
		t.Errorf("no commits: %v", err)
	}
}

func TestExclude(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	for range 2 {
		if err := Exclude(ctx, dir); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if n := strings.Count(string(b), "/.worktrees/"); n != 1 {
		t.Fatalf("written %d times:\n%s", n, b)
	}

	// Ignored already, it is left alone.
	other := repo(t)
	write(t, filepath.Join(other, ".gitignore"), ".worktrees\n")
	os.Remove(filepath.Join(other, ".git", "info", "exclude"))
	if err := Exclude(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(other, ".git", "info", "exclude")); err == nil {
		t.Fatal("wrote an exclude for a folder .gitignore ignores")
	}
}

func TestCopyEnv(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, ".gitignore"), ".env\n.env.local\nnode_modules/\n")
	write(t, filepath.Join(dir, ".env.example"), "KEY=\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "ignore")
	write(t, filepath.Join(dir, ".env"), "KEY=1\n")
	write(t, filepath.Join(dir, "apps", "web", ".env.local"), "KEY=2\n")
	write(t, filepath.Join(dir, ".envrc"), "untracked, not ignored\n")
	write(t, filepath.Join(dir, "node_modules", "x", ".env"), "in an ignored folder\n")

	w, err := Add(context.Background(), dir, "x", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		".env": true, "apps/web/.env.local": true, ".envrc": false, "node_modules/x/.env": false,
	} {
		_, err := os.Stat(filepath.Join(w.Dir, path))
		if got := err == nil; got != want {
			t.Errorf("%s copied: %v, want %v", path, got, want)
		}
	}
	if s := git(t, w.Dir, "status", "--porcelain"); s != "" {
		t.Errorf("copied files show in the worktree:\n%s", s)
	}
	// What is there already stays.
	write(t, filepath.Join(w.Dir, ".env"), "mine\n")
	if n := CopyEnv(context.Background(), dir, w.Dir); n != 0 {
		t.Errorf("copied %d over existing files", n)
	}
}

func TestSummarize(t *testing.T) {
	dir := repo(t)
	w := add(t, dir, "x")
	ctx := context.Background()
	if s, err := Summarize(ctx, w); err != nil || s.Unmerged() {
		t.Fatalf("new worktree: %+v %v", s, err)
	}
	write(t, filepath.Join(w.Dir, "a.txt"), "one\n2\nthree\nfour\n")
	git(t, w.Dir, "commit", "-q", "-am", "change a")
	write(t, filepath.Join(w.Dir, "b.txt"), "new\n")
	s, err := Summarize(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Commits) != 1 || s.Commits[0] != "change a" || s.Files != 1 || s.Added != 2 || s.Removed != 1 || s.Uncommitted != 1 || !s.Unmerged() {
		t.Fatalf("%+v", s)
	}
}

func TestMergeSquash(t *testing.T) {
	dir := repo(t)
	w := add(t, dir, "x")
	ctx := context.Background()
	write(t, filepath.Join(w.Dir, "a.txt"), "one\n2\nthree\n")
	git(t, w.Dir, "commit", "-q", "-am", "two")
	write(t, filepath.Join(w.Dir, "b.txt"), "uncommitted\n")
	if err := Merge(ctx, w, "Do x", true); err != nil {
		t.Fatal(err)
	}
	clean(t, dir)
	if log := git(t, dir, "log", "--format=%s"); log != "Do x\nfirst" {
		t.Fatalf("log:\n%s", log)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "b.txt")); string(b) != "uncommitted\n" {
		t.Fatal("uncommitted work didn't come along")
	}

	// Brought up to date, the branch merges again with only what's new.
	if err := Update(ctx, w, "wip"); err != nil {
		t.Fatal(err)
	}
	if s, _ := Summarize(ctx, w); s.Unmerged() {
		t.Fatalf("after the update: %+v", s)
	}
	write(t, filepath.Join(w.Dir, "c.txt"), "more\n")
	if err := Merge(ctx, w, "More x", true); err != nil {
		t.Fatal(err)
	}
	clean(t, dir)
	if n := git(t, dir, "rev-list", "--count", "HEAD"); n != "3" {
		t.Fatalf("%s commits", n)
	}
}

func TestMergeCommit(t *testing.T) {
	dir := repo(t)
	w := add(t, dir, "x")
	write(t, filepath.Join(w.Dir, "b.txt"), "b\n")
	git(t, w.Dir, "add", ".")
	git(t, w.Dir, "commit", "-q", "-m", "add b")
	if err := Merge(context.Background(), w, "unused", false); err != nil {
		t.Fatal(err)
	}
	clean(t, dir)
	if parents := strings.Fields(git(t, dir, "log", "-1", "--format=%p")); len(parents) != 2 {
		t.Fatalf("not a merge commit: %v", parents)
	}
}

// A worktree made from another merges into that one, where its base is
// checked out.
func TestMergeIntoWorktree(t *testing.T) {
	dir := repo(t)
	w := add(t, dir, "w")
	v := add(t, w.Dir, "v")
	write(t, filepath.Join(v.Dir, "v.txt"), "v\n")
	if err := Merge(context.Background(), v, "Do v", true); err != nil {
		t.Fatal(err)
	}
	clean(t, dir)
	if log := git(t, w.Dir, "log", "-1", "--format=%s"); log != "Do v" {
		t.Fatalf("w's log: %s", log)
	}
	if v.Start() != v.Dir {
		t.Fatalf("Start %s", v.Start())
	}
}

func TestMergeRefuses(t *testing.T) {
	dir := repo(t)
	w := add(t, dir, "x")
	ctx := context.Background()
	if err := Merge(ctx, w, "m", true); err == nil || !strings.Contains(err.Error(), "nothing to merge") {
		t.Errorf("nothing: %v", err)
	}
	write(t, filepath.Join(w.Dir, "b.txt"), "b\n")

	write(t, filepath.Join(dir, "a.txt"), "staged\n")
	git(t, dir, "add", "a.txt")
	if err := Merge(ctx, w, "m", true); err == nil || !strings.Contains(err.Error(), "staged changes") {
		t.Errorf("staged: %v", err)
	}
	git(t, dir, "reset", "-q", "--hard")

	git(t, dir, "switch", "-q", "-c", "other")
	if err := Merge(ctx, w, "m", true); err == nil || !strings.Contains(err.Error(), "is on other, not main") {
		t.Errorf("other branch: %v", err)
	}
}

// A merge that would conflict isn't made: the main checkout stays as it
// was, and the conflict is resolved in the worktree after an update.
func TestMergeConflict(t *testing.T) {
	dir := repo(t)
	w := add(t, dir, "x")
	ctx := context.Background()
	write(t, filepath.Join(dir, "a.txt"), "one\nTWO\nthree\n")
	git(t, dir, "commit", "-q", "-am", "main's")
	write(t, filepath.Join(w.Dir, "a.txt"), "one\n2\nthree\n")
	for _, squash := range []bool{true, false} {
		var conflict *ConflictError
		if err := Merge(ctx, w, "m", squash); !errors.As(err, &conflict) || len(conflict.Files) != 1 || conflict.Files[0] != "a.txt" {
			t.Fatalf("squash %v: %v", squash, err)
		}
		clean(t, dir)
	}
	var conflict *ConflictError
	if err := Update(ctx, w, "wip"); !errors.As(err, &conflict) || conflict.Error() != "conflicts with main in a.txt" {
		t.Fatalf("update: %v", err)
	}
	if git(t, w.Dir, "rev-parse", "-q", "--verify", "MERGE_HEAD") == "" {
		t.Fatal("the conflict isn't left in the worktree")
	}
}

// The user's own changes in the main checkout, which a merge would
// overwrite, stop it, and are left as they were.
func TestMergeKeepsLocalChanges(t *testing.T) {
	dir := repo(t)
	w := add(t, dir, "x")
	write(t, filepath.Join(w.Dir, "a.txt"), "one\n2\nthree\n")
	write(t, filepath.Join(dir, "a.txt"), "mine\n")
	for _, squash := range []bool{true, false} {
		if err := Merge(context.Background(), w, "m", squash); err == nil {
			t.Fatalf("squash %v: merged over local changes", squash)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "mine\n" {
			t.Fatalf("squash %v: local change now %q", squash, b)
		}
		if s := git(t, dir, "status", "--porcelain"); s != "M a.txt" { // unstaged, the line trimmed
			t.Fatalf("squash %v: status %q", squash, s)
		}
	}
}

func TestRemove(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	w := add(t, dir, "x")
	write(t, filepath.Join(w.Dir, "a.txt"), "changed\n")
	write(t, filepath.Join(w.Dir, "new.txt"), "new\n")
	if err := Remove(ctx, w); err != nil {
		t.Fatal(err)
	}
	gone := func(w Info) {
		t.Helper()
		if _, err := os.Stat(w.Dir); err == nil {
			t.Error("the folder is left")
		}
		if b := git(t, dir, "branch", "--list", w.Branch); b != "" {
			t.Errorf("the branch is left: %s", b)
		}
		if l := git(t, dir, "worktree", "list"); strings.Contains(l, w.Dir) {
			t.Errorf("git still lists it:\n%s", l)
		}
	}
	gone(w)

	// A folder deleted already: git forgets it, and the branch goes.
	v := add(t, dir, "v")
	os.RemoveAll(v.Dir)
	if err := Remove(ctx, v); err != nil {
		t.Fatal(err)
	}
	gone(v)
}

func TestValidBranch(t *testing.T) {
	for name, ok := range map[string]bool{
		"fix-login": true, "feat/new-ui": true, "v1.2": true, "café": true,
		"": false, "a b": false, "-x": false, "HEAD": false, "@": false, "a..b": false, "a~1": false,
		"a^": false, "a:b": false, "a?": false, "a*": false, "a[": false, `a\b`: false, "a/": false,
		"/a": false, "a//b": false, "a.": false, ".a": false, "a/.b": false, "a.lock": false,
		"a/b.lock/c": false, "a@{b": false, "a\x01": false,
	} {
		if got := ValidBranch(name) == nil; got != ok {
			t.Errorf("%q: valid %v, want %v", name, got, ok)
		}
		// git agrees.
		if name != "" {
			err := exec.Command("git", "check-ref-format", "--branch", name).Run()
			if git := err == nil; git != ok && name != "HEAD" && name != "@" { // both mean HEAD to git
				t.Errorf("%q: git says valid %v", name, git)
			}
		}
	}
}
