package main

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"oterm/service"
)

// Renders the app's views to PNGs in testdata/ for visual comparison
// against the mock. Run: go test -run TestShots
//
// The suite pins everything that would otherwise drift between runs: the
// workspace is this repo, the project list is replaced (not loaded from
// ~/.config/oterm/state.json), git data comes from fixed fixtures, and
// processes, ports and machine load are fixed values. A shot that fails
// to open the view it is named for fails the test instead of silently
// writing a picture of the terminal.
func TestShots(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Pin "now": relative labels in the graph would otherwise change as
	// the suite runs at a different hour.
	shotNow := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	prevNow := nowFunc
	nowFunc = func() time.Time { return shotNow }
	defer func() { nowFunc = prevNow }()
	// A file that exists in every clone, for the markdown shot.
	const mdFile = ".agents/skills/screenshot/SKILL.md"

	shots := []struct {
		name  string
		setup func(a *appState)
		check func(a *appState) error
	}{
		{"terminal", nil, nil},
		{"editor", func(a *appState) { a.openEditor("app.go") },
			func(a *appState) error { return wantView(a, tabEditor, "app.go") }},
		{"markdown", func(a *appState) { a.openEditor(mdFile) },
			func(a *appState) error { return wantView(a, tabEditor, mdFile) }},
		{"diff", func(a *appState) {
			// Synthetic buffers: the working tree is clean in a fresh
			// clone, and a live file would make the golden depend on it.
			a.setDiffBuffers(
				"package main\n\nfunc greet(name string) string {\n\treturn \"hi \" + name\n}\n\nfunc main() {\n\tprintln(greet(\"world\"))\n}\n",
				"package main\n\nfunc greet(name string) string {\n\treturn \"hello \" + name\n}\n\nfunc main() {\n\tprintln(greet(\"oterm\"))\n}\n",
				"main.go")
			a.diffTitle = "main.go · Working Tree · INDEX ↔ WORKING TREE"
			a.diffSrc = diffSource{Kind: diffWorking, Path: "main.go"}
			a.focusDiffTab("main.go")
		}, func(a *appState) error {
			if a.activeView != tabDiff || a.diffOld == nil || a.diffNew == nil {
				return errShot("diff buffers were not loaded")
			}
			return nil
		}},
		{"diff-inline", func(a *appState) {
			a.setDiffBuffers(
				"package main\n\nfunc greet(name string) string {\n\treturn \"hi \" + name\n}\n\nfunc main() {\n\tprintln(greet(\"world\"))\n}\n",
				"package main\n\nfunc greet(name string) string {\n\treturn \"hello \" + name\n}\n\nfunc main() {\n\tprintln(greet(\"oterm\"))\n}\n",
				"main.go")
			a.diffTitle = "main.go · Working Tree · INDEX ↔ WORKING TREE"
			a.diffSrc = diffSource{Kind: diffWorking, Path: "main.go"}
			a.diffSplit = false
			a.focusDiffTab("main.go")
		}, func(a *appState) error {
			if a.activeView != tabDiff || a.diffSplit {
				return errShot("diff-inline is not in inline layout")
			}
			return nil
		}},
		{"graph", func(a *appState) {
			// Fixed history: the real log changes with every commit, so a
			// golden taken from it would churn.
			a.activeView = tabGraph
			a.commits = shotLinearCommits()
			a.graphLoaded = true
		}, nil},
		{"graph-merges", func(a *appState) {
			// A synthetic history with merges, so lane junctions and
			// continuity are visible on every run.
			a.activeView = tabGraph
			a.commits = shotMergeCommits()
			a.graphLoaded = true
		}, nil},
		{"git-history", func(a *appState) {
			a.inspectorTab = 2
			a.gitGraphOpen = true
			a.commits = shotMergeCommits()
			a.graphLoaded = true
			// The expanded commit's file list is real git data; expanding
			// the synthetic head would run git for a hash that does not
			// exist, so leave it collapsed and show the graph itself.
		}, nil},
		{"files", func(a *appState) { a.inspectorTab = 1 }, nil},
		{"git", func(a *appState) {
			a.inspectorTab = 2
		}, nil},
		{"info-long-branch", func(a *appState) {
			a.inspectorTab = 0
			a.currentBranch = "feature/very-long-branch-name-that-would-overflow-the-inspector"
			if p := a.project(); p != nil {
				p.Branch = a.currentBranch
			}
		}, nil},
		{"git-long-branch", func(a *appState) {
			// A name wider than the panel must ellipsize, leaving the
			// remote actions inside the row.
			a.inspectorTab = 2
			if r := a.activeRepoRef(); r != nil {
				a.branchesRepo = r.Root // don't reload over the override
			}
			a.currentBranch = "feature/very-long-branch-name-that-would-overflow-the-inspector"
			if p := a.project(); p != nil {
				p.Branch = a.currentBranch
			}
		}, nil},
		{"quick-open", func(a *appState) {
			a.fileIndex = []string{
				"app.go", "commands.go", "diff.go", "editor.go", "graph.go",
				"icons.go", "inspector.go", "main.go", "overlays.go", "sidebar.go",
				"state.go", "statusbar.go", "tabs.go", "terminal.go", "theme.go",
				"util.go", "widgets.go", "service/git.go", "service/search.go",
				".agents/skills/screenshot/SKILL.md",
			}
			a.openOverlay(overlayQuickOpen)
		}, func(a *appState) error {
			if a.overlay != overlayQuickOpen {
				return errShot("quick open did not open")
			}
			return nil
		}},
		{"command-palette", func(a *appState) { a.openOverlay(overlayCommandPalette) },
			func(a *appState) error {
				if a.overlay != overlayCommandPalette {
					return errShot("command palette did not open")
				}
				return nil
			}},
		{"branch", func(a *appState) {
			if r := a.activeRepoRef(); r != nil {
				a.currentBranch = r.Branch
			}
			a.openBranchOverlay()
		}, nil},
		{"global-search", func(a *appState) {
			a.openOverlay(overlayGlobalSearch)
			a.searchQuery = "appState"
			// Fixed matches and preview: a real search reads the working
			// tree, which changes with every edit and would churn the
			// golden.
			a.searchResults = []service.Match{
				{Path: "app.go", Line: 12, Text: "func (a *appState) view(c *ui.Context) {", Start: 12, End: 20},
				{Path: "inspector.go", Line: 15, Text: "func (a *appState) inspector(c *ui.Context) ui.Element {", Start: 12, End: 20},
				{Path: "statusbar.go", Line: 16, Text: "func (a *appState) statusBar(c *ui.Context) ui.Element {", Start: 12, End: 20},
				{Path: "tabs.go", Line: 10, Text: "func (a *appState) tabBar(c *ui.Context) ui.Element {", Start: 12, End: 20},
				{Path: "graph.go", Line: 171, Text: "func (a *appState) graphView(c *ui.Context) ui.Element {", Start: 12, End: 20},
			}
			a.searchSelected = 0
			st := &editor.State{ReadOnly: true, Theme: omaEditorTheme()}
			st.Buffer = editor.NewBuffer("package main\n\nfunc (a *appState) view(c *ui.Context) {\n\tc.SetTheme(omaTheme())\n}\n")
			st.Buffer.SetLanguage(editor.LanguageFor("app.go"))
			st.GoTo(editor.Pos{Line: 2, Col: 0})
			start := st.Buffer.Offset(editor.Pos{Line: 2, Col: 12})
			end := st.Buffer.Offset(editor.Pos{Line: 2, Col: 20})
			st.Select(start, end)
			a.searchPreview = st
		}, func(a *appState) error {
			if a.overlay != overlayGlobalSearch || a.searchPreview == nil {
				return errShot("global search did not open with a preview")
			}
			return nil
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
		}, nil},
		{"split-balanced", func(a *appState) {
			// Three panes from a single one must end up equal thirds.
			a.splitPane(a.activeSplit().termIDs()[0], splitVertical)
			a.splitPane(a.activeSplit().termIDs()[0], splitVertical)
		}, nil},
		{"no-sidebars", func(a *appState) { a.sidebarVisible = false; a.inspectorVisible = false }, nil},
		{"confirm-danger", func(a *appState) {
			open := true
			a.pendingKillPid = 4821
			a.pendingKillName = "vite"
			a.killConfirmOpen = &open
		}, func(a *appState) error {
			if a.killConfirmOpen == nil {
				return errShot("danger dialog was not opened")
			}
			return nil
		}},
		{"binary-diff", func(a *appState) {
			a.setDiffBinary("assets/logo.png", 78*1024)
			a.diffTitle = "assets/logo.png · Working Tree · INDEX ↔ WORKING TREE"
			a.diffSrc = diffSource{Kind: diffWorking, Path: "assets/logo.png"}
			a.focusDiffTab("assets/logo.png")
		}, func(a *appState) error {
			if !a.diffBinary {
				return errShot("binary diff placeholder was not set")
			}
			return nil
		}},
	}
	for _, s := range shots {
		a := newApp()
		// Pin the workspace and project list: newApp() reads the user's
		// saved state, which made the shots depend on what was open.
		a.projects = []project{{Name: "oterm", Path: wd, Tabs: []string{"dev", "api", "tests"}}}
		a.active = 0
		a.setProject(0)
		// The tester has no window, so the async refreshers no-op; load
		// repo data synchronously instead.
		if repos := service.DiscoverRepos(a.workspace, 1); len(repos) > 0 {
			a.repos = repos
			a.activeRepo = 0
			a.currentBranch = repos[0].Branch
			if p := a.project(); p != nil {
				p.Branch = repos[0].Branch
			}
			// Fixed status and HEAD: a clean or dirty working tree would
			// otherwise change the Git badge and panel between runs.
			r := &a.repos[0]
			r.Head = "a1b2c3d"
			r.Staged = []service.GitFile{
				{Path: "main.go", Status: "M", Staged: true},
				{Path: "theme.go", Status: "M", Staged: true},
			}
			r.Changes = []service.GitFile{
				{Path: "statusbar.go", Status: "M"},
				{Path: ".agents/skills/screenshot/SKILL.md", Status: "M"},
				{Path: "icons/alert-triangle.svg", Status: "A"},
			}
			r.Stashes = nil
			r.Ahead, r.Behind = 0, 0
		}
		// Fixed process, port and machine-load values: real ones differ
		// on every run.
		a.processes = []service.Process{{Name: "oterm", Pid: 1234, CPU: "2.5%", Mem: "64 MB"}}
		a.ports = []service.Port{{Port: 8080, Name: "http", Open: true, Pid: 1234, Process: "oterm"}}
		a.sysStats = service.SystemStats{CPUPercent: 3, MemUsed: 1395864371, MemTotal: 17179869184, OK: true}
		if s.setup != nil {
			s.setup(a)
		}
		if s.check != nil {
			if err := s.check(a); err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}
		}
		w, h := 1440, 900
		if os.Getenv("SHOT_WIDE") != "" {
			w, h = 2560, 1440 // the maximized case, where alignment shows
		}
		tr := ui.NewTester(a.view, w, h)
		os.MkdirAll("testdata", 0o755)
		f, err := os.Create(filepath.Join("testdata", "shot-"+s.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, tr.Image()); err != nil {
			t.Fatal(err)
		}
		f.Close()
		stopTermPanes(a)
	}
}

