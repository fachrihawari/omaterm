package service

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// GitFile is one changed file in a repo's status.
type GitFile struct {
	Path   string // repo-relative path
	Status string // M, A, D, U(ntracked), R(enamed)
	Staged bool
}

// Stash is one stash entry.
type Stash struct {
	Index   int
	Ref     string // stash@{0}
	Hash    string // its commit: which stash it is, wherever the list moved it
	Message string
	Branch  string
	When    time.Time
}

// Commit is one commit from git log.
type Commit struct {
	Hash    string
	Short   string
	Parents []string
	Subject string
	Author  string
	Time    time.Time
	Refs    []string // branch/tag/HEAD decorations
}

// CommitFile is one file changed in a commit, with its status (M/A/D/R/C).
type CommitFile struct {
	Status string // M, A, D, R, C, etc.
	Path   string
}

// Repo describes one discovered git repository and its status.
type Repo struct {
	Root    string // absolute path of the worktree root
	Name    string // basename of Root
	RelPath string // path relative to the workspace root, "." for the root itself
	Branch  string
	Head    string // short HEAD sha
	Ahead   int
	Behind  int
	Staged  []GitFile
	Changes []GitFile
	Stashes []Stash
	// HasRemote is whether the repo has any remote configured, so the
	// panel can gray out fetch, pull and push without running git itself.
	HasRemote bool
}

