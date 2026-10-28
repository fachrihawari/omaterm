//go:build linux

package mail

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omaterm/internal/proto"
	"omaterm/internal/session"
)

// fakeAgent is a script named claude, as omaterm knows the agent by its
// process: it takes bracketed pastes, says it is idle, and writes what
// it is given to its log; a message ended, it works a moment, then is
// done.
const fakeAgent = `#!/bin/sh
printf '\033[?2004h\033]7501;state=idle\007'
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$LOG"
  case "$line" in
  *'201~'*) printf '\033]7501;state=working\007'; sleep 0.5; printf '\033]7501;state=done\007' ;;
  esac
done
`

// world is a registry running panes, and its mailbox.
type world struct {
	t   *testing.T
	reg *session.Registry
	box *Mailbox
	dir string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	t.Parallel()
	w := &world{t: t, reg: session.NewRegistry(), dir: t.TempDir()}
	w.box = New(w.reg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go w.reg.Run(ctx)
	go w.box.Run(ctx)
	if err := os.WriteFile(filepath.Join(w.dir, "claude"), []byte(fakeAgent), 0o755); err != nil {
		t.Fatal(err)
	}
	return w
}

// agent starts a fake agent in pane id, returning its log.
func (w *world) agent(id string) string {
	w.t.Helper()
	log := filepath.Join(w.dir, id+".log")
	// In a job of its own, as an agent run from a shell is.
	w.start(id, []string{"/bin/sh", "-c", "set -m; '" + filepath.Join(w.dir, "claude") + "'; :"}, "LOG="+log)
	return log
}

func (w *world) start(id string, cmd []string, env ...string) *session.Pane {
	w.t.Helper()
	p, err := w.reg.Start(session.StartOptions{ID: id, Session: "s-" + id, Command: cmd, Exec: true, Env: env, Cols: 100, Rows: 30})
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { w.reg.Remove(id) })
	return p
}

// eventually waits until ok, for 10 seconds at most.
func (w *world) eventually(what string, ok func() bool) {
	w.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			w.t.Fatalf("waiting for %s: %+v", what, w.box.List(""))
		}
	}
}