// errShot is a shot-check failure.
func errShot(msg string) error { return errors.New(msg) }

// wantView checks that path is open in the editor and is the active view.
func wantView(a *appState, kind topTabKind, path string) error {
	if a.activeView != kind {
		return errShot("active view is not the editor")
	}
	doc := a.editors[path]
	if doc == nil {
		return errShot(path + " was not opened")
	}
	if doc.State == nil && !doc.Binary {
		return errShot(path + " has no editor state")
	}
	return nil
}

// shotLinearCommits is a fixed, newest-first history for the graph shot.
func shotLinearCommits() []service.Commit {
	// Fixed times: relativeTime renders stable labels against the pinned
	// clock in TestShots.
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	subjects := []string{
		"feat: keep terminal panes alive across tab switches",
		"fix: rebalance split siblings after a pane closes",
		"feat: show project name in the status bar",
		"perf: cache a commit's file list while it is expanded",
		"fix: read binary files as placeholders, not mojibake",
		"feat: strip front matter from the markdown preview",
		"fix: give destructive dialogs a red confirm button",
		"refactor: drop the dead file-search filter",
		"docs: record the pointer-control workflow",
		"test: pin the screenshot suite's workspace",
		"feat: load git history when the Git tab opens",
		"chore: refresh the UI goldens",
	}
	commits := make([]service.Commit, len(subjects))
	// Newest first: commit i's first parent is i+1.
	hashes := make([]string, len(subjects))
	for i := range subjects {
		hashes[i] = "f" + itoa(100-i) + "000000000000000000000000000000000000000"
		if len(hashes[i]) > 40 {
			hashes[i] = hashes[i][:40]
		}
	}
	for i, s := range subjects {
		h := hashes[i]
		c := service.Commit{
			Hash:    h,
			Short:   h[:7],
			Subject: s,
			Author:  "Ada Lovelace",
			Time:    base.Add(-time.Duration(i) * 3 * time.Hour),
		}
		if i < len(subjects)-1 {
			c.Parents = []string{hashes[i+1]}
		}
		if i == 0 {
			c.Refs = []string{"HEAD -> main", "origin/main"}
		}
		commits[i] = c
	}
	return commits
}