// gitCmd runs git in root. It never takes the index's optional lock, so
// an agent committing beside the panel never finds it held, and never asks
// for a password.
func gitCmd(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"--no-optional-locks"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	if root != "" {
		cmd.Dir = root
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// gitDo runs a git command that changes something; its error carries what
// git said: "exit status 1" told nothing of a failed hook or a missing
// user.email.
func gitDo(root string, args ...string) error {
	out, err := gitCmd(root, args...)
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(out); msg != "" {
		lines := strings.Split(msg, "\n")
		if len(lines) > 6 {
			lines = lines[len(lines)-6:] // a hook's output ends with its verdict
		}
		return errors.New(strings.Join(lines, "\n"))
	}
	return err
}

// IsRepoRoot reports whether dir is inside a work tree whose toplevel is dir.
func IsRepoRoot(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	top, err := gitCmd(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	// Resolve symlinks on both sides before comparing.
	if realTop, err := filepath.EvalSymlinks(top); err == nil {
		top = realTop
	}
	if realAbs, err := filepath.EvalSymlinks(abs); err == nil {
		abs = realAbs
	}
	return top == abs
}

// CurrentBranch returns the current branch name, or "HEAD" when detached.
func CurrentBranch(root string) string {
	b, err := gitCmd(root, "branch", "--show-current")
	if err != nil || b == "" {
		return "HEAD"
	}
	return b
}

// HeadSha returns the short HEAD sha.
func HeadSha(root string) string {
	s, err := gitCmd(root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return s
}

// Ignored returns what git ignores under dir, as slash paths relative to
// it; a folder ignored whole is one entry, not its files. Outside a repo,
// or without git, it is nil.
func Ignored(dir string) map[string]bool {
	out, err := gitOut(dir, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return nil
	}
	ignored := map[string]bool{}
	for _, p := range strings.Split(out, "\x00") {
		if p = strings.TrimSuffix(p, "/"); p != "" {
			ignored[p] = true
		}
	}
	return ignored
}

// AheadBehind returns commits ahead/behind the upstream.
func AheadBehind(root string) (ahead, behind int) {
	out, err := gitCmd(root, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		return 0, 0
	}
	parts := strings.Fields(out)
	if len(parts) == 2 {
		ahead, _ = strconv.Atoi(parts[0])
		behind, _ = strconv.Atoi(parts[1])
	}
	return
}

// Status runs git status and returns staged + unstaged/untracked files.
// Its paths are as they are (-z): quoted, a name with a space or an
// accent wasn't found by Stage or Discard. Only what git prints on its
// output is read: a warning on its errors was taken for a file.
func Status(root string) (staged, changes []GitFile, err error) {
	out, err := gitOut(root, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return nil, nil, err
	}
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		x, y, path := e[0], e[1], e[3:]
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			i++ // the path it was renamed or copied from follows
		}
		if conflicted(x, y) {
			// Unmerged: one row, to resolve, not a staged and an
			// untracked one (it read as untracked, and Discard did
			// nothing).
			changes = append(changes, GitFile{Path: path, Status: "!"})
			continue
		}
		if x != ' ' && x != '?' {
			staged = append(staged, GitFile{Path: path, Status: statusLetter(x), Staged: true})
		}
		if y != ' ' || x == '?' {
			st := statusLetter(y)
			if x == '?' {
				st = "U"
			}
			changes = append(changes, GitFile{Path: path, Status: st})
		}
	}
	return staged, changes, nil
}

// conflicted tells an unmerged path's XY (git status's man page).
func conflicted(x, y byte) bool {
	return x == 'U' || y == 'U' || x == 'A' && y == 'A' || x == 'D' && y == 'D'
}

// gitOut runs a git command for its output alone, not its warnings.
func gitOut(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"--no-optional-locks"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", errors.New(strings.TrimSpace(string(exit.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

func statusLetter(b byte) string {
	switch b {
	case 'M':
		return "M"
	case 'A':
		return "A"
	case 'D':
		return "D"
	case 'R':
		return "R"
	case 'C':
		return "C"
	case '?':
		return "U"
	}
	return string(b)
}

// RepoStatus builds a full Repo snapshot for root.
func RepoStatus(root, workspace string) (*Repo, error) {
	r := &Repo{Root: root, Name: baseName(root)}
	r.RelPath = relPath(workspace, root)
	r.Branch = CurrentBranch(root)
	r.Head = HeadSha(root)
	r.Ahead, r.Behind = AheadBehind(root)
	staged, changes, err := Status(root)
	if err != nil {
		return nil, err
	}
	r.Staged = staged
	r.Changes = changes
	stashes, _ := StashList(root)
	r.Stashes = stashes
	r.HasRemote = HasRemote(root)
	return r, nil
}

// HasRemote reports whether the repo has any remote configured.
func HasRemote(root string) bool {
	out, err := gitCmd(root, "remote")
	return err == nil && strings.TrimSpace(out) != ""
}

// Fetch fetches from the remote, pruning branches that are gone.
func Fetch(root string) (string, error) {
	return gitCmd(root, "fetch", "--prune")
}

// HasUpstream reports whether the current branch tracks a remote branch.
func HasUpstream(root string) bool {
	_, err := gitCmd(root, "rev-parse", "--abbrev-ref", "--verify", "--quiet", "@{upstream}")
	return err == nil
}

// Pull updates the current branch from its upstream, fast-forward only so
// a pull never starts a merge the user did not ask for. A branch with no
// upstream yet is pulled from origin instead, so a first pull works.
func Pull(root string) (string, error) {
	if !HasUpstream(root) {
		return gitCmd(root, "pull", "--ff-only", "origin", "HEAD")
	}
	return gitCmd(root, "pull", "--ff-only")
}

// Push pushes the current branch. A branch with no upstream yet gets one
// at origin, which is what a first push from the panel means.
func Push(root string) (string, error) {
	if !HasUpstream(root) {
		return gitCmd(root, "push", "--set-upstream", "origin", "HEAD")
	}
	return gitCmd(root, "push")
}

// Stage stages path.
func Stage(root, path string) error {
	return gitDo(root, "add", "--", path)
}

// StageAll stages all changes.
func StageAll(root string) error {
	return gitDo(root, "add", "-A")
}

// Unstage unstages path.
func Unstage(root, path string) error {
	return gitDo(root, "reset", "-q", "--", path)
}

// UnstageAll unstages everything.
func UnstageAll(root string) error {
	return gitDo(root, "reset", "-q")
}

// Discard discards working-tree changes of path (or deletes untracked files).
func Discard(root, path string, untracked bool) error {
	if untracked {
		// -d: an untracked folder shows as one entry, which plain clean left.
		return gitDo(root, "clean", "-fd", "--", path)
	}
	return gitDo(root, "checkout", "--", path)
}

// CommitStaged commits the staged changes with message.
func CommitStaged(root, message string) error {
	return gitDo(root, "commit", "-m", message)
}

// StashList lists stashes newest first.
func StashList(root string) ([]Stash, error) {
	out, err := gitCmd(root, "stash", "list", "--format=%gd%x1f%gs%x1f%ct%x1f%H")
	if err != nil || out == "" {
		return nil, err
	}
	var stashes []Stash
	for i, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\x1f", 4)
		if len(parts) < 4 {
			continue
		}
		sec, _ := strconv.ParseInt(parts[2], 10, 64)
		msg := parts[1]
		branch := ""
		// gs looks like "WIP on main: abc123 subject" or "On main: subject"
		if strings.HasPrefix(msg, "WIP on ") {
			rest := strings.TrimPrefix(msg, "WIP on ")
			if i := strings.Index(rest, ":"); i >= 0 {
				branch = rest[:i]
			}
		} else if strings.HasPrefix(msg, "On ") {
			rest := strings.TrimPrefix(msg, "On ")
			if i := strings.Index(rest, ":"); i >= 0 {
				branch = rest[:i]
			}
		}
		stashes = append(stashes, Stash{
			Index:   i,
			Ref:     parts[0],
			Hash:    parts[3],
			Message: msg,
			Branch:  branch,
			When:    time.Unix(sec, 0),
		})
	}
	return stashes, nil
}

// StashPush creates a stash.
func StashPush(root, message string, includeUntracked bool) error {
	args := []string{"stash", "push"}
	if includeUntracked {
		args = append(args, "--include-untracked")
	}
	if message != "" {
		args = append(args, "-m", message)
	}
	return gitDo(root, args...)
}

// StashApply applies stash without dropping it.
func StashApply(root string, index int) error {
	return gitDo(root, "stash", "apply", "stash@{"+strconv.Itoa(index)+"}")
}

// StashPop applies and drops the stash.
func StashPop(root string, index int) error {
	return gitDo(root, "stash", "pop", "stash@{"+strconv.Itoa(index)+"}")
}

// StashDrop drops the stash.
func StashDrop(root string, index int) error {
	return gitDo(root, "stash", "drop", "stash@{"+strconv.Itoa(index)+"}")
}

// StashShow returns the patch of a stash.
func StashShow(root string, index int) (string, error) {
	return gitCmd(root, "stash", "show", "-p", "stash@{"+strconv.Itoa(index)+"}")
}

// Show returns the content of path at the given revision ("" for empty).
func Show(root, rev, path string) (string, error) {
	return gitCmd(root, "show", rev+":"+path)
}

// ShowBytes returns the raw bytes of path at rev, for pictures: gitCmd
// trims trailing newlines, which would corrupt a bitmap. An empty rev
// reads the index.
func ShowBytes(root, rev, path string) ([]byte, error) {
	cmd := exec.Command("git", "show", rev+":"+path)
	if root != "" {
		cmd.Dir = root
	}
	return cmd.Output()
}

// Log returns commits newest first, all branches, with decorations.
func Log(root string, limit int) ([]Commit, error) {
	args := []string{
		"log", "--all", "--topo-order", "--date-order",
		"--decorate=short",
		"--format=%H%x1f%h%x1f%P%x1f%s%x1f%an%x1f%ct%x1f%D",
	}
	if limit > 0 {
		args = append(args, "-n", strconv.Itoa(limit))
	}
	out, err := gitCmd(root, args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\x1f", 7)
		if len(parts) < 7 {
			continue
		}
		sec, _ := strconv.ParseInt(parts[5], 10, 64)
		c := Commit{
			Hash:    parts[0],
			Short:   parts[1],
			Subject: parts[3],
			Author:  parts[4],
			Time:    time.Unix(sec, 0),
		}
		if parts[2] != "" {
			c.Parents = strings.Fields(parts[2])
		}
		if parts[6] != "" {
			for _, d := range strings.Split(parts[6], ", ") {
				d = strings.TrimPrefix(d, "HEAD -> ")
				c.Refs = append(c.Refs, d)
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// HeadVersion returns the file content at HEAD, "" when untracked/new.
func HeadVersion(root, path string) (string, error) {
	out, err := gitCmd(root, "show", "HEAD:"+path)
	if err != nil {
		return "", err
	}
	return out, nil
}

// HeadBytes returns the raw bytes of path at HEAD, for pictures.
func HeadBytes(root, path string) ([]byte, error) {
	return ShowBytes(root, "HEAD", path)
}

// IndexBytes returns the raw bytes of path in the index, for pictures.
func IndexBytes(root, path string) ([]byte, error) {
	return ShowBytes(root, "", path)
}

// IndexVersion returns the staged (index) content of path.
func IndexVersion(root, path string) (string, error) {
	out, err := gitCmd(root, "show", ":"+path)
	if err != nil {
		return "", err
	}
	return out, nil
}

// ShowCommitPatch returns the commit's patch against its first parent
// (or the empty tree for a root commit).
func ShowCommitPatch(root, hash string) (string, error) {
	return gitCmd(root, "show", "--format=", "--patch", hash)
}

// ShowCommitFilePatch returns the diff for a single file in a commit.
func ShowCommitFilePatch(root, hash, file string) (string, error) {
	return gitCmd(root, "show", "--format=", "--patch", hash, "--", file)
}

// CommitFiles returns the files changed in the given commit with their status.
func CommitFiles(root, hash string) ([]CommitFile, error) {
	out, err := gitCmd(root, "diff-tree", "--no-commit-id", "--name-status", "-r", "-M", hash)
	if err != nil {
		out, err = gitCmd(root, "show", "--name-status", "--format=", "-M", hash)
		if err != nil {
			return nil, err
		}
	}
	var files []CommitFile
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Split on tabs only: paths may contain spaces.
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		status := strings.TrimSpace(fields[0])
		// Normalize rename/copy scores: "R100" -> "R", "C075" -> "C".
		if len(status) > 1 {
			status = status[:1]
		}
		// For renames and copies the last field is the new path.
		path := strings.TrimSpace(fields[len(fields)-1])
		if path == "" {
			continue
		}
		files = append(files, CommitFile{Status: status, Path: path})
	}
	return files, nil
}

// CommitStats returns insertions and deletions for a commit.
func CommitStats(root, hash string) (additions, deletions int, err error) {
	out, err := gitCmd(root, "show", "--format=", "--stat", "--stat-width=200", hash)
	if err != nil {
		return 0, 0, err
	}
	// Parse the summary line: "N files changed, X insertions(+), Y deletions(-)"
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "insertion") || strings.Contains(line, "deletion") {
			// Parse numbers.
			for _, part := range strings.Split(line, ",") {
				part = strings.TrimSpace(part)
				if strings.Contains(part, "insertion") {
					numStr := strings.Fields(part)[0]
					n, _ := strconv.Atoi(numStr)
					additions = n
				}
				if strings.Contains(part, "deletion") {
					numStr := strings.Fields(part)[0]
					n, _ := strconv.Atoi(numStr)
					deletions = n
				}
			}
		}
	}
	return additions, deletions, nil
}

// ListBranches returns the list of local branches.
func ListBranches(root string) ([]string, error) {
	out, err := gitCmd(root, "branch", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			branches = append(branches, line)
		}
	}
	return branches, nil
}

// Checkout switches to the given branch.
func Checkout(root, branch string) (string, error) {
	return gitCmd(root, "checkout", branch)
}
