package service

import (
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

func gitCmd(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if root != "" {
		cmd.Dir = root
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
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
func Status(root string) (staged, changes []GitFile, err error) {
	out, err := gitCmd(root, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return nil, nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		x := line[0] // index status
		y := line[1] // worktree status
		path := strings.TrimSpace(line[3:])
		// Renames show as "old -> new"; keep the new path.
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
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
	case '?', 'U':
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
	_, err := gitCmd(root, "add", "--", path)
	return err
}

// StageAll stages all changes.
func StageAll(root string) error {
	_, err := gitCmd(root, "add", "-A")
	return err
}

// Unstage unstages path.
func Unstage(root, path string) error {
	_, err := gitCmd(root, "reset", "-q", "--", path)
	return err
}

// UnstageAll unstages everything.
func UnstageAll(root string) error {
	_, err := gitCmd(root, "reset", "-q")
	return err
}

// Discard discards working-tree changes of path (or deletes untracked files).
func Discard(root, path string, untracked bool) error {
	if untracked {
		_, err := gitCmd(root, "clean", "-f", "--", path)
		return err
	}
	_, err := gitCmd(root, "checkout", "--", path)
	return err
}

// CommitStaged commits the staged changes with message.
func CommitStaged(root, message string) error {
	_, err := gitCmd(root, "commit", "-m", message)
	return err
}

// StashList lists stashes newest first.
func StashList(root string) ([]Stash, error) {
	out, err := gitCmd(root, "stash", "list", "--format=%gd%x1f%gs%x1f%ct")
	if err != nil || out == "" {
		return nil, err
	}
	var stashes []Stash
	for i, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\x1f", 3)
		if len(parts) < 3 {
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
	_, err := gitCmd(root, args...)
	return err
}

// StashApply applies stash without dropping it.
func StashApply(root string, index int) error {
	_, err := gitCmd(root, "stash", "apply", "stash@{"+strconv.Itoa(index)+"}")
	return err
}

// StashPop applies and drops the stash.
func StashPop(root string, index int) error {
	_, err := gitCmd(root, "stash", "pop", "stash@{"+strconv.Itoa(index)+"}")
	return err
}

// StashDrop drops the stash.
func StashDrop(root string, index int) error {
	_, err := gitCmd(root, "stash", "drop", "stash@{"+strconv.Itoa(index)+"}")
	return err
}

// StashShow returns the patch of a stash.
func StashShow(root string, index int) (string, error) {
	return gitCmd(root, "stash", "show", "-p", "stash@{"+strconv.Itoa(index)+"}")
}

// Show returns the content of path at the given revision ("" for empty).
func Show(root, rev, path string) (string, error) {
	return gitCmd(root, "show", rev+":"+path)
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

// CommitFiles returns the list of files changed in the given commit.
func CommitFiles(root, hash string) ([]string, error) {
	out, err := gitCmd(root, "diff-tree", "--no-commit-id", "--name-only", "-r", hash)
	if err != nil {
		out, err = gitCmd(root, "show", "--name-only", "--format=", hash)
		if err != nil {
			return nil, err
		}
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
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
