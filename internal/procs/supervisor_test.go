package procs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"omaterm/internal/agent"
	"omaterm/internal/session"
)

type fakeRunner struct {
	mu      sync.Mutex
	starts  []session.StartOptions
	handles map[string]*fakeHandle
}

func newFakeRunner() *fakeRunner { return &fakeRunner{handles: map[string]*fakeHandle{}} }
func (r *fakeRunner) Start(o session.StartOptions) (session.Handle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := &fakeHandle{id: o.ID, info: session.Info{ID: o.ID, Status: session.Working, PID: 123}, exit: make(chan session.Info, 1), history: []byte("old scrollback")}
	r.starts = append(r.starts, o)
	r.handles[o.ID] = h
	return h, nil
}
func (r *fakeRunner) Get(id string) session.Handle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handles[id]
}
func (r *fakeRunner) Remove(id string) { r.mu.Lock(); delete(r.handles, id); r.mu.Unlock() }
func (r *fakeRunner) latest() (*fakeHandle, session.StartOptions) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.handles[r.starts[len(r.starts)-1].ID], r.starts[len(r.starts)-1]
}
func (r *fakeRunner) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.starts) }

type fakeHandle struct {
	mu      sync.Mutex
	id      string
	info    session.Info
	exit    chan session.Info
	history []byte
}

func (h *fakeHandle) ID() string                               { return h.id }
func (h *fakeHandle) Info() session.Info                       { h.mu.Lock(); defer h.mu.Unlock(); return h.info }
func (h *fakeHandle) Changed() <-chan struct{}                 { return make(chan struct{}) }
func (h *fakeHandle) Term() *terminal.Terminal                 { return nil }
func (h *fakeHandle) TermIfOpen() *terminal.Terminal           { return nil }
func (h *fakeHandle) Write([]byte) error                       { return nil }
func (h *fakeHandle) Send(context.Context, string, bool) error { return nil }
func (h *fakeHandle) Text(bool, int) string                    { return "" }
func (h *fakeHandle) History(bool) []byte                      { return append([]byte(nil), h.history...) }
func (h *fakeHandle) Wait(ctx context.Context, _ func(session.Info) bool) (session.Info, error) {
	select {
	case i := <-h.exit:
		h.mu.Lock()
		h.info = i
		h.mu.Unlock()
		return i, nil
	case <-ctx.Done():
		return session.Info{}, ctx.Err()
	}
}
func (h *fakeHandle) Flag(string, string)            {}
func (h *fakeHandle) Report(string) error            { return nil }
func (h *fakeHandle) SetAgentSession(*agent.Session) {}
func (h *fakeHandle) SetTurn(*session.Turn)          {}
func (h *fakeHandle) finish(code int) {
	h.exit <- session.Info{ID: h.id, Status: session.Exited, ExitCode: &code}
}

