//go:build linux

package session

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func startExec(t *testing.T, r *Registry, id, session, script string) *Pane {
	t.Helper()
	p, err := r.Start(StartOptions{ID: id, Session: session, Command: []string{"/bin/sh", "-c", script}, Exec: true, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Remove(id) })
	return p
}

func waitFor(t *testing.T, p *Pane, what string, until func(Info) bool) Info {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := p.Wait(ctx, until)
	if err != nil {
		t.Fatalf("waiting for %s: %+v", what, info)
	}
	return info
}

// A program's status reports set the pane's status; the user's input ends
// a wait it reported only once the program says so.
func TestReportsSetStatus(t *testing.T) {
	r := NewRegistry()
	msg := base64.StdEncoding.EncodeToString([]byte("Approve deploy?"))
	p := startExec(t, r, "p1", "s1",
		`printf '\033]7501;state=working\007'; sleep 0.3; printf '\033]7501;state=blocked:kind=permission:msg=`+msg+`\033\\'; sleep 5`)
	waitFor(t, p, "working", func(i Info) bool { return i.Status == Working })
	info := waitFor(t, p, "waiting", func(i Info) bool { return i.Status == Waiting })
	if info.Message != "Approve deploy?" || info.Kind != "permission" || !info.Unread {
		t.Fatalf("%+v", info)
	}
	// The user typed: unread goes, but the program declared it waits.
	p.Write([]byte("y"))
	info = p.Info()
	if info.Unread || info.Status != Waiting {
		t.Fatalf("after input: %+v", info)
	}
}

// Viewing a session clears its unread marks; changes in a viewed session
// are not unread.
func TestViewedSession(t *testing.T) {
	r := NewRegistry()
	r.SetViewed("s1")
	p := startExec(t, r, "p1", "s1", `printf '\033]7501;state=error\007'; sleep 5`)
	info := waitFor(t, p, "failed", func(i Info) bool { return i.Status == Failed })
	if info.Unread {
		t.Fatal("unread in the viewed session")
	}
}

// A spinner title means work; work going quiet ends after idleHold.
func TestTitleSpinner(t *testing.T) {
	r := NewRegistry()
	p := startExec(t, r, "p1", "s1", `printf '\033]0;⠋ build\007'; sleep 0.5; printf '\033]0;build done\007'; sleep 5`)
	waitFor(t, p, "title", func(i Info) bool { return strings.HasPrefix(i.Title, "⠋") })
	r.Tick(time.Now())
	if s := p.Info().Status; s != Working {
		t.Fatalf("status %s", s)
	}
	waitFor(t, p, "title change", func(i Info) bool { return i.Title == "build done" })
	t0 := time.Now()
	r.Tick(t0)
	if s := p.Info().Status; s != Idle {
		// A title with no spinner shows idle outright (visible), unlike an
		// agent's screen that merely stopped showing work.
		t.Fatalf("status %s", s)
	}
	if !p.Info().Unread {
		t.Fatal("work ending unseen should be unread")
	}
}

// The foreground program is followed, and an agent named by it.
func TestForegroundAgent(t *testing.T) {
	prev := procName
	procName = func(pid int) string { return "claude" }
	defer func() { procName = prev }()
	r := NewRegistry()
	p := startExec(t, r, "p1", "s1", `set -m; sleep 5; :`)
	time.Sleep(200 * time.Millisecond)
	r.Tick(time.Now())
	info := p.Info()
	if info.Process != "claude" || info.Agent != "claude" {
		t.Fatalf("%+v", info)
	}
}

// A shell that exits ends the pane, with its code, and tells the app.
func TestExit(t *testing.T) {
	r := NewRegistry()
	exited := make(chan int, 1)
	r.OnExit = func(id string, code int) { exited <- code }
	p := startExec(t, r, "p1", "s1", "printf hi; exit 3")
	select {
	case code := <-exited:
		if code != 3 {
			t.Fatalf("code %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no exit")
	}
	info := p.Info()
	if info.Status != Exited || info.ExitCode == nil || *info.ExitCode != 3 {
		t.Fatalf("%+v", info)
	}
	if !strings.Contains(p.Text(true, 0), "hi") {
		t.Fatalf("text %q", p.Text(true, 0))
	}
}

// A command pane reports its command's exit through OSC 133;D.
func TestCommandExit(t *testing.T) {
	r := NewRegistry()
	p, err := r.Start(StartOptions{ID: "p1", Session: "s1", Command: []string{"sh", "-c", "exit 4"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Remove("p1")
	info := waitFor(t, p, "command exit", func(i Info) bool { return i.CommandExit != nil })
	if *info.CommandExit != 4 {
		t.Fatalf("command exit %d", *info.CommandExit)
	}
}

func TestLead(t *testing.T) {
	infos := []Info{{ID: "a", Status: Working}, {ID: "b", Status: Waiting}, {ID: "c", Status: Idle, Unread: true}}
	if l, _ := Lead(infos); l.ID != "b" {
		t.Fatalf("lead %s", l.ID)
	}
}

func TestEnvironment(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("TMUX", "x")
	env := strings.Join(environment("s1", "p1", "/bin/zsh", []string{"OMATERM_SOCKET=/run/x.sock"}), "\n")
	for _, want := range []string{"OMATERM_PANE=p1", "OMATERM_SESSION=s1", "OMATERM_SOCKET=/run/x.sock", "TERM_PROGRAM=ghostty", "SHELL=/bin/zsh"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(env, "CLAUDECODE=") || strings.Contains(env, "TMUX=") {
		t.Error("kept the outer terminal's variables")
	}
}
