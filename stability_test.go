package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/crash"
	"omaterm/internal/model"
)

// Regression tests of the stability work (0.9.2 on): each names the bug
// it keeps fixed.

// A crash a past run left shows a banner until dismissed.
func TestCrashBanner(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.crashes = []crash.Report{{Name: "app", Path: "/nonexistent/crash-app.log"}}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("omaterm quit unexpectedly last time. Its report can help fix it.") {
		t.Fatal("no crash banner")
	}
	if err := tr.Click("Dismiss"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if len(a.crashes) != 0 || tr.HasText("Show Report") {
		t.Fatal("the banner stayed after Dismiss")
	}
}

// A session closed (by the omaterm command, or its shell exiting) in a
// project that isn't shown left that project's kept workspace pointing
// at a tab that was gone: showing it again panicked.
func TestCloseSessionInHiddenProject(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	other := t.TempDir()
	pi := a.addProject(other, "")
	a.switchProject(pi)
	for i := 0; i < 2; i++ {
		a.newTerminalTab()
	}
	last := a.tabs[len(a.tabs)-1].ID
	a.activeTab = len(a.tabs) - 1
	a.switchProject(0) // the other project is kept, its last tab active
	if err := a.cliCloseSession(last); err != nil {
		t.Fatal(err)
	}
	a.switchProject(pi)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame() // panicked: index out of range
	if a.activeTab >= len(a.tabs) {
		t.Fatalf("activeTab %d with %d tabs", a.activeTab, len(a.tabs))
	}
}

// A shell exiting in a project that isn't shown closes its session there
// too: it stayed as a dead session.
func TestPaneExitInHiddenProject(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	other := t.TempDir()
	pi := a.addProject(other, "")
	a.switchProject(pi)
	a.newTerminalTab()
	tab := a.tabs[a.activeTab]
	id := tab.Split.termIDs()[0]
	if a.term(id) == nil {
		t.Fatal("the pane did not start")
	}
	a.switchProject(0)
	a.paneExited(id)
	if _, _, _, ok := a.findPane(id); ok {
		t.Fatal("the exited pane's session is still there")
	}
}

// A state.json that can't be read is kept, copied, and never written over
// or used to end panes; 0.9.1 started empty, ended every daemon pane, and
// removed the file as the window closed.
func TestBrokenStateKept(t *testing.T) {
	path := stateFilePath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	broken := []byte(`{"version": 2, "projects": [`)
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Remove(path)
		bad, _ := filepath.Glob(path + ".bad-*")
		for _, f := range bad {
			os.Remove(f)
		}
	})
	a := newApp()
	defer stopTermPanes(a)
	if a.stateLoadFailed == "" {
		t.Fatal("the broken file went unnoticed")
	}
	if b, err := os.ReadFile(a.stateLoadFailed); err != nil || string(b) != string(broken) {
		t.Fatalf("no copy of the broken file: %v", err)
	}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("Show File") {
		t.Fatal("no banner about the broken file")
	}
}

// A project whose folder is missing at launch keeps its sessions; 0.9.1
// dropped it, and its sessions, at the next save.
func TestMissingProjectFolderKept(t *testing.T) {
	path := stateFilePath()
	gone := filepath.Join(t.TempDir(), "unplugged")
	st := model.State{Projects: []model.Project{{ID: "pgone", Name: "gone", Dir: gone}},
		Sessions: []model.Session{{ID: "sgone", Project: "pgone", Name: "agent",
			Layout: &model.Layout{Pane: "xgone"}, Panes: []model.Pane{{ID: "xgone", Dir: gone}}}}}
	if err := model.Save(path, st); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })
	a := newApp()
	defer stopTermPanes(a)
	if len(a.unavailable) != 1 || a.unavailable[0].ID != "pgone" {
		t.Fatalf("unavailable = %+v", a.unavailable)
	}
	saved := a.state()
	if len(saved.Projects) != 1 || len(saved.Sessions) != 1 || saved.Sessions[0].ID != "sgone" {
		t.Fatalf("the save drops the missing project: %+v", saved)
	}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("folder missing") {
		t.Fatal("the sidebar doesn't show the missing project")
	}
}
