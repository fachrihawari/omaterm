package main

import (
	"slices"
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

// Both side panels resize by their edge, within their bounds, and a double
// click on the edge brings the width back to the default.
func TestPanelEdgesResize(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.sidebarVisible, a.inspectorVisible = true, true
	a.sidebarW, a.inspectorW = sidebarDefaultW, inspectorDefaultW
	// The sidebar's width animates to what it was dragged to; the test
	// looks for the edge where the width is, not where it is on its way.
	a.settings.ReduceMotion = true
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	drag := func(x, dx float32) {
		t.Helper()
		tr.Press(x, 450)
		tr.Frame()
		tr.Move(x+dx, 450)
		tr.Frame()
		tr.Release(x+dx, 450)
		tr.Frame()
	}
	drag(a.sidebarWidth()-3, 60)
	if a.sidebarW != sidebarDefaultW+60 {
		t.Fatalf("sidebar after a drag: %v", a.sidebarW)
	}
	drag(a.sidebarWidth()-3, 1000)
	if a.sidebarW != sidebarMaxW {
		t.Fatalf("sidebar past its bound: %v", a.sidebarW)
	}
	gap := a.paneGap()
	edge := func() float32 { return 1440 - gap - a.inspectorWidth() + 3 }
	drag(edge(), -50)
	if a.inspectorW != inspectorDefaultW+50 {
		t.Fatalf("inspector after a drag: %v", a.inspectorW)
	}
	drag(edge(), 1000)
	if a.inspectorW != inspectorMinW {
		t.Fatalf("inspector past its bound: %v", a.inspectorW)
	}
	x := edge()
	tr.ClickAt(x, 450)
	tr.ClickAt(x, 450)
	tr.Frame()
	if a.inspectorW != inspectorDefaultW {
		t.Fatalf("inspector after a double click: %v", a.inspectorW)
	}
}

// What can be added is one + away: the sidebar's + lists a session, an
// agent, a process and a project; a project's own menu starts with the
// first three; an empty project offers them as links.
func TestSidebarAddMenu(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.projects = append(a.projects, project{ID: "empty-project", Name: "quiet", Path: t.TempDir()})
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if err := tr.Click("New"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	want := []string{"New Session", "New Agent…", "Add Process…", "-", "Open Project…"}
	if got := tr.Menu(); !slices.Equal(got, want) {
		t.Fatalf("the + menu lists %q, want %q", tr.Menu(), want)
	}
	if err := tr.ChooseMenuItem("Add Process…"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.procDialog == nil || !a.procDialog.open {
		t.Fatal("Add Process… from the + menu did not open the process dialog")
	}
	a.procDialog = nil
	tr.Frame()

	head, ok := tr.Find("test")
	if !ok {
		t.Fatal("project header is missing")
	}
	tr.RightClickAt(head.X+head.W/2, head.Y+head.H/2)
	tr.Frame()
	if menu := tr.Menu(); len(menu) < 3 || !slices.Equal(menu[:3], want[:3]) {
		t.Fatalf("the project's menu starts with %q", menu)
	}
	tr.CloseMenu()

	for _, link := range []string{"Nothing here yet", "New session", "New agent", "Add process"} {
		if !tr.HasText(link) {
			t.Fatalf("the empty project does not offer %q", link)
		}
	}
	tabs := len(a.projectTabs("empty-project"))
	if err := tr.Click("New session"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.projects[a.active].ID != "empty-project" || len(a.tabs) != tabs+1 {
		t.Fatalf("New session in the empty project: active=%q tabs=%d", a.projects[a.active].ID, len(a.tabs))
	}
}
