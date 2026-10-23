package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The saved active project survives startup: start loads it, it does not
// switch back to the first project.
func TestStartupKeepsSavedActiveProject(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	one, two := t.TempDir(), t.TempDir()
	st := savedState{Projects: []project{{Name: "one", Path: one}, {Name: "two", Path: two}}, Active: 1}
	data, _ := json.Marshal(st)
	if err := os.MkdirAll(filepath.Join(cfg, "omaterm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "omaterm", "state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	a := newApp()
	a.start()
	defer a.stopSystemStats()
	defer a.stopProcessRefresh()
	if a.active != 1 || a.workspace != two {
		t.Fatalf("active = %d (%q), want 1 (%q)", a.active, a.workspace, two)
	}
}

// A pane removed from a tab the user is not looking at goes from that tab,
// and leaves the focused pane of the active tab alone.
func TestBackgroundPaneExitRemovesPane(t *testing.T) {
	a := newApp()
	a.win = nil
	a.tabs = []topTab{
		{Kind: tabTerminal, Label: "bg", Split: &splitNode{TermID: "a"}},
		{Kind: tabTerminal, Label: "fg", Split: &splitNode{TermID: "c"}},
	}
	a.activeTab = 0
	a.splitPane("a", splitVertical)
	ids := a.tabs[0].Split.termIDs()
	if len(ids) != 2 {
		t.Fatalf("split: %v", ids)
	}
	a.activeTab = 1
	a.activePaneTermID = "c"

	a.removePane(ids[1])
	if !a.tabs[0].Split.isLeaf() || a.tabs[0].Split.TermID != "a" {
		t.Fatalf("background tab still holds the pane: %v", a.tabs[0].Split.termIDs())
	}
	if a.activePaneTermID != "c" {
		t.Fatalf("focused pane moved to %q", a.activePaneTermID)
	}
}

func TestRemoveLeafDeepTree(t *testing.T) {
	root := &splitNode{TermID: "a"}
	for _, id := range []string{"b", "c", "d"} {
		if r := root.splitLeaf("a", splitHorizontal, id); r != nil {
			root = r
		}
	}
	root, ok := removeLeaf(root, "c")
	if !ok {
		t.Fatal("c not removed")
	}
	got := root.termIDs()
	want := []string{"a", "d", "b"}
	if len(got) != len(want) {
		t.Fatalf("leaves = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("leaves = %v, want %v", got, want)
		}
	}
}

// Work posted with no window runs at the next frame, in order.
func TestPostRunsOnNextFrame(t *testing.T) {
	a := newApp()
	a.win = nil
	var got []int
	a.post(func() { got = append(got, 1) })
	a.post(func() { got = append(got, 2) })
	if len(got) != 0 {
		t.Fatal("posted work ran before the frame")
	}
	a.drainPosted()
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("got %v", got)
	}
}

// A shell that exits in a background tab closes its pane through the
// registry's exit event, posted to the next frame.
func TestBackgroundExitViaRegistry(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.termCmd = []string{"sh", "-c", "sleep 0.3"}
	a.tabs = []topTab{
		{ID: "s1", Kind: tabTerminal, Label: "bg", Split: &splitNode{TermID: "a"}},
		{ID: "s2", Kind: tabTerminal, Label: "fg", Split: &splitNode{TermID: "c"}},
	}
	a.activeTab = 0
	a.splitPane("a", splitVertical)
	ids := a.tabs[0].Split.termIDs()
	for _, id := range ids {
		if a.term(id) == nil {
			t.Fatal("pane did not start")
		}
	}
	a.activeTab = 1
	deadline := time.Now().Add(5 * time.Second)
	for a.panes.Len() > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		a.drainPosted()
	}
	if a.panes.Len() != 0 {
		t.Fatalf("%d panes still running", a.panes.Len())
	}
	if len(a.tabs) != 1 || a.tabs[0].ID != "s2" {
		t.Fatalf("tabs: %+v", a.tabs)
	}
}
