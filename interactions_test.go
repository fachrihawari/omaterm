package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"oterm/service"
)

// newTestApp returns an app whose workspace is a temp git repo holding a
// couple of commits and one modified file.
func newTestApp(t *testing.T) *appState {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "initial")
	// Modify the file so the repo has a change.
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := newApp()
	a.projects = []project{{Name: "test", Path: dir, Tabs: []string{"dev"}}}
	a.win = nil // no window: async refreshers no-op; call sync helpers instead
	a.setProject(0)

	// Load repo status synchronously for the test.
	r, err := service.RepoStatus(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	a.repos = []service.Repo{*r}
	a.activeRepo = 0
	return a
}

func TestRepoStatusStagedChanges(t *testing.T) {
	a := newTestApp(t)
	r := a.activeRepoRef()
	if r == nil {
		t.Fatal("no repo discovered")
	}
	if r.Branch != "main" {
		t.Errorf("branch = %q, want main", r.Branch)
	}
	if a.gitCount() != 1 {
		t.Fatalf("git count = %d, want 1", a.gitCount())
	}
	if len(r.Changes) != 1 || r.Changes[0].Path != "hello.txt" {
		t.Fatalf("changes = %+v", r.Changes)
	}

	// Stage it via the service and reload.
	if err := service.Stage(r.Root, "hello.txt"); err != nil {
		t.Fatal(err)
	}
	fresh, err := service.RepoStatus(r.Root, a.workspace)
	if err != nil {
		t.Fatal(err)
	}
	a.repos[0] = *fresh
	r = a.activeRepoRef()
	if len(r.Staged) != 1 || len(r.Changes) != 0 {
		t.Fatalf("after stage: staged=%d changes=%d", len(r.Staged), len(r.Changes))
	}

	// Commit clears the staged list.
	if err := service.CommitStaged(r.Root, "update"); err != nil {
		t.Fatal(err)
	}
	fresh, err = service.RepoStatus(r.Root, a.workspace)
	if err != nil {
		t.Fatal(err)
	}
	a.repos[0] = *fresh
	if len(a.activeRepoRef().Staged) != 0 {
		t.Fatal("after commit: staged should be empty")
	}
}

func TestStashFlow(t *testing.T) {
	a := newTestApp(t)
	r := a.activeRepoRef()

	if err := service.StashPush(r.Root, "wip", false); err != nil {
		t.Fatal(err)
	}
	stashes, err := service.StashList(r.Root)
	if err != nil || len(stashes) != 1 {
		t.Fatalf("stashes = %v, %v", stashes, err)
	}
	if stashes[0].Message == "" {
		t.Error("stash message empty")
	}

	if err := service.StashPop(r.Root, 0); err != nil {
		t.Fatal(err)
	}
	stashes, _ = service.StashList(r.Root)
	if len(stashes) != 0 {
		t.Fatal("stash not popped")
	}
}

func TestSidebarAndInspectorToggle(t *testing.T) {
	a := newTestApp(t)
	tr := ui.NewTester(a.view, 1440, 900)

	tr.Key(ui.Cmd, ui.KeyB)
	tr.Frame()
	if a.sidebarVisible {
		t.Error("Cmd+B did not hide the sidebar")
	}
	tr.Key(ui.Cmd, ui.KeyB)
	tr.Frame()
	if !a.sidebarVisible {
		t.Error("Cmd+B did not show the sidebar")
	}

	tr.Key(ui.Cmd|ui.Shift, ui.KeyB)
	tr.Frame()
	if a.inspectorVisible {
		t.Error("Cmd+Shift+B did not hide the inspector")
	}
	tr.Key(ui.Cmd|ui.Shift, ui.KeyB)
	tr.Frame()
	if !a.inspectorVisible {
		t.Error("Cmd+Shift+B did not show the inspector")
	}
}

