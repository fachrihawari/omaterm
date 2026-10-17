package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestStageUnstageCommit(t *testing.T) {
	a := newApp()
	tr := ui.NewTester(a.view, 1440, 900)
	a.inspectorTab = 2
	tr.Frame()

	if a.gitCount() != 5 {
		t.Fatalf("git count = %d, want 5", a.gitCount())
	}

	// Stage one file.
	a.stage("FileTree.tsx")
	tr.Frame()
	if len(a.staged) != 3 || len(a.changes) != 2 {
		t.Fatalf("after stage: staged=%d changes=%d", len(a.staged), len(a.changes))
	}
	if !tr.HasText("3") {
		t.Error("staged count badge did not update")
	}

	// Unstage it again.
	a.unstage("FileTree.tsx")
	tr.Frame()
	if len(a.staged) != 2 || len(a.changes) != 3 {
		t.Fatalf("after unstage: staged=%d changes=%d", len(a.staged), len(a.changes))
	}

	// Discard a change.
	a.discard("terminal-layout.ts")
	tr.Frame()
	if len(a.changes) != 2 {
		t.Fatalf("after discard: changes=%d", len(a.changes))
	}

	// Commit clears the staged list.
	tr.Click("Commit")
	tr.Frame()
	if len(a.staged) != 0 {
		t.Fatalf("after commit: staged=%d", len(a.staged))
	}
}

func TestSidebarAndInspectorToggle(t *testing.T) {
	a := newApp()
	tr := ui.NewTester(a.view, 1440, 900)

	tr.Key(ui.Cmd, ui.KeyB) // hide projects
	tr.Frame()
	if a.sidebarVisible {
		t.Error("Cmd+B did not hide the sidebar")
	}
	if tr.HasText("PROJECTS") {
		t.Error("sidebar still shows")
	}
	tr.Key(ui.Cmd, ui.KeyB) // show again
	tr.Frame()
	if !a.sidebarVisible || !tr.HasText("PROJECTS") {
		t.Error("Cmd+B did not show the sidebar")
	}

	tr.Key(ui.Cmd|ui.Shift, ui.KeyB) // hide inspector
	tr.Frame()
	if a.inspectorVisible || tr.HasText("PROCESSES") {
		t.Error("Cmd+Shift+B did not hide the inspector")
	}
	tr.Key(ui.Cmd|ui.Shift, ui.KeyB)
	tr.Frame()
	if !a.inspectorVisible {
		t.Error("Cmd+Shift+B did not show the inspector")
	}
}

func TestViewShortcuts(t *testing.T) {
	a := newApp()
	tr := ui.NewTester(a.view, 1440, 900)

	tr.Key(ui.Cmd, ui.Key2)
	tr.Frame()
	if a.activeView != tabEditor || !tr.HasText("TypeScript React") {
		t.Error("Cmd+2 did not show the editor")
	}
	tr.Key(ui.Cmd, ui.Key3)
	tr.Frame()
	if a.activeView != tabDiff || !tr.HasText("WORKING TREE · GitPanel.tsx") {
		t.Error("Cmd+3 did not show the diff")
	}
	tr.Key(ui.Cmd, ui.Key1)
	tr.Frame()
	if a.activeView != tabTerminal {
		t.Error("Cmd+1 did not show the terminal")
	}
}

func TestProjectSwitch(t *testing.T) {
	a := newApp()
	tr := ui.NewTester(a.view, 1440, 900)

	tr.Click("phoenix")
	tr.Frame()
	if a.project().Name != "phoenix" {
		t.Fatalf("active project = %s", a.project().Name)
	}
	if !tr.HasText("worker") || !tr.HasText("db") {
		t.Error("project tabs did not switch to phoenix's")
	}
}

func TestDiffModeToggle(t *testing.T) {
	a := newApp()
	a.activeView = tabDiff
	tr := ui.NewTester(a.view, 1440, 900)

	if !tr.HasText("INDEX · GitPanel.tsx") {
		t.Fatal("split diff not showing")
	}
	tr.Click("Inline")
	tr.Frame()
	if a.diffSplit || tr.HasText("INDEX · GitPanel.tsx") {
		t.Error("Inline did not switch the diff")
	}
	tr.Click("Split")
	tr.Frame()
	if !a.diffSplit || !tr.HasText("INDEX · GitPanel.tsx") {
		t.Error("Split did not switch back")
	}
}

func TestLayoutButtons(t *testing.T) {
	a := newApp()
	tr := ui.NewTester(a.view, 1440, 900)
	if a.layout != layoutMixed {
		t.Fatal("default layout should be mixed")
	}
	a.layout = layoutVertical
	tr.Frame()
	if !tr.HasText("pnpm api:dev") {
		t.Error("vertical split missing pane 2")
	}
	a.layout = layoutSingle
	tr.Frame()
	if tr.HasText("pnpm api:dev") {
		t.Error("single layout still shows pane 2")
	}
}

func TestCloseTab(t *testing.T) {
	a := newApp()
	tr := ui.NewTester(a.view, 1440, 900)
	n := len(a.tabs)
	a.closeTab(n - 1)
	tr.Frame()
	if len(a.tabs) != n-1 {
		t.Fatalf("tabs = %d, want %d", len(a.tabs), n-1)
	}
}