func read(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

func (w *world) mail(id string) proto.Mail {
	for _, m := range w.box.List("") {
		if m.ID == id {
			return m
		}
	}
	return proto.Mail{}
}

// A message waits for its agent to be free, is pasted into its prompt
// saying who it is from and how to answer, and its answer comes to the
// one waiting for it.
func TestAskAndReply(t *testing.T) {
	w := newWorld(t)
	log := w.agent("b")
	m, err := w.box.Ask(proto.MailAskParams{To: "b", ToName: "worker", Text: "What is 2+2?\nShow your work."})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		m   proto.Mail
		err error
	}
	done := make(chan result, 1)
	go func() {
		m, err := w.box.Await(context.Background(), m.ID)
		done <- result{m, err}
	}()
	w.eventually("the message typed", func() bool { return strings.Contains(read(log), "201~") })
	got := read(log)
	for _, want := range []string{"\x1b[200~[omaterm] Message m1 from a command run outside omaterm, not from the user:\n",
		"What is 2+2?\nShow your work.\n", "Answer it with: omaterm reply m1"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %q", want, got)
		}
	}
	if _, err := w.box.Reply(proto.MailReplyParams{From: "elsewhere", Text: "5"}); err == nil {
		t.Error("an answer from a pane the message wasn't for")
	}
	if _, err := w.box.Reply(proto.MailReplyParams{From: "b", Text: "4"}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.m.State != proto.MailReplied || r.m.Reply != "4" {
			t.Fatalf("%+v %v", r.m, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the answer didn't come")
	}
	if _, err := w.box.Reply(proto.MailReplyParams{ID: "m1", From: "b", Text: "4 again"}); err == nil {
		t.Error("answered twice")
	}
}

// An agent that stops without answering ends the wait for its answer,
// which shows the end of its screen.
func TestNoReply(t *testing.T) {
	w := newWorld(t)
	w.agent("b")
	m, err := w.box.Ask(proto.MailAskParams{To: "b", Text: "Do it"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	got, err := w.box.Await(ctx, m.ID)
	if err != nil || got.State != proto.MailNoReply {
		t.Fatalf("%+v %v", got, err)
	}
	// A late answer, nobody waiting, goes to no one: it has no asker.
	if _, err := w.box.Reply(proto.MailReplyParams{From: "b", Text: "done after all"}); err != nil {
		t.Fatal(err)
	}
}

// The answer to a message nobody waits for comes to its asker as a
// message of its own, once the asker is free.
func TestAnswerComesToTheAsker(t *testing.T) {
	w := newWorld(t)
	asker, worker := w.agent("a"), w.agent("b")
	m, err := w.box.Ask(proto.MailAskParams{From: "a", FromName: "lead", To: "b", ToName: "w1", Text: "Fix the login", NoWait: true})
	if err != nil {
		t.Fatal(err)
	}
	w.eventually("the task typed", func() bool { return strings.Contains(read(worker), `from the agent in session "lead" (pane a)`) })
	if _, err := w.box.Reply(proto.MailReplyParams{ID: m.ID, From: "b", Text: "Fixed: the cookie expired."}); err != nil {
		t.Fatal(err)
	}
	w.eventually("the answer typed", func() bool { return strings.Contains(read(asker), "201~") })
	if got := read(asker); !strings.Contains(got, "[omaterm] w1's answer to your message m1:\nFixed: the cookie expired.") {
		t.Fatalf("the asker got %q", got)
	}
}

// An agent waiting for another's answer can't be asked by it in turn:
// neither could answer.
func TestCycle(t *testing.T) {
	w := newWorld(t)
	w.start("a", []string{"sleep", "30"})
	w.start("b", []string{"sleep", "30"})
	m, err := w.box.Ask(proto.MailAskParams{From: "a", To: "b", ToName: "w1", Text: "Fix it"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.box.Await(ctx, m.ID)
	w.eventually("a waiting", func() bool {
		w.box.mu.Lock()
		defer w.box.mu.Unlock()
		return w.box.find(m.ID).waiters > 0
	})
	_, err = w.box.Ask(proto.MailAskParams{From: "b", To: "a", ToName: "lead", Text: "Which one?"})
	if err == nil || !strings.Contains(err.Error(), "lead is waiting for your answer to m1: give it with omaterm reply m1") {
		t.Fatalf("asked back: %v", err)
	}
}

// A message waits while the user types in its agent's pane, and for a
// pane with no agent, a while, then fails.
func TestHolds(t *testing.T) {
	w := newWorld(t)
	log := w.agent("b")
	w.start("sh", []string{"sleep", "60"})
	w.eventually("the agent known", func() bool { return w.reg.Get("b").Info().Agent == "claude" })
	time.Sleep(1100 * time.Millisecond) // its screen settled
	w.reg.Get("b").Write([]byte("half a thought"))
	m, _ := w.box.Ask(proto.MailAskParams{To: "b", Text: "Hi"})
	w.box.Tick(time.Now())
	if got := w.mail(m.ID); got.State != proto.MailQueued || got.Why != "the user is typing in it" {
		t.Fatalf("while typing: %+v", got)
	}
	if info := w.reg.Get("b").Info(); info.Inbox != 1 || info.Held != "the user is typing in it" {
		t.Fatalf("the pane shows %d held for %q", info.Inbox, info.Held)
	}
	w.box.Tick(time.Now().Add(5 * time.Second))
	if got := w.mail(m.ID); got.Why != "the user left a draft in it" {
		t.Fatalf("with a draft: %+v", got)
	}
	w.reg.Get("b").Write([]byte("\r"))
	w.eventually("delivered", func() bool { return strings.Contains(read(log), "Hi") })

	n, _ := w.box.Ask(proto.MailAskParams{To: "sh", ToName: "server", Text: "Hi"})
	w.box.Tick(time.Now())
	if got := w.mail(n.ID); got.State != proto.MailQueued {
		t.Fatalf("a pane just started: %+v", got)
	}
	w.box.Tick(time.Now().Add(time.Minute))
	if got := w.mail(n.ID); got.State != proto.MailFailed || !strings.Contains(got.Why, "no agent runs in server") {
		t.Fatalf("no agent: %+v", got)
	}
}

// What is typed can't end the paste, nor act; without bracketed pastes,
// it goes on one line.
func TestPaste(t *testing.T) {
	if got := string(paste("a\x1b[201~b\x07c\u009bd\r\ne\tf", true)); got != "\x1b[200~a[201~bcd\ne\tf\x1b[201~" {
		t.Errorf("bracketed %q", got)
	}
	if got := string(paste("one\ntwo  three\n", false)); got != "one two three" {
		t.Errorf("flat %q", got)
	}
}

// Bad messages are refused.
func TestAskRefuses(t *testing.T) {
	w := newWorld(t)
	w.start("a", []string{"sleep", "30"})
	for _, p := range []proto.MailAskParams{{To: "a", Text: "  "}, {To: "a", Text: strings.Repeat("x", MaxText+1)},
		{From: "a", To: "a", Text: "me"}, {To: "nope", Text: "hi"}} {
		if _, err := w.box.Ask(p); err == nil {
			t.Errorf("asked %+v", p)
		}
	}
}

// A pane limited in how often it may ask another is refused past that.
func TestLimit(t *testing.T) {
	w := newWorld(t)
	w.start("a", []string{"sleep", "30"})
	w.start("b", []string{"sleep", "30"})
	w.box.Limit("b", "a", 2)
	for i := range 3 {
		_, err := w.box.Ask(proto.MailAskParams{From: "b", To: "a", ToName: "w1", Text: "Fix it", NoWait: true})
		if (err == nil) != (i < 2) {
			t.Fatalf("ask %d: %v", i+1, err)
		}
		if err != nil && !strings.Contains(err.Error(), "you asked w1 as many times as you may") {
			t.Fatal(err)
		}
	}
	if _, err := w.box.Ask(proto.MailAskParams{From: "a", To: "b", Text: "Others may"}); err != nil {
		t.Fatal(err)
	}
}

// takingAgent is fakeAgent, but a dialog comes up over its prompt as it
// starts on its first message, and takes it, as Codex's hooks to review
// do.
const takingAgent = `#!/bin/sh
printf '\033[?2004h\033]7501;state=idle\007'
first=1
while IFS= read -r line; do
  case "$line" in
  *'201~'*)
    if [ $first = 1 ]; then
      first=0
      printf '\033]7501;state=working\007'; sleep 0.4
      printf '\033]7501;state=blocked:kind=question\007'; sleep 1; printf '\033]7501;state=idle\007'
    else
      printf '%s\n' "$line" >> "$LOG"
      printf '\033]7501;state=working\007'; sleep 0.5; printf '\033]7501;state=done\007'
    fi ;;
  esac
done
`

// A message a dialog took as it was sent goes again, once the dialog is
// gone; a note too, which wants no answer.
func TestRetypedAfterADialog(t *testing.T) {
	w := newWorld(t)
	if err := os.WriteFile(filepath.Join(w.dir, "claude"), []byte(takingAgent), 0o755); err != nil {
		t.Fatal(err)
	}
	log := w.agent("b")
	m, err := w.box.Ask(proto.MailAskParams{To: "b", Text: "Review it", Note: true})
	if err != nil {
		t.Fatal(err)
	}
	w.eventually("the note retyped and taken up", func() bool {
		return strings.Contains(read(log), "Review it") && w.mail(m.ID).State == proto.MailDelivered
	})
	w.eventually("the note settled", func() bool {
		w.box.mu.Lock()
		defer w.box.mu.Unlock()
		return w.box.find(m.ID).settled()
	})
	if n := strings.Count(read(log), "Review it"); n != 1 {
		t.Fatalf("taken up %d times", n)
	}
}