func TestViewShortcuts(t *testing.T) {
	a := newTestApp(t)
	tr := ui.NewTester(a.view, 1440, 900)

	tr.Key(ui.Cmd, ui.Key2)
	tr.Frame()
	if a.activeView != tabEditor {
		t.Error("Cmd+2 did not show the editor")
	}
	tr.Key(ui.Cmd, ui.Key3)
	tr.Frame()
	if a.activeView != tabDiff {
		t.Error("Cmd+3 did not show the diff")
	}
	tr.Key(ui.Cmd, ui.Key1)
	tr.Frame()
	if a.activeView != tabTerminal {
		t.Error("Cmd+1 did not show the terminal")
	}
}

func TestQuickOpenOverlay(t *testing.T) {
	a := newTestApp(t)
	a.fileIndex = []string{"hello.txt", "src/main.go", "src/util.go"}
	tr := ui.NewTester(a.view, 1440, 900)

	tr.Key(ui.Cmd, ui.KeyP)
	tr.Frame()
	if a.overlay != overlayQuickOpen {
		t.Fatal("Ctrl+P did not open quick open")
	}
	if !tr.HasText("hello.txt") {
		t.Error("file list did not render")
	}

	items := a.quickOpenItems()
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}

	a.quickQuery = "util"
	items = a.quickOpenItems()
	if len(items) != 1 || items[0].Value != "src/util.go" {
		t.Fatalf("fuzzy filter = %+v", items)
	}

	tr.Key(0, ui.KeyEscape)
	tr.Frame()
	if a.overlay != overlayNone {
		t.Error("Esc did not close the overlay")
	}
}

func TestCommandPaletteOverlay(t *testing.T) {
	a := newTestApp(t)
	tr := ui.NewTester(a.view, 1440, 900)

	tr.Key(ui.Cmd|ui.Shift, ui.KeyP)
	tr.Frame()
	if a.overlay != overlayCommandPalette {
		t.Fatal("Ctrl+Shift+P did not open the palette")
	}
	cmds := a.paletteCommands()
	if len(cmds) == 0 {
		t.Fatal("palette is empty")
	}
	a.cmdQuery = "sidebar"
	cmds = a.paletteCommands()
	found := false
	for _, e := range cmds {
		if e.ID == "view.toggleSidebar" {
			found = true
		}
	}
	if !found {
		t.Error("toggle sidebar command not found")
	}
	tr.Key(0, ui.KeyEscape)
	tr.Frame()
}

func TestGraphLanes(t *testing.T) {
	// Linear history: all on lane 0.
	linear := []service.Commit{
		{Hash: "c", Parents: []string{"b"}},
		{Hash: "b", Parents: []string{"a"}},
		{Hash: "a"},
	}
	lanes := computeLanes(linear)
	for i, l := range lanes {
		if l.NodeLane != 0 {
			t.Errorf("linear commit %d on lane %d", i, l.NodeLane)
		}
	}

	// Branch + merge: the merge commit has two parents; the second parent
	// branch gets its own lane.
	branched := []service.Commit{
		{Hash: "m", Parents: []string{"b", "f"}}, // merge of feature
		{Hash: "f", Parents: []string{"a"}},      // feature tip
		{Hash: "b", Parents: []string{"a"}},      // main
		{Hash: "a"},
	}
	lanes = computeLanes(branched)
	if lanes[1].NodeLane == lanes[2].NodeLane {
		t.Errorf("feature and main share lane %d", lanes[1].NodeLane)
	}
	// The common ancestor sits on one of the two branch lanes.
	if lanes[3].NodeLane != lanes[1].NodeLane && lanes[3].NodeLane != lanes[2].NodeLane {
		t.Errorf("root commit on unexpected lane %d", lanes[3].NodeLane)
	}
}

func TestCloseTab(t *testing.T) {
	a := newTestApp(t)
	n := len(a.tabs)
	a.closeTab(n - 1)
	if len(a.tabs) != n-1 {
		t.Fatalf("tabs = %d, want %d", len(a.tabs), n-1)
	}
}