// shotMergeCommits is the fixed history with merges used by the graph and
// git-history shots.
func shotMergeCommits() []service.Commit {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	mk := func(hash, short, subject, author string, parents []string, hoursAgo int) service.Commit {
		return service.Commit{
			Hash: hash, Short: short, Subject: subject, Author: author,
			Parents: parents, Time: base.Add(-time.Duration(hoursAgo) * time.Hour),
		}
	}
	commits := []service.Commit{
		mk("1a2b3c4d5e6f", "1a2b3c4", "Merge pull request #4 from feature/chat", "Ada", []string{"aa11", "ff99"}, 0),
		mk("aa11", "aa11", "fix: guided question position on chat ui", "Ada", []string{"cc22"}, 2),
		mk("ff99", "ff99", "feat: move vps egress to all traffic", "Ada", []string{"cc22"}, 4),
		mk("cc22", "cc22", "Merge pull request #3 from dev", "Ada", []string{"dd33", "ee44"}, 6),
		mk("ee44", "ee44", "feat: add simple RAG to the chatbot", "Ada", []string{"dd33"}, 8),
		mk("dd33", "dd33", "refactor: TDSS UI to be person view oriented", "Ada", []string{"bb55"}, 10),
		mk("bb55", "bb55", "fix: logging module location on seed gcs", "Ada", nil, 12),
	}
	commits[0].Refs = []string{"HEAD -> main", "origin/main"}
	return commits
}
