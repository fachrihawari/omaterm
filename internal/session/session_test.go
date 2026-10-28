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
	t.Setenv("OMATERM_PANE", "outer")
	t.Setenv("OMATERM_DEV", "1")
	env := strings.Join(environment("s1", "p1", "/bin/zsh", []string{"OMATERM_SOCKET=/run/x.sock"}), "\n")
	for _, want := range []string{"OMATERM_PANE=p1", "OMATERM_SESSION=s1", "OMATERM_SOCKET=/run/x.sock", "TERM_PROGRAM=ghostty", "SHELL=/bin/zsh",
		"OMATERM_DEV=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(env, "CLAUDECODE=") || strings.Contains(env, "TMUX=") || strings.Contains(env, "OMATERM_PANE=outer") {
		t.Error("kept the outer terminal's variables")
	}
}

// What the user types is told from the terminal's answers and mouse
// reports, and from what omaterm's mailbox types.
func TestTyping(t *testing.T) {
	r := NewRegistry()
	p := startExec(t, r, "p1", "s1", `sleep 5`)
	typed := func() (time.Time, bool) {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.typed, p.typedEnd
	}
	for _, b := range []string{"\x1b[I", "\x1b[O", "\x1b[<0;3;4M", "\x1b[<0;3;4m", "\x1b[M !!", "\x1b[2;1R"} {
		p.Write([]byte(b))
		if at, _ := typed(); !at.IsZero() {
			t.Fatalf("%q counted as typing", b)
		}
	}
	p.Deliver([]byte("from the mailbox\r"))
	if at, _ := typed(); !at.IsZero() {
		t.Fatal("the mailbox's delivery counted as typing")
	}
	for _, c := range []struct {
		in  string
		end bool
	}{{"x", false}, {"\r", true}, {"ab\x1b[200~c\x1b[201~", false}, {"\x03", true}, {"\x1b", true},
		{"\x1b[13u", true}, {"\x1b[99;5u", true}, {"\x1b[97u", false}, {"\x15", true}} {
		p.Write([]byte(c.in))
		if at, end := typed(); at.IsZero() || end != c.end {
			t.Errorf("%q: typed %v, ends %v", c.in, !at.IsZero(), end)
		}
	}
}

// A message waits for an agent at its prompt, and for the user to stop
// typing and leave no draft; it is pasted bracketed when the agent asks.
func TestMailHold(t *testing.T) {
	prev := procName
	procName = func(pid int) string { return "claude" }
	defer func() { procName = prev }()
	r := NewRegistry()
	p := startExec(t, r, "p1", "s1", `set -m; printf '\033[?2004h'; sleep 0.3; printf '\033]7501;state=working\007'; sleep 0.3; `+
		`printf '\033]7501;state=done\007\n────────────────────\n❯ \n────────────────────\n'; sleep 5; :`)
	if hold, _ := p.MailHold(time.Now()); hold != "no agent runs in it" {
		t.Fatalf("hold %q", hold)
	}
	time.Sleep(200 * time.Millisecond)
	r.Tick(time.Now())
	waitFor(t, p, "working", func(i Info) bool { return i.Status == Working })
	if hold, _ := p.MailHold(time.Now()); hold != "it is working" {
		t.Fatalf("hold %q", hold)
	}
	waitFor(t, p, "done", func(i Info) bool { return i.Status == Idle && len(i.Records) > 0 })
	time.Sleep(100 * time.Millisecond)
	r.Tick(time.Now()) // the screen read: its prompt shows
	later := time.Now().Add(3 * time.Second)
	if hold, bracketed := p.MailHold(later); hold != "" || !bracketed {
		t.Fatalf("hold %q, bracketed %v", hold, bracketed)
	}
	p.Write([]byte("hel"))
	now := time.Now()
	for _, c := range []struct {
		after time.Duration
		hold  string
	}{{time.Second, "the user is typing in it"}, {3 * time.Second, "the user left a draft in it"}, {2 * time.Minute, ""}} {
		if hold, _ := p.MailHold(now.Add(c.after)); hold != c.hold {
			t.Errorf("%v after typing: hold %q", c.after, hold)
		}
	}
	p.Write([]byte("\r"))
	if hold, _ := p.MailHold(time.Now().Add(3 * time.Second)); hold != "" {
		t.Fatalf("after Enter: hold %q", hold)
	}
}
