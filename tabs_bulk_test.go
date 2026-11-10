package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/procs"
	"omaterm/internal/session"
)

// bulkTabs is a tab bar of n sessions, each with a pane of its own.
func bulkTabs(n int) []topTab {
	tabs := make([]topTab, n)
	for i := range tabs {
		id := "s" + itoa(i)
		tabs[i] = topTab{ID: id, Kind: tabTerminal, Label: id, Split: &splitNode{TermID: "p" + itoa(i)}}
	}
	return tabs
}

// Close Other Tabs keeps the tab it was asked from, and shows it.
func TestCloseOtherTabs(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.tabs = bulkTabs(4)
	a.activeTab, a.activeView = 0, tabTerminal

	a.closeOtherTabsAsking(2)
	if len(a.tabs) != 1 || a.tabs[0].ID != "s2" {
		t.Fatalf("tabs = %v, want only s2", tabIDs(a.tabs))
	}
	if a.activeTab != 0 || a.activeView != tabTerminal {
		t.Fatalf("active = %d/%v, want the kept tab shown", a.activeTab, a.activeView)
	}
}

// Close Tabs to the Right and to the Left keep the tab they were asked
// from, and the ones on the other side.
func TestCloseTabsBeside(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.tabs = bulkTabs(5)
	a.activeTab, a.activeView = 2, tabTerminal

	a.closeTabsRightAsking(2)
	if got := tabIDs(a.tabs); !equalStrings(got, []string{"s0", "s1", "s2"}) {
		t.Fatalf("after Close Right: %v", got)
	}
	a.closeTabsLeftAsking(2)
	if got := tabIDs(a.tabs); !equalStrings(got, []string{"s2"}) {
		t.Fatalf("after Close Left: %v", got)
	}
}

// Close All Tabs leaves none, and the view says so rather than showing a
// tab that is gone.
func TestCloseAllTabs(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.tabs = bulkTabs(3)
	a.activeTab, a.activeView = 1, tabTerminal
	a.activePaneTermID = "p1"

	a.closeAllTabsAsking()
	if len(a.tabs) != 0 {
		t.Fatalf("tabs left: %v", tabIDs(a.tabs))
	}
	if a.activePaneTermID != "" {
		t.Errorf("the closed pane still has the keyboard: %q", a.activePaneTermID)
	}
}

// Unsaved edits are never lost unasked: closing many tabs asks once for
// the whole set, and only the answer closes them.
func TestCloseAllTabsAsksOnceForUnsaved(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.openEditor("hello.txt")
	a.editors["hello.txt"].State.Buffer.Set("changed\n")
	a.focusDiffTab("hello.txt")
	if len(a.tabs) < 2 {
		t.Fatalf("want at least two tabs, got %d", len(a.tabs))
	}

	a.closeAllTabsAsking()
	if a.closeAsk == nil {
		t.Fatal("closing unsaved edits did not ask")
	}
	if len(a.tabs) == 0 {
		t.Fatal("the tabs closed before the answer")
	}
	if !strings.Contains(a.closeAsk.text, "unsaved changes") {
		t.Errorf("dialog text = %q", a.closeAsk.text)
	}
	a.closeAsk.run()
	if len(a.tabs) != 0 {
		t.Fatalf("tabs left after confirming: %v", tabIDs(a.tabs))
	}
}

// A session still running a program is asked about once, as one tab's
// close asks, and the answer ends it.
func TestCloseAllTabsAsksForRunningProgram(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.tabs = bulkTabs(2)
	a.activeTab, a.activeView = 0, tabTerminal
	a.settings.ConfirmClose = true
	a.status = map[string]session.Info{"p0": {ID: "p0", Status: session.Working, Process: "sleep"}}

	a.closeAllTabsAsking()
	if a.closeAsk == nil {
		t.Fatal("closing a running program did not ask")
	}
	if !strings.Contains(a.closeAsk.text, "sleep is still running") {
		t.Errorf("dialog text = %q", a.closeAsk.text)
	}
	a.closeAsk.run()
	if len(a.tabs) != 0 {
		t.Fatalf("tabs left after confirming: %v", tabIDs(a.tabs))
	}
}

// A process's tab goes without ending the process: Stop is what ends it,
// and the toast says so once for the set.
func TestCloseAllTabsKeepsProcessesRunning(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	pid := a.projects[0].ID
	a.procList[pid] = []procs.Proc{{Name: "web", Status: procs.Running}}
	a.tabs = append(bulkTabs(1), topTab{
		ID: procs.SessionID(pid, "web"), Kind: tabProcess, Label: "web", Process: "web",
	})
	a.activeTab, a.activeView = 0, tabTerminal

	a.closeAllTabsAsking()
	if len(a.tabs) != 0 {
		t.Fatalf("tabs left: %v", tabIDs(a.tabs))
	}
	if len(a.queuedToasts) != 1 || !strings.Contains(a.queuedToasts[0], "keep running") {
		t.Errorf("toasts = %v, want one about the process", a.queuedToasts)
	}
}