func trustForTest(t *testing.T, dir string, defs ...Def) *Trust {
	t.Helper()
	trust := OpenTrust(filepath.Join(t.TempDir(), "trust.json"))
	for _, d := range defs {
		if err := trust.Add(dir, d.Variant()); err != nil {
			t.Fatal(err)
		}
	}
	return trust
}
func waitProc(t *testing.T, s *Supervisor, project, name string, status Status) Proc {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range s.List(project) {
			if p.Name == name && p.Status == status {
				return p
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("process %s did not reach %s: %+v", name, status, s.List(project))
	return Proc{}
}

func TestSupervisorTrustStartAndExit(t *testing.T) {
	oldAlive := aliveDelay
	aliveDelay = 20 * time.Millisecond
	defer func() { aliveDelay = oldAlive }()
	dir := t.TempDir()
	def := Def{Name: "web", Command: "echo hi", Dir: "web", Env: map[string]string{"Z": "last", "A": "first"}}
	r := newFakeRunner()
	s := New(r, trustForTest(t, dir))
	defer s.Close()
	s.Shell = "/bin/sh"
	s.Load("p", dir, []Def{def})
	if err := s.Start("p", "web"); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("untrusted start: %v", err)
	}
	if err := s.Trust("p", "web"); err != nil {
		t.Fatal(err)
	}
	if err := s.Start("p", "web"); err != nil {
		t.Fatal(err)
	}
	h, o := r.latest()
	if o.Role != "process" || !o.Exec || strings.Join(o.Command, " ") != "/bin/sh -l -c echo hi" || o.Dir != filepath.Join(dir, "web") || strings.Join(o.Env, ",") != "A=first,Z=last" {
		t.Fatalf("start options: %+v", o)
	}
	waitProc(t, s, "p", "web", Running)
	h.finish(0)
	waitProc(t, s, "p", "web", Exited)
}

func TestSupervisorRestartBackoffHistoryAndPause(t *testing.T) {
	oldAlive, oldBase := aliveDelay, restartBase
	aliveDelay = 10 * time.Millisecond
	restartBase = 10 * time.Millisecond
	defer func() { aliveDelay = oldAlive; restartBase = oldBase }()
	dir := t.TempDir()
	def := Def{Name: "worker", Command: "run", Restart: "on-failure"}
	r := newFakeRunner()
	s := New(r, trustForTest(t, dir, def))
	defer s.Close()
	s.MaxRestarts = 2
	s.Load("p", dir, []Def{def})
	if err := s.Start("p", "worker"); err != nil {
		t.Fatal(err)
	}
	h, _ := r.latest()
	h.finish(1)
	deadline := time.Now().Add(time.Second)
	for r.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r.count() != 2 {
		t.Fatalf("first restart was not started: %d", r.count())
	}
	_, o := r.latest()
	if !strings.Contains(string(o.History), "old scrollback") || !strings.Contains(string(o.History), "restarted (exit 1)") {
		t.Fatalf("restart history: %q", o.History)
	}
	h, _ = r.latest()
	h.finish(1)
	p := waitProc(t, s, "p", "worker", Paused)
	if p.Restarts != 2 || !strings.Contains(p.Message, "paused") {
		t.Fatalf("paused state: %+v", p)
	}
}

func TestSupervisorStopStickyLoadAndAutostart(t *testing.T) {
	oldAlive := aliveDelay
	aliveDelay = 10 * time.Millisecond
	defer func() { aliveDelay = oldAlive }()
	dir := t.TempDir()
	auto := Def{Name: "auto", Command: "run", Autostart: true}
	r := newFakeRunner()
	s := New(r, trustForTest(t, dir, auto))
	defer s.Close()
	s.Load("p", dir, []Def{auto})
	if r.count() != 1 {
		t.Fatalf("first load did not autostart: %d", r.count())
	}
	s.Load("p", dir, []Def{auto})
	if r.count() != 1 {
		t.Fatalf("second load autostarted again: %d", r.count())
	}
	stopped, _ := r.latest()
	if err := s.Stop("p", "auto"); err != nil {
		t.Fatal(err)
	}
	stopped.finish(1)
	time.Sleep(30 * time.Millisecond)
	if r.count() != 1 {
		t.Fatalf("stopped process restarted: %d", r.count())
	}
	if err := s.Start("p", "auto"); err != nil {
		t.Fatal(err)
	}
	changed := auto
	changed.Command = "different"
	s.Load("p", dir, []Def{changed})
	if !s.List("p")[0].Changed {
		t.Fatalf("definition change not recorded: %+v", s.List("p"))
	}

	// A process removed from the definitions remains visible while its pane runs.
	s.Load("p", dir, []Def{auto})
	if err := s.Start("p", "auto"); err != nil {
		t.Fatal(err)
	}
	s.Load("p", dir, nil)
	list := s.List("p")
	if len(list) != 1 || !list[0].Changed || list[0].Message != "no longer defined" {
		t.Fatalf("removed running process: %+v", list)
	}
}

func TestSupervisorRealRestartsPause(t *testing.T) {
	oldBase := restartBase
	restartBase = 20 * time.Millisecond
	defer func() { restartBase = oldBase }()
	h := session.NewLocalHost()
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	defer func() { cancel(); h.CloseAll() }()
	dir := t.TempDir()
	def := Def{Name: "bad", Command: "exit 1", Restart: "on-failure"}
	s := New(h, trustForTest(t, dir, def))
	defer s.Close()
	s.MaxRestarts = 2
	s.Load("p", dir, []Def{def})
	if err := s.Start("p", "bad"); err != nil {
		t.Fatal(err)
	}
	waitProc(t, s, "p", "bad", Paused)
}

func TestSupervisorTrustUsesDefinitionVariant(t *testing.T) {
	dir := t.TempDir()
	def := Def{Name: "x", Command: "true"}
	trust := trustForTest(t, dir, def)
	s := New(newFakeRunner(), trust)
	defer s.Close()
	s.Load("p", dir, []Def{def})
	if !s.List("p")[0].Trusted {
		t.Fatal("trusted definition not recognized")
	}
	if _, err := os.Stat(trust.path); err != nil {
		t.Fatal(err)
	}
}

// A definition that changes while its process runs is the one the process
// has from then on: Restart runs it, and an untrusted one is refused with
// the old run left alone.
func TestLoadUpdatesARunningDefinition(t *testing.T) {
	oldAlive := aliveDelay
	aliveDelay = 10 * time.Millisecond
	defer func() { aliveDelay = oldAlive }()
	dir := t.TempDir()
	def := Def{Name: "web", Command: "run v1"}
	changed := Def{Name: "web", Command: "run v2"}
	r := newFakeRunner()
	trust := trustForTest(t, dir, def, changed)
	s := New(r, trust)
	defer s.Close()
	s.Shell = "/bin/sh"
	s.Load("p", dir, []Def{def})
	if err := s.Start("p", "web"); err != nil {
		t.Fatal(err)
	}
	waitProc(t, s, "p", "web", Running)

	s.Load("p", dir, []Def{changed})
	p := s.List("p")[0]
	if !p.Changed || p.Command != "run v2" || p.Status != Running {
		t.Fatalf("after the change: %+v", p)
	}
	if err := s.Restart("p", "web"); err != nil {
		t.Fatal(err)
	}
	p = waitProc(t, s, "p", "web", Running)
	_, o := r.latest()
	if !strings.Contains(strings.Join(o.Command, " "), "run v2") || p.Changed {
		t.Fatalf("the restart ran %v, changed %v", o.Command, p.Changed)
	}

	// A third version nobody trusted: the process keeps running the
	// second, and a restart is refused rather than stopping it.
	s.Load("p", dir, []Def{{Name: "web", Command: "run v3"}})
	p = s.List("p")[0]
	if p.Trusted || !p.Changed || p.Status != Running {
		t.Fatalf("after the untrusted change: %+v", p)
	}
	if err := s.Restart("p", "web"); !errors.Is(err, ErrUntrusted) {
		t.Fatalf("restart of an untrusted definition: %v", err)
	}
	if p := s.List("p")[0]; p.Status != Running || r.count() != 2 {
		t.Fatalf("the refused restart changed the run: %+v, %d starts", p, r.count())
	}
}

// A crash restart runs the definition the process has then, and waits
// for the user when that one isn't trusted.
func TestCrashRestartWaitsForTrust(t *testing.T) {
	oldAlive, oldBase := aliveDelay, restartBase
	aliveDelay, restartBase = 10*time.Millisecond, 10*time.Millisecond
	defer func() { aliveDelay, restartBase = oldAlive, oldBase }()
	dir := t.TempDir()
	def := Def{Name: "worker", Command: "run", Restart: "always"}
	r := newFakeRunner()
	s := New(r, trustForTest(t, dir, def))
	defer s.Close()
	s.Load("p", dir, []Def{def})
	if err := s.Start("p", "worker"); err != nil {
		t.Fatal(err)
	}
	waitProc(t, s, "p", "worker", Running)
	s.Load("p", dir, []Def{{Name: "worker", Command: "run v2", Restart: "always"}})
	h, _ := r.latest()
	h.finish(0)
	p := waitProc(t, s, "p", "worker", Untrusted)
	if r.count() != 1 || !strings.Contains(p.Message, "trusted") {
		t.Fatalf("an untrusted definition was restarted: %d starts, %+v", r.count(), p)
	}
}
