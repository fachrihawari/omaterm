// Package worktree gives an agent a branch and a checkout of its own: a
// git worktree in the repository's .worktrees folder, which the user then
// merges into the branch it came from, or discards. Unlike gitsnap, it
// changes repositories; the main checkout is never left mid-merge, as its
// conflicts are resolved in the worktree.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"omaterm/internal/gitsnap"
)

// Folder is where a repository's worktrees go, in its main checkout.
const Folder = ".worktrees"

// Info is a worktree omaterm made.
type Info struct {
	Main   string // the main checkout's top level
	Dir    string // the worktree's top level
	Branch string // its branch
	Base   string // the branch it was made from, which it merges into
	// Rel is the project's folder within its checkout, "" at its top: a
	// session starts in the same folder of the worktree.
	Rel string
	// Reused is set when the branch's worktree was there already.
	Reused bool
}

// Of returns the worktree at dir, which is in Folder of its main checkout.
func Of(dir, branch, base string) Info {
	return Info{Main: filepath.Dir(filepath.Dir(dir)), Dir: dir, Branch: branch, Base: base}
}

// Start is the folder a session in the worktree starts in: the project's
// folder in it, else its top level when the branch hasn't that folder.
func (w Info) Start() string {
	if w.Rel != "" {
		if fi, err := os.Stat(filepath.Join(w.Dir, w.Rel)); err == nil && fi.IsDir() {
			return filepath.Join(w.Dir, w.Rel)
		}
	}
	return w.Dir
}

// Add makes a worktree for branch beside the checkout projectDir is in:
// on a new branch from base, or on branch when it exists. base "" is the
// branch checked out there. A worktree of branch already in Folder is
// returned as it is. With copyEnv, the checkout's ignored .env files are
// copied into it, so its programs run as they do there.
func Add(ctx context.Context, projectDir, branch, base string, copyEnv bool) (Info, error) {
	if err := ValidBranch(branch); err != nil {
		return Info{}, err
	}
	r, err := gitsnap.Open(ctx, projectDir)
	if err != nil {
		if errors.Is(err, gitsnap.ErrNoGit) {
			return Info{}, err
		}
		return Info{}, fmt.Errorf("%s is not in a git repository", filepath.Base(projectDir))
	}
	list, err := worktrees(ctx, r.Root)
	if err != nil {
		return Info{}, err
	}
	if len(list) == 0 || list[0].bare {
		return Info{}, errors.New("the repository has no checkout to work beside")
	}
	main := list[0].path
	info := Info{Main: main, Branch: branch}
	if rel, err := filepath.Rel(r.Root, projectDir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		info.Rel = rel
	}
	if ok, _ := test(ctx, main, "rev-parse", "--verify", "-q", "HEAD"); !ok {
		return Info{}, errors.New("the repository has no commits yet")
	}
	if base == "" {
		base, _ = out(ctx, r.Root, "branch", "--show-current")
		if base == "" {
			base = r.DefaultBranch()
		}
		if base == "" {
			return Info{}, errors.New("choose the branch to start from")
		}
	}
	if ok, _ := test(ctx, main, "rev-parse", "--verify", "-q", "refs/heads/"+base); !ok {
		return Info{}, fmt.Errorf("there is no branch %s", base)
	}
	info.Base = base

	if err := do(ctx, main, "worktree", "prune"); err != nil {
		return Info{}, err
	}
	if list, err = worktrees(ctx, main); err != nil {
		return Info{}, err
	}
	for _, w := range list {
		if w.branch != branch {
			continue
		}
		if filepath.Dir(w.path) != filepath.Join(main, Folder) {
			return Info{}, fmt.Errorf("%s is checked out in %s", branch, Tilde(w.path))
		}
		info.Dir, info.Reused = w.path, true
		return info, Exclude(ctx, main)
	}
	if err := Exclude(ctx, main); err != nil {
		return Info{}, err
	}

	name := strings.ReplaceAll(branch, "/", "-")
	info.Dir = filepath.Join(main, Folder, name)
	for i := 2; exists(info.Dir); i++ {
		info.Dir = filepath.Join(main, Folder, name+"-"+strconv.Itoa(i))
	}
	if has, _ := test(ctx, main, "rev-parse", "--verify", "-q", "refs/heads/"+branch); has {
		err = do(ctx, main, "worktree", "add", info.Dir, branch)
	} else {
		err = do(ctx, main, "worktree", "add", "-b", branch, info.Dir, base)
	}
	if err != nil {
		return Info{}, err
	}
	if copyEnv {
		CopyEnv(ctx, r.Root, info.Dir)
	}
	return info, nil
}

