package main

import (
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/model"
	"omaterm/internal/procs"
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

// A session with no status ends its row with its time, where another's
// status icon is, rather than beside an empty slot.
func TestSessionTimeTakesTheEdge(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	idle := a.activeSplitLeaf()
	a.newTerminalTab()
	done := a.activeSplitLeaf()
	now := nowFunc()
	a.status[idle] = session.Info{ID: idle, Session: a.tabs[0].ID, Status: session.Idle, Progress: -1, Dir: a.workspace,
		Since: now.Add(-2 * time.Hour).UnixMilli()}
	a.status[done] = session.Info{ID: done, Session: a.tabs[1].ID, Agent: "claude", Process: "claude", Status: session.Idle,
		Progress: -1, Dir: a.workspace, Message: "Done", Since: now.Add(-17 * time.Minute).UnixMilli()}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	tr.Frame()
	bare, ok := tr.Find("2h")
	if !ok {
		t.Fatal("no time on the session with no status")
	}
	beside, ok := tr.Find("17m")
	if !ok {
		t.Fatal("no time on the done session")
	}
	// The icon is 14 wide, 8 from the time.
	if d := (bare.X + bare.W) - (beside.X + beside.W); d < 21 || d > 23 {
		t.Errorf("the bare time ends %.1f right of the one beside an icon, want 22", d)
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
	// The inspector's edge is the gap before its card.
	edge := func() float32 { return 1440 - 2*gap - a.inspectorWidth() + gap/2 }
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
	// The + is the header's last button: Open Project is in its menu, so
	// no folder button follows it.
	if plus, ok := tr.Find("New"); !ok || a.sidebarWidth()-(plus.X+plus.W) > 16 {
		t.Fatalf("something follows the header's +: found=%v rect=%+v width=%v", ok, plus, a.sidebarWidth())
	}
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

	tr.Frame()
}

// A project with nothing in it says so in one line, and nothing starts
// there by itself: shown, it offers what can start there.
func TestEmptyProject(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.projects = append(a.projects, project{ID: "empty-project", Name: "quiet", Path: t.TempDir()})
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("No sessions") {
		t.Fatal("the empty project doesn't say it has no sessions")
	}
	// Its name shows it: no session starts, the main area offers them.
	if err := tr.Click("quiet"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.projects[a.active].ID != "empty-project" || len(a.tabs) != 0 {
		t.Fatalf("showing the empty project: active=%q tabs=%d", a.projects[a.active].ID, len(a.tabs))
	}
	for _, text := range []string{"Nothing runs in quiet yet", "New Session", "New Agent…", "Add Process…"} {
		if !tr.HasText(text) {
			t.Fatalf("the empty project's main area lacks %q", text)
		}
	}
	if err := tr.Click("New Session"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if len(a.tabs) != 1 || a.tabs[0].Kind != tabTerminal {
		t.Fatalf("New Session made %d tabs", len(a.tabs))
	}
}

// A folder opened for the first time starts with a session, as asked;
// opened again, it shows what it has.
func TestOpenFolderStartsASession(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	dir := t.TempDir()
	a.openFromArgs([]string{dir}, "")
	if p := a.project(); p == nil || p.Path != dir || len(a.tabs) != 1 || a.tabs[0].Kind != tabTerminal {
		t.Fatalf("opened %v with %d tabs", a.project(), len(a.tabs))
	}
	a.closeTab(0)
	a.switchProject(0)
	a.openFromArgs([]string{dir}, "")
	if len(a.tabs) != 0 {
		t.Fatalf("opening a known folder again started %d sessions", len(a.tabs))
	}
}

// A process's name stays readable beside its pills: an untrusted process
// with a long Dev URL showed its name as "…".
func TestProcessNameBesidePills(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	pid := a.projects[a.active].ID
	a.procList = map[string][]procs.Proc{pid: {
		{Def: procs.Def{Name: "web", Command: "x", URL: "stories.shop"}, Status: procs.Untrusted, Link: "https://stories.shop.localhost:8443"},
	}}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	r, ok := tr.Find("web")
	if !ok || r.W < 26 {
		t.Fatalf("the name is squeezed: %+v %v", r, ok)
	}
	if got := trustDetails(procs.Def{Command: "x", URL: "shop"}); got[3][1] == "none" {
		t.Fatalf("a URL process's port reads none: %v", got[3])
	}
}

// Many projects scroll in the sidebar, down to the last one; no project
// folds away, so each shows its sessions.
func TestSidebarScrollsManyProjects(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	for i := 0; i < 20; i++ {
		a.projects = append(a.projects, project{ID: "p" + itoa(i), Name: "many-" + itoa(i), Path: t.TempDir()})
	}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if r, ok := tr.Find("many-19"); ok && r.Y+r.H < 840 {
		t.Fatalf("the last project fits without scrolling: %+v", r)
	}
	tr.Scroll(100, 400, 0, 5000)
	tr.Frame()
	r, ok := tr.Find("many-19")
	if !ok || r.Y < 0 || r.Y+r.H > 860 {
		t.Fatalf("the last project isn't in sight after scrolling: %+v %v", r, ok)
	}
}

// A project whose folder moved comes back where it went, as the same
// project, its sessions with it.
func TestRelocateMissingProject(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.unavailable = []model.Project{{ID: "moved", Name: "shop", Dir: "/nowhere/shop"}}
	dir := t.TempDir()
	a.relocateUnavailable("moved", dir)
	if len(a.unavailable) != 0 {
		t.Fatal("the project still shows as missing")
	}
	p := a.project()
	if p == nil || p.ID != "moved" || p.Path != dir {
		t.Fatalf("shown: %+v", p)
	}
}

// A project moves up and down the list, the shown one staying shown.
func TestMoveProject(t *testing.T) {
	a := twoProjectApp(t)
	defer stopTermPanes(a)
	first, second := a.projects[0].ID, a.projects[1].ID
	shown := a.projects[a.active].ID
	a.moveProject(0, 1)
	if a.projects[0].ID != second || a.projects[1].ID != first {
		t.Fatalf("not swapped: %s, %s", a.projects[0].ID, a.projects[1].ID)
	}
	if a.projects[a.active].ID != shown {
		t.Errorf("the shown project changed to %s", a.projects[a.active].ID)
	}
}
