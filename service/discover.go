package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// baseName returns the last path component.
func baseName(p string) string {
	return filepath.Base(p)
}

// relPath returns p relative to workspace, "." when equal, or p when outside.
func relPath(workspace, p string) string {
	if workspace == "" {
		return p
	}
	rel, err := filepath.Rel(workspace, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return rel
}

// onMac is whether the home Library folder is off limits: reading the
// other apps' containers in it makes macOS block omaterm. Tests set it.
var onMac = runtime.GOOS == "darwin"

// Protected reports whether path is a directory omaterm must not read:
// on macOS, the home Library folder.
func Protected(path string) bool {
	if !onMac {
		return false
	}
	home, err := os.UserHomeDir()
	return err == nil && home != "" && filepath.Clean(path) == filepath.Join(home, "Library")
}

// skipDirs are never descended into while indexing or discovering repos.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".mygo":        true,
	"build":        false, // build is allowed for discovery? keep it skippable for index
	"dist":         true,
	".cache":       true,
	".next":        true,
	"target":       true,
}

// indexSkipDirs is used for file indexing (quick open / search).
var indexSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".mygo":        true,
	"dist":         true,
	".cache":       true,
	".next":        true,
	"target":       true,
}

// DiscoverRepos finds git repositories at workspace root and up to depth
// levels below it. Dedupes by toplevel. Root repo, when present, comes first.
func DiscoverRepos(workspace string, depth int) []Repo {
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	workspace = filepath.Clean(workspace)
	var repos []Repo
	seen := map[string]bool{}

	addIfRepo := func(dir string) {
		if !IsRepoRoot(dir) {
			return
		}
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			real = dir
		}
		if seen[real] {
			return
		}
		seen[real] = true
		r, err := RepoStatus(dir, workspace)
		if err != nil {
			return
		}
		repos = append(repos, *r)
	}

	// Root first.
	addIfRepo(workspace)

	if depth < 1 {
		return repos
	}

	entries, err := os.ReadDir(workspace)
	if err != nil {
		return repos
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if skipDirs[name] || strings.HasPrefix(name, ".") {
			continue
		}
		addIfRepo(filepath.Join(workspace, name))
	}
	return repos
}

// IndexFiles walks workspace and returns repo-relative file paths,
// skipping .gitignore'd and heavy directories.
func IndexFiles(workspace string, maxFiles int) []string {
	ig := loadGitIgnore(workspace)
	var files []string
	root := filepath.Clean(workspace)

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if indexSkipDirs[d.Name()] || (strings.HasPrefix(d.Name(), ".") && d.Name() != ".") || Protected(path) {
				return filepath.SkipDir
			}
			if ig != nil && ig.matches(rel+"/") {
				return filepath.SkipDir
			}
			return nil
		}
		if ig != nil && ig.matches(rel) {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		if maxFiles > 0 && len(files) >= maxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	return files
}

// MatchGlob matches a gitignore-style pattern against a path: * stays
// within one path segment, ** crosses segments. It is what the folder
// watcher tests a changed file against, so a watch's globs read the same
// as the .gitignore beside them.
func MatchGlob(pattern, path string) bool {
	return matchGitGlob(pattern, path)
}

// IgnoreFunc returns what a folder watch should skip: .git, the heavy
// folders skipDirs names, and what the root's .gitignore ignores. It suits
// mygo.FileWatchOptions.Ignore, which is given a path relative to root
// with forward slashes and asked about directories as well as files.
//
// The .gitignore is read once, here: a watch asks the function about every
// entry it finds, on a goroutine of its own, where reading a file would
// not do.
func IgnoreFunc(root string) func(path string, isDir bool) bool {
	ig := loadGitIgnore(root)
	return func(path string, isDir bool) bool {
		rel := strings.TrimPrefix(filepath.ToSlash(path), "./")
		if rel == "" || rel == "." {
			return false
		}
		segs := strings.Split(rel, "/")
		for _, s := range segs {
			// A file in an ignored folder is ignored with it.
			if s == ".git" || skipDirs[s] {
				return true
			}
		}
		if ig == nil {
			return false
		}
		if isDir {
			rel += "/"
		}
		return ig.matches(rel)
	}
}

// gitIgnore is a minimal .gitignore matcher supporting the common cases:
// comments, blank lines, dir-only patterns (trailing /), leading-anchored
// patterns (leading /), and * wildcards. No negation/character classes.
type gitIgnore struct {
	patterns []gitIgnorePattern
}

type gitIgnorePattern struct {
	pat      string
	dirOnly  bool
	anchored bool
}

func loadGitIgnore(root string) *gitIgnore {
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil
	}
	ig := &gitIgnore{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		p := gitIgnorePattern{pat: line}
		if strings.HasSuffix(p.pat, "/") {
			p.dirOnly = true
			p.pat = strings.TrimSuffix(p.pat, "/")
		}
		if strings.HasPrefix(p.pat, "/") {
			p.anchored = true
			p.pat = strings.TrimPrefix(p.pat, "/")
		}
		ig.patterns = append(ig.patterns, p)
	}
	return ig
}

// matches reports whether rel (slash-cleaned relative path; dirs end with /)
// is ignored.
func (ig *gitIgnore) matches(rel string) bool {
	rel = filepath.ToSlash(rel)
	isDir := strings.HasSuffix(rel, "/")
	rel = strings.TrimSuffix(rel, "/")
	for _, p := range ig.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if p.anchored {
			if matchGitGlob(p.pat, rel) {
				return true
			}
			continue
		}
		// Unanchored: match against the full path or any trailing component.
		if matchGitGlob(p.pat, rel) {
			return true
		}
		parts := strings.Split(rel, "/")
		for i := 1; i < len(parts); i++ {
			if matchGitGlob(p.pat, strings.Join(parts[i:], "/")) {
				return true
			}
		}
		// A dir-only pattern also matches when it names any leading
		// sub-path: "node_modules" ignores "node_modules/x/" and the
		// files below it.
		if p.dirOnly {
			psegs := strings.Split(p.pat, "/")
			for start := 0; start+len(psegs) <= len(parts); start++ {
				ok := true
				for j, ps := range psegs {
					if !matchGlob(ps, parts[start+j]) {
						ok = false
						break
					}
				}
				if ok {
					return true
				}
			}
		}
	}
	return false
}

// matchGitGlob matches pat against path segment-wise; * doesn't cross /,
// ** does.
func matchGitGlob(pat, path string) bool {
	psegs := strings.Split(pat, "/")
	ssegs := strings.Split(path, "/")
	return matchSegs(psegs, ssegs)
}

func matchSegs(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegs(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if !matchGlob(pat[0], segs[0]) {
		return false
	}
	return matchSegs(pat[1:], segs[1:])
}

func matchGlob(pat, s string) bool {
	px, sx := 0, 0
	nextPx, nextSx := -1, -1
	for px < len(pat) || sx < len(s) {
		if px < len(pat) {
			switch pat[px] {
			case '*':
				nextPx = px
				nextSx = sx + 1
				px++
				continue
			case '?':
				if sx < len(s) {
					px++
					sx++
					continue
				}
			default:
				if sx < len(s) && pat[px] == s[sx] {
					px++
					sx++
					continue
				}
			}
		}
		if nextSx > 0 && nextSx <= len(s) {
			px = nextPx
			sx = nextSx
			continue
		}
		return false
	}
	return true
}
