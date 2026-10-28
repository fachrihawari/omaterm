package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/cli"
	"omaterm/internal/client"
	"omaterm/internal/ctl"
	"omaterm/internal/proto"
)

// dialApp serves a's control socket and connects to it, as the omaterm
// command does.
func dialApp(t *testing.T, a *appState) *client.Client {
	t.Helper()
	a.host = newLocalHost(a)
	s, err := ctl.Listen(filepath.Join(t.TempDir(), "omaterm.sock"), a.host, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a.ctl = s
	c, err := client.Dial(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func sessionOf(t *testing.T, c *client.Client, id string) (proto.Session, bool) {
	t.Helper()
	var st proto.State
	if err := c.Call("state", nil, &st); err != nil {
		t.Fatal(err)
	}
	for _, s := range st.Sessions {
		if s.ID == id {
			return s, true
		}
	}
	return proto.Session{}, false
}

// A session made from an agent's pane is its session's child: saved so,
// and standing on its own once its parent closes.
func TestParentSession(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	c := dialApp(t, a)
	var lead, child proto.Created
	if err := c.Call("create", proto.CreateParams{Name: "lead", Dir: a.workspace, Command: []string{"sleep", "30"}}, &lead); err != nil {
		t.Fatal(err)
	}
	if err := c.Call("create", proto.CreateParams{Name: "w1", Dir: a.workspace, Command: []string{"sleep", "30"}, By: lead.Pane}, &child); err != nil {
		t.Fatal(err)
	}
	if s, _ := sessionOf(t, c, child.Session); s.Parent != lead.Session {
		t.Fatalf("parent %q, want %q", s.Parent, lead.Session)
	}
	saved := false
	for _, s := range a.state().Sessions {
		saved = saved || s.ID == child.Session && s.Parent == lead.Session
	}
	if !saved {
		t.Fatal("the parent isn't saved")
	}
	if err := c.Call("close", proto.IDParams{ID: lead.Session}, nil); err != nil {
		t.Fatal(err)
	}
	if s, ok := sessionOf(t, c, child.Session); !ok || s.Parent != "" {
		t.Fatalf("after its parent closed: %+v", s)
	}
}

// The sidebar shows a session under the one whose agent started it, which
// can hide it; the tabs go in that order; closing one that started others
// asks about them.
func TestSessionTree(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	make := func(name, by string) proto.Created {
		t.Helper()
		v, err := a.cliCreate(proto.CreateParams{Name: name, Dir: a.workspace, Command: []string{"sleep", "30"}, By: by})
		if err != nil {
			t.Fatal(err)
		}
		return v.(proto.Created)
	}
	lead := make("lead", "")
	w1 := make("w1", lead.Pane)
	other := make("other", "")
	w2 := make("w2", lead.Pane)
	g := make("grandchild", w1.Pane)
	index := func(id string) int {
		for i, t := range a.tabs {
			if t.ID == id {
				return i
			}
		}
		return -1
	}

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	// The tab bar names the sessions too: the sidebar's are left of it.
	inSidebar := func(name string) bool {
		r, ok := tr.Find(name)
		return ok && r.X < a.sidebarWidth()
	}
	rects := map[string]ui.Rect{}
	for _, name := range []string{"lead", "w1", "grandchild", "w2", "other"} {
		r, ok := tr.Find(name)
		if !ok {
			t.Fatalf("no %s in %v", name, tr.Texts())
		}
		rects[name] = r
	}
	for _, pair := range [][2]string{{"lead", "w1"}, {"w1", "grandchild"}, {"grandchild", "w2"}, {"w2", "other"}} {
		if rects[pair[0]].Y >= rects[pair[1]].Y {
			t.Errorf("%s isn't above %s", pair[0], pair[1])
		}
	}
	if !(rects["lead"].X < rects["w1"].X && rects["w1"].X < rects["grandchild"].X && rects["w2"].X == rects["w1"].X && rects["other"].X == rects["lead"].X) {
		t.Errorf("indents %+v", rects)
	}
	// The tab bar, and the keys, go through them in that order too.
	var order []string
	for _, t := range a.tabs {
		order = append(order, t.Label)
	}
	if got := strings.Join(order[len(order)-5:], " "); got != "lead w1 grandchild w2 other" {
		t.Errorf("tabs go %s", got)
	}

	// The chevron left of the lead's icon hides what it started.
	r := rects["lead"]
	tr.ClickAt(r.X-36, r.Y+r.H/2)
	tr.Frame()
	if !a.tabs[index(lead.Session)].Collapsed || inSidebar("w1") || !tr.HasText("+3") {
		t.Fatalf("collapsed %v: %v", a.tabs[index(lead.Session)].Collapsed, tr.Texts())
	}
	if a.activeTab == index(lead.Session) {
		t.Error("the chevron went to the session")
	}
	tr.ClickAt(r.X-36, r.Y+r.H/2)
	tr.Frame()
	if !inSidebar("w1") {
		t.Fatal("expanding didn't show w1")
	}

	// Keeping what w1 started: the grandchild stands on its own.
	a.closeTabAsking(index(w1.Session))
	if a.closeAsk == nil || a.closeAsk.alt != "Keep Them" || a.closeAsk.confirm != "Close All" ||
		!strings.Contains(a.closeAsk.text, "w1 started grandchild.") {
		t.Fatalf("ask %+v", a.closeAsk)
	}
	a.closeAsk.altRun()
	a.closeAsk = nil
	if index(w1.Session) >= 0 || index(g.Session) < 0 {
		t.Fatalf("keep them: w1 %d, grandchild %d", index(w1.Session), index(g.Session))
	}
	tr.Frame()
	if gr, _ := tr.Find("grandchild"); gr.X != rects["lead"].X {
		t.Errorf("the grandchild isn't on its own: %v", gr)
	}

	// Closing them all.
	a.closeTabAsking(index(lead.Session))
	if a.closeAsk == nil || !strings.Contains(a.closeAsk.text, "lead started w2.") {
		t.Fatalf("ask %+v", a.closeAsk)
	}
	a.closeAsk.run()
	a.closeAsk = nil
	if index(lead.Session) >= 0 || index(w2.Session) >= 0 || index(other.Session) < 0 || index(g.Session) < 0 {
		t.Fatalf("close all: lead %d, w2 %d, other %d, grandchild %d", index(lead.Session), index(w2.Session), index(other.Session), index(g.Session))
	}
}

// runCLI runs the omaterm command, returning what it printed and its exit
// status.
func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	code := cli.Run(args)
	w.Close()
	os.Stdout = stdout
	out, _ := io.ReadAll(r)
	return string(out), code
}

// omaterm wait waits for several sessions at once: for the first, or for
// all of them.
func TestWaitMany(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	dialApp(t, a)
	t.Setenv("OMATERM_SOCKET", a.ctl.Path())
	t.Setenv("OMATERM_PANE", "")
	t.Setenv("OMATERM_SESSION", "")
	for _, s := range []struct{ name, cmd string }{{"fast", "sleep 0.2; exit 3"}, {"slow", "sleep 1.5"}, {"never", "sleep 60"}} {
		if _, err := a.cliCreate(proto.CreateParams{Name: s.name, Dir: a.workspace, Command: []string{"sh", "-c", s.cmd}}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	out, code := runCLI(t, "wait", "slow", "fast", "--any", "--exit", "-t", "10s")
	if code != 0 || out != "fast exited 3\n" || time.Since(start) > time.Second {
		t.Fatalf("--any: %q, status %d, after %v", out, code, time.Since(start))
	}
	out, code = runCLI(t, "wait", "slow", "fast", "--exit", "-t", "10s")
	if code != 0 || out != "fast exited 3\nslow exited 0\n" {
		t.Fatalf("all: %q, status %d", out, code)
	}
	if _, code = runCLI(t, "wait", "fast", "never", "--exit", "-t", "300ms"); code != 1 {
		t.Fatalf("a timeout exits %d", code)
	}
	if _, code = runCLI(t, "wait", "fast", "nope", "--exit"); code != 3 {
		t.Fatalf("no such target exits %d", code)
	}
}
