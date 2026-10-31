package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Two starts of one pane at once: one runs, the other is told it exists.
// Both ran, the id went twice into the order, and after its close the
// daemon's tick hit a nil pane and died with every session in it.
func TestStartOnceAtATime(t *testing.T) {
	r := NewRegistry()
	defer r.CloseAll()
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = r.Start(StartOptions{ID: "p1", Session: "s", Command: []string{"/bin/sh", "-c", "sleep 30"}, Exec: true, Cols: 80, Rows: 24})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrExists):
			t.Fatalf("start: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d starts of one pane ran, want 1", ok)
	}
	r.Remove("p1")
	if n := len(r.list()); n != 0 {
		t.Fatalf("%d panes listed after the close", n)
	}
	r.Tick(time.Now()) // panicked on the nil pane
}

// Waiting on a pane that closes returns "pane closed": `omaterm wait` on
// it, with no timeout, waited for ever.
func TestWaitEndsWithPane(t *testing.T) {
	r := NewRegistry()
	defer r.CloseAll()
	p, err := r.Start(StartOptions{ID: "p1", Session: "s", Command: []string{"/bin/sh", "-c", "sleep 30"}, Exec: true, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := p.Wait(context.Background(), func(Info) bool { return false })
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	r.Remove("p1")
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("wait ended with %v, want ErrClosed", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the wait goes on after the pane closed")
	}
}

// A script is named by itself, its runtime apart, whatever the runtime's
// case: Homebrew's Python on macOS runs as "Python".
func TestScriptName(t *testing.T) {
	for _, c := range []struct {
		proc          string
		args          []string
		name, runtime string
	}{
		{"node", []string{"node", "/usr/lib/node_modules/codex/bin/codex.js"}, "codex", "node"},
		{"python3", []string{"python3", "manage.py", "runserver"}, "manage", "python"},
		{"Python", []string{"/opt/homebrew/bin/python3", "serve.py"}, "serve", "python"},
		{"Python", []string{"python3"}, "Python", ""},
		{"vim", []string{"vim", "main.go"}, "vim", ""},
	} {
		if name, runtime := scriptName(c.proc, c.args); name != c.name || runtime != c.runtime {
			t.Errorf("%s %q: %q, %q; want %q, %q", c.proc, c.args, name, runtime, c.name, c.runtime)
		}
	}
}
