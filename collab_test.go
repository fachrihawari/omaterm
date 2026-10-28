package main

import (
	"path/filepath"
	"testing"

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
