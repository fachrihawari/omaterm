package main

import (
	"context"
	"sync"
	"time"

	"omaterm/service"
)

// gitWatch keeps the branch and diffstat of the repositories panes work
// in, for their headers: it checks each repository's stamp every second,
// and reads it again when the stamp changes, every 4 s while a pane in it
// works (an agent edits files without touching the index), and every 30 s
// else.
type gitWatch struct {
	mu      sync.Mutex
	dirs    map[string]bool // the panes' directories, and whether one works
	roots   map[string]string
	stamps  map[string]string
	read    map[string]time.Time
	summary map[string]service.GitSummary // by root
}

func newGitWatch() *gitWatch {
	return &gitWatch{dirs: map[string]bool{}, roots: map[string]string{}, stamps: map[string]string{},
		read: map[string]time.Time{}, summary: map[string]service.GitSummary{}}
}

// setDirs tells the directories to follow, and which have a pane working.
func (g *gitWatch) setDirs(dirs map[string]bool) {
	g.mu.Lock()
	g.dirs = dirs
	g.mu.Unlock()
}

// of returns the summary of the repository holding dir.
func (g *gitWatch) of(dir string) (service.GitSummary, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	root, ok := g.roots[dir]
	if !ok || root == "" {
		return service.GitSummary{}, false
	}
	s, ok := g.summary[root]
	return s, ok
}

func (g *gitWatch) run(ctx context.Context, changed func()) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if g.poll(ctx, now) {
				changed()
			}
		}
	}
}

// poll reads the repositories that are due; it reports whether any
// summary changed.
func (g *gitWatch) poll(ctx context.Context, now time.Time) bool {
	g.mu.Lock()
	working := map[string]bool{}
	var dirs []string
	for d, w := range g.dirs {
		dirs = append(dirs, d)
		if _, ok := g.roots[d]; !ok {
			g.mu.Unlock()
			root := service.RepoRoot(d)
			g.mu.Lock()
			g.roots[d] = root
		}
		if w {
			working[g.roots[d]] = true
		}
	}
	roots := map[string]bool{}
	for _, d := range dirs {
		if r := g.roots[d]; r != "" {
			roots[r] = true
		}
	}
	g.mu.Unlock()

	changed := false
	for root := range roots {
		stamp := service.GitStamp(root)
		g.mu.Lock()
		last, seen := g.read[root]
		due := !seen || stamp != g.stamps[root] || now.Sub(last) > 30*time.Second ||
			working[root] && now.Sub(last) > 4*time.Second
		g.mu.Unlock()
		if !due {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		s, err := service.LoadGitSummary(cctx, root)
		cancel()
		g.mu.Lock()
		g.read[root], g.stamps[root] = now, stamp
		if err == nil && g.summary[root] != s {
			g.summary[root] = s
			changed = true
		}
		g.mu.Unlock()
	}
	return changed
}
