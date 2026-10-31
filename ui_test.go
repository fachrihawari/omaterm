package main

import (
	"testing"
	"time"

	"omaterm/internal/session"
)

func leaves(n *splitNode) string {
	s := ""
	for _, id := range n.termIDs() {
		s += id
	}
	return s
}

func TestSwapLeaves(t *testing.T) {
	n := &splitNode{Dir: splitVertical, First: &splitNode{TermID: "a"},
		Second: &splitNode{Dir: splitHorizontal, First: &splitNode{TermID: "b"}, Second: &splitNode{TermID: "c"}}}
	swapLeaves(n, "a", "c")
	if got := leaves(n); got != "cba" {
		t.Fatalf("leaves %s", got)
	}
}

func TestMoveLeafEachSide(t *testing.T) {
	for zone, want := range map[dropZone]struct {
		dir   splitDir
		order string
	}{
		zoneLeft: {splitVertical, "ab"}, zoneRight: {splitVertical, "ba"},
		zoneUp: {splitHorizontal, "ab"}, zoneDown: {splitHorizontal, "ba"},
	} {
		root := &splitNode{Dir: splitVertical, First: &splitNode{TermID: "a"}, Second: &splitNode{TermID: "b"}}
		got := moveLeaf(root, "a", "b", zone)
		if got.isLeaf() || got.Dir != want.dir || leaves(got) != want.order {
			t.Errorf("zone %d: dir %d order %s", zone, got.Dir, leaves(got))
		}
	}
}

func TestMoveLeafIntoSibling(t *testing.T) {
	root := &splitNode{Dir: splitVertical, First: &splitNode{TermID: "a"},
		Second: &splitNode{Dir: splitHorizontal, First: &splitNode{TermID: "b"}, Second: &splitNode{TermID: "c"}}}
	got := moveLeaf(root, "c", "a", zoneLeft)
	if leaves(got) != "cab" {
		t.Fatalf("leaves %s", leaves(got))
	}
}

func TestZoneAt(t *testing.T) {
	cases := map[[2]float32]dropZone{
		{0.5, 0.5}: zoneSwap, {0.05, 0.5}: zoneLeft, {0.95, 0.5}: zoneRight, {0.5, 0.05}: zoneUp, {0.5, 0.95}: zoneDown,
	}
	for uv, want := range cases {
		if got := zoneAt(uv[0], uv[1]); got != want {
			t.Errorf("zoneAt%v = %d, want %d", uv, got, want)
		}
	}
}

func TestAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "now", 3 * time.Minute: "3m", 2 * time.Hour: "2h", 50 * time.Hour: "2d", 20 * 24 * time.Hour: "2w",
	} {
		if got := ago(d); got != want {
			t.Errorf("ago(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestProgramIcons(t *testing.T) {
	if programIcon("vim") != programIcon("vi") || programIcon("nvim") == programIcon("zsh") {
		t.Error("aliases")
	}
	if programIcon("zsh") != iconTerminal {
		t.Error("shells take the terminal icon")
	}
}

func TestStatusState(t *testing.T) {
	cases := []struct {
		i    session.Info
		want iconState
	}{
		{session.Info{Status: session.Working, Progress: -1}, iconWorking},
		{session.Info{Status: session.Working, Progress: 40}, iconProgress},
		{session.Info{Status: session.Waiting}, iconWaiting},
		{session.Info{Status: session.Idle, Unread: true}, iconDone},
		{session.Info{Status: session.Idle, Message: "Deployed"}, iconDoneSeen},
		{session.Info{Status: session.Idle}, iconNone},
	}
	for _, c := range cases {
		if got := statusState(c.i); got != c.want {
			t.Errorf("%+v: %d, want %d", c.i, got, c.want)
		}
	}
}

func TestPaneTitle(t *testing.T) {
	if got := paneTitle(session.Info{Agent: "claude"}, nil); got != "Claude Code" {
		t.Error(got)
	}
	if got := paneTitle(session.Info{Title: "fachri@thinkpad:~/x", Dir: "/srv/api"}, nil); got != "api" {
		t.Errorf("shell title: %q", got)
	}
	if got := paneTitle(session.Info{Title: "⠋ vite build", Process: "node"}, nil); got != "vite build" {
		t.Errorf("program title: %q", got)
	}
}

// A Windows console titles its shell with the program's path or the
// window's name, which says no more than the folder the header shows.
func TestWindowsShellTitles(t *testing.T) {
	for _, title := range []string{
		`C:\Program Files\PowerShell\7\pwsh.exe`,
		`C:\WINDOWS\System32\WindowsPowerShell\v1.0\powershell.exe`,
		`C:\WINDOWS\system32\cmd.exe`,
		"Administrator: Windows PowerShell",
		"Windows PowerShell",
		"Command Prompt",
	} {
		if !shellTitle(title, session.Info{Dir: "/srv/api"}) {
			t.Errorf("%q is not a shell title", title)
		}
	}
	if got := paneTitle(session.Info{Title: `C:\WINDOWS\system32\cmd.exe`, Dir: "/srv/api"}, nil); got != "api" {
		t.Errorf("cmd title: %q", got)
	}
	if shellTitle(`C:\tools\node.exe`, session.Info{}) {
		t.Error("a program's path is a shell title")
	}
}

// The palette puts the session needing you first, and "> " keeps to
// commands.
func TestPaletteRanking(t *testing.T) {
	a := twoProjectApp(t)
	defer stopTermPanes(a)
	a.newTerminalTabReal()
	waiting := a.tabs[1]
	a.status[waiting.Split.termIDs()[0]] = session.Info{ID: waiting.Split.termIDs()[0], Session: waiting.ID, Status: session.Waiting}
	got := a.paletteCommands()
	if len(got) == 0 || got[0].ID != "session:"+waiting.ID {
		t.Fatalf("first entry %+v", got[0].Command)
	}
	a.cmdQuery = "> zoom"
	for _, e := range a.paletteCommands() {
		if e.Category == "Session" || e.Category == "Setting" {
			t.Fatalf("> query found %s", e.ID)
		}
	}
}
