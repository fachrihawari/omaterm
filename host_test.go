package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/ctl"
	"omaterm/internal/proto"
)

// The control socket drives the app's sessions as the omaterm command
// does: make one, split it, type into a pane, read it, wait for its
// command, close it.
func TestControlSocket(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.host = newLocalHost(a)
	s, err := ctl.Listen(filepath.Join(t.TempDir(), "omaterm.sock"), a.host, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a.ctl = s
	c, err := client.Dial(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var made proto.Created
	if err := c.Call("create", proto.CreateParams{Name: "probe", Dir: a.workspace, Command: []string{"sh", "-c", "echo HELLO_SOCKET; sleep 0.3; exit 3"}}, &made); err != nil {
		t.Fatal(err)
	}
	var info proto.Pane
	if err := c.Call("wait", proto.WaitParams{ID: made.Pane, Exit: true, TimeoutMS: 10000}, &info); err != nil {
		t.Fatal(err)
	}
	if info.CommandExit == nil || *info.CommandExit != 3 {
		t.Fatalf("command exit %+v", info.CommandExit)
	}
	var out proto.ReadResult
	if err := c.Call("read", proto.ReadParams{ID: made.Pane, Scrollback: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, "HELLO_SOCKET") {
		t.Fatalf("read %q", out.Text)
	}

	var split proto.Created
	if err := c.Call("split", proto.CreateParams{Split: made.Pane, Direction: "down", Command: []string{"cat"}}, &split); err != nil {
		t.Fatal(err)
	}
	if split.Session != made.Session {
		t.Fatalf("split went to %s", split.Session)
	}
	if err := c.Call("send", proto.SendParams{ID: split.Pane, Data: "typed-through-socket\r"}, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.Call("read", proto.ReadParams{ID: split.Pane}, &out)
		if strings.Contains(out.Text, "typed-through-socket") || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out.Text, "typed-through-socket") {
		t.Fatalf("send/read %q", out.Text)
	}

	var st proto.State
	if err := c.Call("state", nil, &st); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range st.Sessions {
		if s.ID == made.Session {
			found = len(s.Panes) == 2 && s.Name == "probe"
		}
	}
	if !found {
		t.Fatalf("state: %+v", st.Sessions)
	}

	if err := c.Call("rename", proto.RenameParams{ID: made.Session, Name: "renamed"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Call("close", proto.IDParams{ID: made.Session}, nil); err != nil {
		t.Fatal(err)
	}
	if a.panes.Get(made.Pane) != nil || a.panes.Get(split.Pane) != nil {
		t.Fatal("closing the session left its panes")
	}
	if proto.CodeOf(c.Call("send", proto.SendParams{ID: "nope", Data: "x"}, nil)) != proto.CodeNotFound {
		t.Fatal("send to no pane")
	}
	if proto.CodeOf(c.Call("do", proto.DoParams{Action: "view.toggleSidebar"}, nil)) != proto.CodeRemoteControl {
		t.Fatal("do without Remote Control")
	}
}

func TestSplitAt(t *testing.T) {
	root := splitAt(&splitNode{TermID: "a"}, "a", "b", splitHorizontal, true, 0.3)
	if root.Dir != splitHorizontal || root.First.TermID != "b" || root.Ratio != 30 {
		t.Fatalf("%+v", root)
	}
}

// An agent's hook finds its pane by its process's ancestry, and the pane
// keeps the agent's session to resume.
func TestAgentSessionByPID(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.termCmd = []string{"sh", "-c", "sleep 30"}
	id := a.activeSplitLeaf()
	if a.term(id) == nil {
		t.Fatal("no pane")
	}
	pid := a.panes.Get(id).Info().PID
	v, err := a.agentSession(proto.AgentSessionParams{PID: pid, Agent: "claude", Session: "abc-123"})
	if err != nil {
		t.Fatal(err)
	}
	if r := v.(proto.AgentSessionResult); r.Pane != id || r.Away {
		t.Fatalf("%+v", r)
	}
	if s := a.panes.Get(id).Info().AgentSession; s == nil || s.ID != "abc-123" {
		t.Fatalf("session %+v", s)
	}
	if _, err := a.agentSession(proto.AgentSessionParams{PID: pid, Agent: "claude", Session: "bad;id"}); err == nil {
		t.Fatal("accepted a bad id")
	}
}
