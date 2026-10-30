package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// GitSummary is what a pane header says of the repository a pane works
// in: its branch, and the lines changed in the working tree since HEAD.
type GitSummary struct {
	Root           string
	Branch         string
	Added, Removed int
	Files          int
}

// safeGit runs git as the app's background readers must: without taking
// the index lock an agent may be committing under, and without asking for
// a password.
func safeGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-c", "core.quotepath=false", "-c", "color.ui=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	hide(cmd)
	out, err := cmd.Output()
	return strings.TrimRight(string(out), "\n"), err
}

// RepoRoot returns the root of the work tree holding dir, "" for none,
// without running git: the nearest directory with a .git.
func RepoRoot(dir string) string {
	for d := dir; d != "" && d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	return ""
}

// GitStamp changes when a repository's HEAD, index or branches do: the
// mtimes and sizes of the files git rewrites for them.
func GitStamp(root string) string {
	gitDir := filepath.Join(root, ".git")
	if b, err := os.ReadFile(gitDir); err == nil {
		// A linked worktree: .git is a file naming its git directory.
		if p, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:"); ok {
			gitDir = strings.TrimSpace(p)
			if !filepath.IsAbs(gitDir) {
				gitDir = filepath.Join(root, gitDir)
			}
		}
	}
	var sb strings.Builder
	for _, f := range []string{"HEAD", "index", "packed-refs", "MERGE_HEAD", "REBASE_HEAD"} {
		if st, err := os.Stat(filepath.Join(gitDir, f)); err == nil {
			sb.WriteString(st.ModTime().Format(time.RFC3339Nano))
			sb.WriteString(strconv.FormatInt(st.Size(), 10))
		}
		sb.WriteByte('|')
	}
	return sb.String()
}

// LoadGitSummary reads a repository's branch and diffstat.
func LoadGitSummary(ctx context.Context, root string) (GitSummary, error) {
	s := GitSummary{Root: root}
	branch, err := safeGit(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return s, err
	}
	s.Branch = branch
	if branch == "HEAD" {
		if sha, err := safeGit(ctx, root, "rev-parse", "--short", "HEAD"); err == nil {
			s.Branch = sha
		}
	}
	out, err := safeGit(ctx, root, "diff", "--numstat", "HEAD")
	if err != nil {
		return s, nil // no commits yet: a branch and nothing to count
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		s.Files++
		if n, err := strconv.Atoi(f[0]); err == nil {
			s.Added += n
		}
		if n, err := strconv.Atoi(f[1]); err == nil {
			s.Removed += n
		}
	}
	return s, nil
}
