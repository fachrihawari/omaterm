package procs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeWatch is a folder watch a test drives: the paths it reports are the
// test's, and it ends when the supervisor's context does.
type fakeWatch struct {
	changed chan []string
	mu      sync.Mutex
	ctxs    []context.Context
	err     error
}

func newFakeWatch() *fakeWatch { return &fakeWatch{changed: make(chan []string, 8)} }

func (f *fakeWatch) watch(ctx context.Context, root string, changed func([]string)) error {
	f.mu.Lock()
	f.ctxs = append(f.ctxs, ctx)
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case paths := <-f.changed:
			changed(paths)
		}
	}
}

// lastCtx is the context of the newest watch, which the test ends or looks
// at.
func (f *fakeWatch) lastCtx() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ctxs) == 0 {
		return nil
	}
	return f.ctxs[len(f.ctxs)-1]
}

func (f *fakeWatch) watches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ctxs)
}

// send reports a change, and fails the test if the watch is not there.
func (f *fakeWatch) send(t *testing.T, paths ...string) {
	t.Helper()
	select {
	case f.changed <- paths:
	case <-time.After(time.Second):
		t.Fatal("no watch to report a change to")
	}
}

func watchTestSupervisor(t *testing.T) (*Supervisor, *fakeRunner, *fakeWatch, string) {
	t.Helper()
	oldAlive, oldDebounce := aliveDelay, watchDebounce
	aliveDelay, watchDebounce = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { aliveDelay, watchDebounce = oldAlive, oldDebounce })
	dir := t.TempDir()
	def := Def{Name: "web", Command: "echo hi", Watch: []string{"src/**"}}
	r := newFakeRunner()
	s := New(r, trustForTest(t, dir, def))
	t.Cleanup(s.Close)
	s.Shell = "/bin/sh"
	// src/** crosses segments, as the daemon's service.MatchGlob does.
	s.Match = matchSegs
	w := newFakeWatch()
	s.Watch = w.watch
	s.Load("p", dir, []Def{def})
	if err := s.Start("p", "web"); err != nil {
		t.Fatal(err)
	}
	waitProc(t, s, "p", "web", Running)
	return s, r, w, dir
}

// matchSegs is service.MatchGlob, near enough for these tests.
func matchSegs(pattern, path string) bool {
	if pattern == "src/**" {
		return len(path) > 4 && path[:4] == "src/"
	}
	return pattern == path
}

func TestWatchTakesChangesAndIgnoresTheRest(t *testing.T) {
	_, r, w, _ := watchTestSupervisor(t)
	// Something that is not a file the globs name restarts nothing.
	w.send(t, "README.md")
	time.Sleep(150 * time.Millisecond)
	if got := r.count(); got != 1 {
		t.Fatalf("a change outside the globs restarted the process: %d starts", got)
	}
}

func TestWatchRestartsOnAChange(t *testing.T) {
	s, r, w, _ := watchTestSupervisor(t)
	w.send(t, "src/a.go")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r.count() > 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := r.count(); got != 2 {
		t.Fatalf("a change did not restart the process: %d starts", got)
	}
	p := waitProc(t, s, "p", "web", Running)
	if p.LastChange != "src/a.go" {
		t.Fatalf("last change: %q", p.LastChange)
	}
	// It was asked for, so it counts as no restart at all.
	if p.Restarts != 0 {
		t.Fatalf("a watch restart counted as a restart: %d", p.Restarts)
	}
	// The new run says in its scrollback why it started.
	_, o := r.latest()
	if history := string(o.History); !contains(history, "── restarted (changed: src/a.go) ──") {
		t.Fatalf("no banner in the new run's history: %q", history)
	}
	if !contains(string(o.History), "old scrollback") {
		t.Fatalf("the old scrollback is gone: %q", o.History)
	}
}

func TestWatchRestartsOncePerBurst(t *testing.T) {
	_, r, w, _ := watchTestSupervisor(t)
	w.send(t, "src/a.go")
	w.send(t, "src/b.go")
	// Two changes within the debounce are one restart: the first run, and
	// the one the burst started.
	time.Sleep(400 * time.Millisecond)
	if got := r.count(); got != 2 {
		t.Fatalf("a burst restarted the process %d times, want 1", got-1)
	}
}

func TestStopEndsTheWatch(t *testing.T) {
	s, _, w, _ := watchTestSupervisor(t)
	ctx := w.lastCtx()
	if ctx == nil {
		t.Fatal("the process was not watched")
	}
	if err := s.Stop("p", "web"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(5 * time.Millisecond)
	}
	if ctx.Err() == nil {
		t.Fatal("the watch outlived the process")
	}
}

func TestWatchFailureFallsBackToPolling(t *testing.T) {
	oldAlive, oldPoll := aliveDelay, pollPeriod
	aliveDelay, pollPeriod = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { aliveDelay, pollPeriod = oldAlive, oldPoll })
	dir := t.TempDir()
	def := Def{Name: "web", Command: "echo hi", Watch: []string{"src/**"}}
	r := newFakeRunner()
	s := New(r, trustForTest(t, dir, def))
	t.Cleanup(s.Close)
	s.Shell = "/bin/sh"
	s.Match = matchSegs
	w := newFakeWatch()
	w.err = errNoWatch // no folder watch on this system
	s.Watch = w.watch
	s.Load("p", dir, []Def{def})
	if err := s.Start("p", "web"); err != nil {
		t.Fatal(err)
	}
	waitProc(t, s, "p", "web", Running)

	// The run says it cannot be watched, and keeps running: it is not a
	// failure of the process.
	until(t, "the watch failure on the process", func() bool {
		for _, p := range s.List("p") {
			return contains(p.Message, "watch failed: no watch on this system")
		}
		return false
	})
	if got := r.count(); got != 1 {
		t.Fatalf("a watch failure ended the process: %d starts", got)
	}

	// The poll stands in for the watch: a file the globs name, written
	// after the poll's first look, restarts it.
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "src", "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	until(t, "the restart the poll found", func() bool { return r.count() > 1 })
	p := waitProc(t, s, "p", "web", Running)
	if p.LastChange != "src/a.go" {
		t.Fatalf("last change: %q", p.LastChange)
	}
}

// until waits for ok, which the test's process reaches on its own.
func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waited for %s", what)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var errNoWatch = errorString("no watch on this system")

type errorString string

func (e errorString) Error() string { return string(e) }