// entry is a worktree of git worktree list.
type entry struct {
	path, branch string
	bare         bool
}

// worktrees lists the repository's worktrees, the main checkout first.
func worktrees(ctx context.Context, dir string) ([]entry, error) {
	s, err := out(ctx, dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var list []entry
	for _, f := range strings.Split(s, "\x00") {
		switch k, v, _ := strings.Cut(f, " "); k {
		case "worktree":
			list = append(list, entry{path: filepath.Clean(v)})
		case "branch":
			if len(list) > 0 {
				list[len(list)-1].branch = strings.TrimPrefix(v, "refs/heads/")
			}
		case "bare":
			if len(list) > 0 {
				list[len(list)-1].bare = true
			}
		}
	}
	return list, nil
}

// Exclude keeps Folder out of the main checkout's status, through the
// repository's info/exclude rather than a .gitignore the user commits.
// Unless it does, the main checkout shows the worktrees as files to add.
func Exclude(ctx context.Context, main string) error {
	if ok, err := test(ctx, main, "check-ignore", "-q", Folder+"/"); err != nil || ok {
		return err
	}
	common, err := out(ctx, main, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	path := filepath.Join(common, "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	line := "/" + Folder + "/\n"
	if len(b) > 0 && b[len(b)-1] != '\n' {
		line = "\n" + line
	}
	_, err = f.WriteString(line)
	return err
}

// CopyEnv copies the .env files git ignores in the checkout src into the
// worktree dst, where they are missing, and returns how many it copied.
// Files git doesn't ignore are left: a commit there would take them.
func CopyEnv(ctx context.Context, src, dst string) int {
	s, err := out(ctx, src, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return 0
	}
	n := 0
	for _, rel := range strings.Split(s, "\x00") {
		if rel == "" || strings.HasSuffix(rel, "/") || strings.HasPrefix(rel, Folder+"/") ||
			!strings.HasPrefix(filepath.Base(rel), ".env") {
			continue
		}
		from, to := filepath.Join(src, rel), filepath.Join(dst, rel)
		fi, err := os.Lstat(from)
		if err != nil || !fi.Mode().IsRegular() || exists(to) {
			continue
		}
		if copyFile(from, to, fi.Mode().Perm()) == nil {
			n++
		}
	}
	return n
}

func copyFile(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, in); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Summary is what a worktree has that its base doesn't.
type Summary struct {
	Commits []string // its commits' subjects, oldest first
	// Files changed in those commits, and the lines they add and remove.
	Files, Added, Removed int
	// Uncommitted is the number of files changed and not committed.
	Uncommitted int
}

// Unmerged reports whether merging would bring anything into the base.
func (s Summary) Unmerged() bool { return s.Files > 0 || s.Uncommitted > 0 }

// Summarize returns what the worktree w has that its base doesn't.
func Summarize(ctx context.Context, w Info) (Summary, error) {
	var s Summary
	status, err := out(ctx, w.Dir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return s, err
	}
	for _, f := range strings.Split(status, "\x00") {
		// A rename's second field is its old name, without a status.
		if len(f) > 3 && f[2] == ' ' {
			s.Uncommitted++
		}
	}
	base, err := out(ctx, w.Dir, "merge-base", "refs/heads/"+w.Base, "HEAD")
	if err != nil {
		return s, err
	}
	log, err := out(ctx, w.Dir, "log", "--reverse", "--no-merges", "--format=%s", base+"..HEAD")
	if err != nil {
		return s, err
	}
	if log != "" {
		s.Commits = strings.Split(log, "\n")
	}
	stat, err := out(ctx, w.Dir, "diff", "--numstat", base, "HEAD")
	if err != nil {
		return s, err
	}
	for _, l := range strings.Split(stat, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 {
			continue
		}
		s.Files++
		a, _ := strconv.Atoi(f[0]) // "-" for a binary file
		r, _ := strconv.Atoi(f[1])
		s.Added += a
		s.Removed += r
	}
	return s, nil
}

// CommitPending commits every change in the worktree at dir, new files
// too, with msg; it does nothing when there is none. Hooks don't run: a
// worktree lacks what they need, such as node_modules, and the commit
// into the base runs them.
func CommitPending(ctx context.Context, dir, msg string) error {
	status, err := out(ctx, dir, "status", "--porcelain")
	if err != nil || status == "" {
		return err
	}
	if err := do(ctx, dir, "add", "--all"); err != nil {
		return err
	}
	return do(ctx, dir, "commit", "-q", "--no-verify", "-m", msg)
}

// ConflictError is a merge that would conflict, with the files it would
// conflict in.
type ConflictError struct {
	Base  string
	Files []string
}

func (e *ConflictError) Error() string {
	if len(e.Files) == 1 {
		return fmt.Sprintf("conflicts with %s in %s", e.Base, e.Files[0])
	}
	return fmt.Sprintf("conflicts with %s in %d files", e.Base, len(e.Files))
}

// Merge brings the worktree's work into its base, in the checkout of the
// base (the main one, usually): as one commit with msg when squash, else
// as a merge commit, its pending changes committed first with msg. That
// checkout must have nothing staged. A merge that would conflict is not
// made, and returns a *ConflictError: Update resolves it in the worktree.
func Merge(ctx context.Context, w Info, msg string, squash bool) error {
	into, err := checkout(ctx, w)
	if err != nil {
		return err
	}
	name := filepath.Base(into)
	if r, err := gitsnap.Open(ctx, into); err != nil {
		return err
	} else if state := midway(r.GitDir); state != "" {
		return fmt.Errorf("%s is in the middle of a %s: finish it first", name, state)
	}
	if same, err := test(ctx, into, "diff", "--cached", "--quiet"); err != nil {
		return err
	} else if !same {
		return fmt.Errorf("%s has staged changes: commit or unstage them first", name)
	}
	if err := CommitPending(ctx, w.Dir, msg); err != nil {
		return err
	}
	if same, err := test(ctx, into, "diff", "--quiet", w.Base+"..."+w.Branch); err != nil {
		return err
	} else if same {
		return fmt.Errorf("%s has nothing to merge into %s", w.Branch, w.Base)
	}
	if files, err := conflicts(ctx, into, w); err != nil {
		return err
	} else if len(files) > 0 {
		return &ConflictError{Base: w.Base, Files: files}
	}
	if squash {
		if err := do(ctx, into, "merge", "--squash", w.Branch); err != nil {
			return undo(ctx, into, w.Base, err)
		}
		if err := do(ctx, into, "commit", "-q", "-m", msg); err != nil {
			return undo(ctx, into, w.Base, err)
		}
		return nil
	}
	if err := do(ctx, into, "merge", "--no-ff", "--no-edit", w.Branch); err != nil {
		return undo(ctx, into, w.Base, err)
	}
	return nil
}

// checkout returns the checkout the worktree's base is checked out in.
func checkout(ctx context.Context, w Info) (string, error) {
	list, err := worktrees(ctx, w.Main)
	if err != nil {
		return "", err
	}
	for _, e := range list {
		if e.branch == w.Base {
			return e.path, nil
		}
	}
	cur, _ := out(ctx, w.Main, "branch", "--show-current")
	if cur == "" {
		cur = "a detached HEAD"
	}
	return "", fmt.Errorf("%s is on %s, not %s: switch it first", filepath.Base(w.Main), cur, w.Base)
}

// conflicts returns the files merging the worktree's branch into its base
// would conflict in, without touching either: none when git is too old to
// tell, as merge itself then finds them.
func conflicts(ctx context.Context, into string, w Info) ([]string, error) {
	s, err := out(ctx, into, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", w.Base, w.Branch)
	if err == nil {
		return nil, nil
	}
	var files []string
	for i, f := range strings.Split(s, "\x00") {
		if i > 0 && f != "" { // the first is the tree's id
			files = append(files, f)
		}
	}
	return files, nil // none: git before 2.38, without --write-tree
}

// undo puts the checkout into back as it was before a merge that failed,
// and returns its conflict, else err.
func undo(ctx context.Context, into, base string, err error) error {
	files, _ := out(ctx, into, "diff", "--name-only", "--diff-filter=U")
	do(ctx, into, "reset", "-q", "--merge")
	if files != "" {
		return &ConflictError{Base: base, Files: strings.Split(files, "\n")}
	}
	return err
}

// midway returns the operation a worktree's git directory is in the middle
// of, "" when none.
func midway(gitDir string) string {
	for _, s := range []struct{ file, what string }{
		{"MERGE_HEAD", "merge"}, {"rebase-merge", "rebase"}, {"rebase-apply", "rebase"},
		{"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"},
	} {
		if exists(filepath.Join(gitDir, s.file)) {
			return s.what
		}
	}
	return ""
}

// MergeMessage is the message of a squash of a worktree's work: its one
// commit's, or the branch's with its commits' as the body.
func MergeMessage(branch string, s Summary) string {
	switch len(s.Commits) {
	case 0:
		return branch
	case 1:
		return s.Commits[0]
	}
	msg := branch + "\n"
	for _, c := range s.Commits {
		msg += "\n* " + c
	}
	return msg
}

// ForLead words a failed merge of worker name's worktree, into base, for
// its lead's agent: a conflict is the worker's to resolve.
func ForLead(err error, name, base string) error {
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		return fmt.Errorf("%s %s: ask it to merge %s into its branch and resolve that, then merge again", name, conflict.Error(), base)
	}
	return err
}

// MergeWork merges w into its base as omaterm merge does: with msg, else
// MergeMessage's. A squash of a worktree that stays brings its base back
// into it, for the next squash to be clean. It returns the subjects of
// the commits it brought.
func MergeWork(ctx context.Context, w Info, msg string, squash, keep bool) ([]string, error) {
	s, err := Summarize(ctx, w)
	if err != nil {
		return nil, err
	}
	if msg == "" {
		msg = MergeMessage(w.Branch, s)
	}
	if err := Merge(ctx, w, msg, squash); err != nil {
		return nil, err
	}
	if squash && keep {
		if err := Update(ctx, w, msg); err != nil {
			return s.Commits, err
		}
	}
	return s.Commits, nil
}

// Update merges the base into the worktree's branch, its pending changes
// committed first with msg. A conflict is left in the worktree, to be
// resolved there; it returns a *ConflictError.
func Update(ctx context.Context, w Info, msg string) error {
	if err := CommitPending(ctx, w.Dir, msg); err != nil {
		return err
	}
	err := do(ctx, w.Dir, "merge", "--no-edit", w.Base)
	if err == nil {
		return nil
	}
	if files, _ := out(ctx, w.Dir, "diff", "--name-only", "--diff-filter=U"); files != "" {
		return &ConflictError{Base: w.Base, Files: strings.Split(files, "\n")}
	}
	return err
}

// Remove deletes the worktree, with what it has not committed, and its
// branch.
func Remove(ctx context.Context, w Info) error {
	if exists(w.Dir) {
		if err := do(ctx, w.Main, "worktree", "remove", "--force", w.Dir); err != nil {
			return err
		}
	} else if err := do(ctx, w.Main, "worktree", "prune"); err != nil {
		return err
	}
	if has, _ := test(ctx, w.Main, "rev-parse", "--verify", "-q", "refs/heads/"+w.Branch); has {
		return do(ctx, w.Main, "branch", "-D", w.Branch)
	}
	return nil
}

// ghPath finds the GitHub command, "" when it isn't installed.
var ghPath = func() string {
	p, _ := exec.LookPath("gh")
	return p
}

// PR pushes the worktree's branch to origin, then opens a pull request of
// it into its base with the GitHub command, and returns the request's
// page: the one there already, if any. Without the command, or when it
// fails, it is the page that opens one, for a remote on GitHub, else "".
// Only what is committed goes: uncommitted files are refused.
func PR(ctx context.Context, w Info) (string, error) {
	if status, err := out(ctx, w.Dir, "status", "--porcelain"); err != nil {
		return "", err
	} else if status != "" {
		return "", fmt.Errorf("%s has uncommitted files: commit them first", w.Branch)
	}
	remote, err := out(ctx, w.Dir, "remote", "get-url", "origin")
	if err != nil {
		return "", errors.New("the repository has no origin to push to")
	}
	if err := do(ctx, w.Dir, "push", "-q", "-u", "origin", w.Branch); err != nil {
		return "", err
	}
	if gh := ghPath(); gh != "" {
		cmd := exec.CommandContext(ctx, gh, "pr", "create", "--fill", "--base", w.Base, "--head", w.Branch)
		cmd.Dir = w.Dir
		cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
		hide(cmd)
		// What it made, or, when one is there already, that one.
		b, _ := cmd.CombinedOutput()
		for _, f := range strings.Fields(string(b)) {
			if strings.HasPrefix(f, "https://") {
				return f, nil
			}
		}
	}
	return CompareURL(remote, w.Base, w.Branch), nil
}

// CompareURL is the page opening a pull request of branch into base, for
// a remote on GitHub (https, ssh, or scp-like), "" for others.
func CompareURL(remote, base, branch string) string {
	path := ""
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "ssh://git@github.com/", "git@github.com:"} {
		if rest, ok := strings.CutPrefix(remote, prefix); ok {
			path = rest
		}
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	if strings.Count(path, "/") != 1 {
		return ""
	}
	esc := func(ref string) string {
		parts := strings.Split(ref, "/")
		for i, p := range parts {
			parts[i] = url.PathEscape(p)
		}
		return strings.Join(parts, "/")
	}
	return "https://github.com/" + path + "/compare/" + esc(base) + "..." + esc(branch) + "?expand=1"
}

// ValidBranch says what is wrong with a branch's name, nil when nothing:
// git's rules for one (git check-ref-format --branch).
func ValidBranch(name string) error {
	switch {
	case name == "":
		return errors.New("name the branch")
	case strings.ContainsAny(name, " \t"):
		return errors.New("a branch's name has no spaces")
	case name == "@" || name == "HEAD" || strings.HasPrefix(name, "-"):
		return fmt.Errorf("a branch can't be named %s", name)
	}
	if strings.ContainsAny(name, "~^:?*[\\") || strings.Contains(name, "..") || strings.Contains(name, "@{") ||
		strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") ||
		strings.Contains(name, "//") {
		return fmt.Errorf("a branch can't be named %s", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("a branch can't be named %q", name)
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return fmt.Errorf("a branch can't be named %s", name)
		}
	}
	return nil
}

// Tilde shortens a path in the home folder to ~/….
func Tilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		if rel == "." {
			return "~"
		}
		return "~" + string(filepath.Separator) + rel
	}
	return path
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