// Collapse All closes every folder in the Files tree, keeping what was
// read so opening one again is instant.
func TestCollapseAllFiles(t *testing.T) {
	a := newTestApp(t)
	a.fileTree = &fileNode{Dir: true, Children: []*fileNode{
		{Name: "src", Dir: true, Open: true, Loaded: true, Children: []*fileNode{
			{Name: "deep", Dir: true, Open: true, Children: []*fileNode{{Name: "a.go"}}},
		}},
		{Name: "b.go"},
	}}

	a.collapseAllFiles()
	src := a.fileTree.Children[0]
	if src.Open || src.Children[0].Open {
		t.Fatal("a folder stayed open")
	}
	if !src.Loaded {
		t.Error("Collapse All dropped what was read")
	}
}

// The palette offers each bulk close only where it would close something.
func TestTabCloseCommandsEnabled(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.tabs = bulkTabs(3)
	a.activeTab = 1
	for _, id := range []string{"tab.closeOthers", "tab.closeRight", "tab.closeLeft", "tab.closeAll"} {
		cmd := commandsByID[id]
		if cmd == nil {
			t.Fatalf("%s is not in the palette", id)
		}
		if cmd.Enabled == nil || !cmd.Enabled(a) {
			t.Errorf("%s is disabled with tabs on both sides", id)
		}
	}
	a.activeTab = 0
	if commandsByID["tab.closeLeft"].Enabled(a) {
		t.Error("Close Tabs to the Left is enabled on the first tab")
	}
	a.activeTab = 2
	if commandsByID["tab.closeRight"].Enabled(a) {
		t.Error("Close Tabs to the Right is enabled on the last tab")
	}
	a.tabs = bulkTabs(1)
	a.activeTab = 0
	if commandsByID["tab.closeOthers"].Enabled(a) {
		t.Error("Close Other Tabs is enabled with one tab")
	}
}

// A tab's context menu offers the bulk closes, from the tab it was opened
// on, and they act on that tab rather than on the active one.
func TestTabMenuOffersBulkClose(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.tabs = bulkTabs(3)
	a.activeTab, a.activeView = 0, tabTerminal
	// The sidebar lists the same sessions: hidden, the pill is the only
	// "s2" the window shows.
	a.setSidebar(false)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	// The third pill: right-click it, the first one stays the active tab.
	r, ok := tr.Find("s2")
	if !ok {
		t.Fatal("the third tab's pill is not drawn")
	}
	tr.RightClickAt(r.X+r.W/2, r.Y+r.H/2)
	tr.Frame()
	items := tr.Menu()
	want := []string{"Close Other Tabs", "Close Tabs to the Left", "Close All Tabs"}
	for _, w := range want {
		if !slices.Contains(items, w) {
			t.Errorf("menu %v is missing %q", items, w)
		}
	}
	if slices.Contains(items, "Close Tabs to the Right") {
		t.Error("the last tab offers Close Tabs to the Right")
	}
	if err := tr.ChooseMenuItem("Close Other Tabs"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if got := tabIDs(a.tabs); !equalStrings(got, []string{"s2"}) {
		t.Fatalf("tabs = %v, want the right-clicked one alone", got)
	}
}

// Collapse All in the Files panel's header closes every folder, and the
// palette offers the same.
func TestFilesCollapseAllControl(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.showInspectorTab(1)
	dir := &fileNode{Name: "src", Path: filepath.Join(a.workspace, "src"), Dir: true, Open: true, Loaded: true,
		Children: []*fileNode{{Name: "a.go", Path: filepath.Join(a.workspace, "src", "a.go")}}}
	a.fileTree = &fileNode{Dir: true, Children: []*fileNode{dir}}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if err := tr.Click("Collapse All Folders"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if dir.Open {
		t.Error("Collapse All left the folder open")
	}
	cmd := commandsByID["files.collapseAll"]
	if cmd == nil || cmd.Enabled == nil || !cmd.Enabled(a) {
		t.Fatal("the palette has no Collapse All Folders")
	}
	dir.Open = true
	cmd.Run(a, nil)
	if dir.Open {
		t.Error("the command left the folder open")
	}
}

// An editor tab's menu has a close of its own, which its kind's menu
// lacks, and the bulk closes beside it.
func TestEditorTabMenuOffersClose(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.openEditor("hello.txt")
	a.setSidebar(false)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	r, ok := tr.Find("hello.txt")
	if !ok {
		t.Fatal("the editor's pill is not drawn")
	}
	tr.RightClickAt(r.X+r.W/2, r.Y+r.H/2)
	tr.Frame()
	items := tr.Menu()
	for _, w := range []string{"Close Tab", "Close Other Tabs", "Close Tabs to the Left", "Close All Tabs"} {
		if !slices.Contains(items, w) {
			t.Errorf("menu %v is missing %q", items, w)
		}
	}
	if err := tr.ChooseMenuItem("Close Tab"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	for _, tab := range a.tabs {
		if tab.Kind == tabEditor {
			t.Fatal("Close Tab left the editor open")
		}
	}
}

// tabIDs names the tabs, for a test's message.
func tabIDs(tabs []topTab) []string {
	out := make([]string, len(tabs))
	for i, t := range tabs {
		out[i] = t.ID
	}
	return out
}

// equalStrings tells whether two string slices are the same, in order.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
