package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/crash"
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
