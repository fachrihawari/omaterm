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
	st := map[string]any{"projects": []map[string]any{{"Name": "one", "Path": one}, {"Name": "two", "Path": two}}, "active": 1}
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

// The home folder, the project the app opens with none, keeps its
// sessions across launches, though it is too broad to be added as one.
func TestHomeProjectKeepsSessions(t *testing.T) {
	cfg, home := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", home)
	st := map[string]any{"version": 2, "activeProject": "phome01",
		"projects": []map[string]any{{"id": "phome01", "name": "home", "dir": home}},
		"sessions": []map[string]any{{"id": "shome01", "project": "phome01", "name": "dev",
			"layout": map[string]any{"pane": "ahome01"}, "panes": []map[string]any{{"id": "ahome01", "dir": home}}}}}
	data, _ := json.Marshal(st)
	if err := os.MkdirAll(filepath.Join(cfg, "omaterm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "omaterm", "state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	if len(a.projects) != 1 || a.projects[0].ID != "phome01" {
		t.Fatalf("projects = %+v, want the saved home project", a.projects)
	}
	if len(a.tabs) != 1 || a.tabs[0].ID != "shome01" {
		t.Fatalf("tabs = %+v, want the saved session", a.tabs)
	}
}

// The omaterm command sees the sessions of every project, the ones not
// shown this run too.
func TestStateListsUnshownProjects(t *testing.T) {
	cfg, one, two := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	st := map[string]any{"version": 2, "activeProject": "pone001",
		"projects": []map[string]any{{"id": "pone001", "name": "one", "dir": one}, {"id": "ptwo001", "name": "two", "dir": two}},
		"sessions": []map[string]any{
			{"id": "sone001", "project": "pone001", "layout": map[string]any{"pane": "aone001"}, "panes": []map[string]any{{"id": "aone001"}}},
			{"id": "stwo001", "project": "ptwo001", "layout": map[string]any{"pane": "atwo001"}, "panes": []map[string]any{{"id": "atwo001"}}}}}
	data, _ := json.Marshal(st)
	os.MkdirAll(filepath.Join(cfg, "omaterm"), 0o755)
	if err := os.WriteFile(filepath.Join(cfg, "omaterm", "state.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	got := map[string]bool{}
	for _, s := range a.protoState(time.Now()).Sessions {
		got[s.ID] = true
	}
	if !got["sone001"] || !got["stwo001"] {
		t.Fatalf("sessions %v, want both projects'", got)
	}
}
