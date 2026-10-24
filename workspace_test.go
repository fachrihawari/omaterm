package main

import (
	"path/filepath"
	"strings"
	"testing"

	"omaterm/internal/agent"
	"omaterm/internal/model"
	"omaterm/internal/session"
	"omaterm/internal/shellcmd"
)

func twoProjectApp(t *testing.T) *appState {
	t.Helper()
	a := newApp()
	a.win = nil
	a.wsOwner = ""
	a.ws = map[string]*projectWS{}
	a.termCmd = []string{"sleep", "30"}
	a.projects = []project{{ID: "pa", Name: "a", Path: t.TempDir()}, {ID: "pb", Name: "b", Path: t.TempDir()}}
	a.active = -1
	a.setProject(0)
	return a
}

// Switching projects keeps the other project's shells running: switching
// back shows the same panes.
func TestProjectSwitchKeepsTerminals(t *testing.T) {
	a := twoProjectApp(t)
	defer stopTermPanes(a)
	id := a.activeSplitLeaf()
	if a.term(id) == nil {
		t.Fatal("no terminal")
	}
	a.switchProject(1)
	if a.panes.Get(id) == nil {
		t.Fatal("switching away closed the pane")
	}
	if a.activeSplitLeaf() == id {
		t.Fatal("project b shows project a's pane")
	}
	a.switchProject(0)
	if a.activeSplitLeaf() != id {
		t.Fatalf("switching back shows %q, want %q", a.activeSplitLeaf(), id)
	}
}

// Removing a project ends its sessions, not the others'.
func TestRemoveProjectClosesOnlyItsPanes(t *testing.T) {
	a := twoProjectApp(t)
	defer stopTermPanes(a)
	ida := a.activeSplitLeaf()
	a.term(ida)
	a.switchProject(1)
	idb := a.activeSplitLeaf()
	a.term(idb)
	a.removeProject(0)
	if a.panes.Get(ida) != nil {
		t.Error("removed project's pane still runs")
	}
	if a.panes.Get(idb) == nil {
		t.Error("other project's pane was closed")
	}
	if a.project() == nil || a.project().ID != "pb" {
		t.Fatalf("active project %+v", a.project())
	}
}

// Sessions and their splits survive a restart: what state() saves, a new
// app shows.
func TestSessionsRoundTrip(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	a := twoProjectApp(t)
	defer stopTermPanes(a)
	first := a.activeSplitLeaf()
	a.splitPane(first, splitVertical)
	a.newTerminalTabReal()
	a.tabs[len(a.tabs)-1].Label = "server"
	if err := model.Save(filepath.Join(cfg, "omaterm", "state.json"), a.state()); err != nil {
		t.Fatal(err)
	}

	b := newApp()
	b.win = nil
	if b.project() == nil || b.project().ID != "pa" {
		t.Fatalf("project %+v", b.project())
	}
	if len(b.tabs) != 2 || b.tabs[1].Label != "server" {
		t.Fatalf("tabs %+v", b.tabs)
	}
	if got := b.tabs[0].Split.termIDs(); len(got) != 2 || got[0] != first {
		t.Fatalf("split %v", got)
	}
	// Project b was never shown: its sessions come back as saved.
	b.switchProject(1)
	if len(b.tabs) != 1 {
		t.Fatalf("project b tabs %+v", b.tabs)
	}
}

func TestLayoutConversion(t *testing.T) {
	n := &splitNode{Dir: splitVertical, Ratio: 30, First: &splitNode{TermID: "a"},
		Second: &splitNode{Dir: splitHorizontal, Ratio: 50, First: &splitNode{TermID: "b"}, Second: &splitNode{TermID: "c"}}}
	l := toLayout(n)
	if !l.Row || l.Ratio != 0.3 || l.B.Row {
		t.Fatalf("%+v", l)
	}
	back := fromLayout(l)
	if back.Dir != splitVertical || back.Ratio != 30 || back.Second.Dir != splitHorizontal {
		t.Fatalf("%+v", back)
	}
	if got := back.termIDs(); len(got) != 3 || got[2] != "c" {
		t.Fatal(got)
	}
}

// A pane running an agent whose hook never told its conversation saves
// the agent, so a restart continues the folder's last conversation.
func TestStateSavesAgentWithoutSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newApp()
	a.win = nil
	a.projects = []project{{Name: "test", Path: t.TempDir(), Tabs: []string{"dev"}}}
	a.setProject(0)
	id := a.activeSplitLeaf()
	a.status[id] = session.Info{ID: id, Agent: "opencode", Status: session.Idle}
	for _, s := range a.state().Sessions {
		for _, p := range s.Panes {
			if p.ID == id {
				if p.Agent == nil || p.Agent.Agent != "opencode" || p.Agent.ID != "" {
					t.Fatalf("saved agent %+v, want opencode without an id", p.Agent)
				}
				if got := agent.ResumeCommand(p.Agent); strings.Join(got, " ") != "opencode --continue" {
					t.Fatalf("resume %q", got)
				}
				return
			}
		}
	}
	t.Fatal("pane not saved")
}

// A project's first session is named as every new one is, after the
// shell, so the pane's own title can replace it.
func TestFirstSessionNamedAfterShell(t *testing.T) {
	a := newApp()
	a.win = nil
	a.projects = []project{{Name: "test", Path: t.TempDir()}}
	a.setProject(0)
	if len(a.tabs) != 1 || a.tabs[0].Label != shellcmd.Name(shellcmd.Login()) || !autoLabel(a.tabs[0].Label) {
		t.Fatalf("tabs %+v: want one named after the shell", a.tabs)
	}
}
