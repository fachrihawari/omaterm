package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/session"
)

// A session's row shows its agent's last word on its turn under its name,
// unless the sidebar is compact.
func TestSidebarShowsSummary(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	id := a.activeSplitLeaf()
	a.status[id] = session.Info{ID: id, Session: a.tabs[0].ID, Agent: "claude", Process: "claude", Status: session.Idle,
		Progress: -1, Dir: a.workspace, Summary: "All tests pass now"}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if r, ok := tr.Find("All tests pass now"); !ok || r.X > a.sidebarWidth() {
		t.Fatalf("the summary isn't in the sidebar (found %v at %+v)", ok, r)
	}
	a.settings.Density = "compact"
	tr.Frame()
	if tr.HasText("All tests pass now") {
		t.Fatal("the compact sidebar shows the summary")
	}
}
