package main

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"oterm/service"
)

// Renders the app's views to PNGs in testdata/ for visual comparison
// against the mock. Run: go test -run TestShots
func TestShots(t *testing.T) {
	shots := []struct {
		name  string
		setup func(a *appState)
	}{
		{"terminal", nil},
		{"editor", func(a *appState) { a.openEditor("app.go") }},
		{"markdown", func(a *appState) { a.openEditor("README.md") }},
		{"diff", func(a *appState) {
			if r := a.activeRepoRef(); r != nil && len(r.Changes) > 0 {
				a.openWorkingDiff(r.Changes[0].Path, false)
			}
		}},
		{"graph", func(a *appState) { a.activeView = tabGraph }},
		{"graph-merges", func(a *appState) {
			// A synthetic history with merges, so lane junctions and
			// continuity are visible on every run.
			a.activeView = tabGraph
			a.commits = []service.Commit{
				{Hash: "1a2b3c4d5e6f", Short: "1a2b3c4", Subject: "Merge pull request #4 from feature/chat", Author: "Ada", Parents: []string{"aa11", "ff99"}, Time: time.Now()},
				{Hash: "aa11", Short: "aa11", Subject: "fix: guided question position on chat ui", Author: "Ada", Parents: []string{"cc22"}, Time: time.Now()},
				{Hash: "ff99", Short: "ff99", Subject: "feat: move vps egress to all traffic", Author: "Ada", Parents: []string{"cc22"}, Time: time.Now()},
				{Hash: "cc22", Short: "cc22", Subject: "Merge pull request #3 from dev", Author: "Ada", Parents: []string{"dd33", "ee44"}, Time: time.Now()},
				{Hash: "ee44", Short: "ee44", Subject: "feat: add simple RAG to the chatbot", Author: "Ada", Parents: []string{"dd33"}, Time: time.Now()},
				{Hash: "dd33", Short: "dd33", Subject: "refactor: TDSS UI to be person view oriented", Author: "Ada", Parents: []string{"bb55"}, Time: time.Now()},
				{Hash: "bb55", Short: "bb55", Subject: "fix: logging module location on seed gcs", Author: "Ada", Parents: nil, Time: time.Now()},
			}
		}},
		{"git-history", func(a *appState) {
			a.inspectorTab = 2
			a.gitGraphOpen = true
			if len(a.commits) > 0 {
				a.expandedCommit = a.commits[0].Hash
			}
		}},
		{"files", func(a *appState) { a.inspectorTab = 1 }},
		{"git", func(a *appState) { a.inspectorTab = 2 }},
		{"quick-open", func(a *appState) {
			a.fileIndex = service.IndexFiles(a.workspace, 100000)
			a.openOverlay(overlayQuickOpen)
		}},
		{"command-palette", func(a *appState) { a.openOverlay(overlayCommandPalette) }},
		{"branch", func(a *appState) {
			if r := a.activeRepoRef(); r != nil {
				a.currentBranch = r.Branch
			}
			a.openBranchOverlay()
		}},
		{"global-search", func(a *appState) {
			a.openOverlay(overlayGlobalSearch)
			a.searchQuery = "appState"
			a.searchResults, _ = service.SearchContent(context.Background(), a.workspace, "appState")
			a.updateSearchPreview()
		}},
		{"split", func(a *appState) {
			a.tabs[a.activeTab].Split = &splitNode{
				Dir: splitVertical, Ratio: 50,
				First: &splitNode{TermID: randomTermID()},
				Second: &splitNode{Dir: splitHorizontal, Ratio: 50,
					First:  &splitNode{TermID: randomTermID()},
					Second: &splitNode{TermID: randomTermID()},
				},
			}
		}},
		{"split-balanced", func(a *appState) {
			// Three panes from a single one must end up equal thirds.
			a.splitPane(a.activeSplit().termIDs()[0], splitVertical)
			a.splitPane(a.activeSplit().termIDs()[0], splitVertical)
		}},
		{"no-sidebars", func(a *appState) { a.sidebarVisible = false; a.inspectorVisible = false }},
	}
	if os.Getenv("SHOT_TOAST") != "" {
		shots = append(shots, struct {
			name  string
			setup func(a *appState)
		}{"toast", nil})
	}
	for _, s := range shots {
		a := newApp()
		// Shots need a project to render against; use the repo itself.
		if len(a.projects) == 0 {
			a.projects = []project{{Name: "oterm", Path: ".", Tabs: []string{"dev", "api", "tests"}}}
			a.setProject(0)
		}
		// The tester has no window, so the async refreshers no-op; load
		// repo data synchronously instead.
		if repos := service.DiscoverRepos(a.workspace, 1); len(repos) > 0 {
			a.repos = repos
			a.activeRepo = 0
		}
		a.commits, _ = service.Log(a.workspace, 300)
		if s.setup != nil {
			s.setup(a)
		}
		w, h := 1440, 900
		if os.Getenv("SHOT_WIDE") != "" {
			w, h = 2560, 1440 // the maximized case, where alignment shows
		}
		tr := ui.NewTester(a.view, w, h)
		if s.name == "toast" {
			tr.Click("oterm") // shows "Switched to oterm"
			tr.Advance(100 * time.Millisecond)
			tr.Frame()
		}
		os.MkdirAll("testdata", 0o755)
		f, err := os.Create(filepath.Join("testdata", "shot-"+s.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tr.Image()); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
