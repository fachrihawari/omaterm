package procs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"omaterm/internal/session"
	"omaterm/internal/shellcmd"
)

// Runner starts and ends the panes processes run in: the daemon's
// registry, or the app's.
type Runner interface {
	Start(o session.StartOptions) (session.Handle, error)
	Get(id string) session.Handle
	Remove(id string)
}

// Status is a process's state in the supervisor.
type Status string

const (
	Stopped   Status = "stopped"
	Starting  Status = "starting"
	Running   Status = "running"
	Exited    Status = "exited"
	Failed    Status = "failed"
	Paused    Status = "paused"
	Untrusted Status = "untrusted"
)

// Proc is a process as the UI and CLI see it.
type Proc struct {
	Def
	Status   Status `json:"status"`
	Pane     string `json:"pane,omitempty"`
	Session  string `json:"session"`
	PID      int    `json:"pid,omitempty"`
	Restarts int    `json:"restarts,omitempty"`
	LastExit int    `json:"lastExit,omitempty"`
	// LastChange is the file whose change last restarted the process.
	LastChange string `json:"lastChange,omitempty"`
	Since      int64  `json:"since"`
	Trusted    bool   `json:"trusted"`
	Changed    bool   `json:"changed,omitempty"`
	Message    string `json:"message,omitempty"`
	// Link is the address its Dev URL serves it at, https://shop.localhost,
	// while the proxy routes it; "" otherwise.
	Link string `json:"link,omitempty"`
	// Share is the address it is shared at, and ShareVia the tunnel
	// ("tailscale" or "cloudflare"), while it is shared.
	Share    string `json:"share,omitempty"`
	ShareVia string `json:"shareVia,omitempty"`
}

// EventKind is a process lifecycle event.
type EventKind string

const (
	EventStarted EventKind = "started"
	EventStopped EventKind = "stopped"
	EventFailed  EventKind = "failed"
	EventPaused  EventKind = "paused"
)

// Event reports a process lifecycle event.
type Event struct {
	Project, Name string
	Kind          EventKind
	Code          int
}

// Supervisor starts project processes and restarts them according to their definitions.
type Supervisor struct {
	OnEvent       func(Event)
	OnChange      func()
	Shell         string
	MaxRestarts   int
	RestartWindow time.Duration
	// Watch watches a project's folder for changes, when a definition asks
	// for it: the daemon sets it to mygo's, and a supervisor with none
	// watches nothing. Match tests a changed path against a definition's
	// globs, and Ignore gives, for a project's folder, what a poll of it
	// should skip; together they keep this package free of the app's and
	// the terminal's.
	Watch  WatchFunc
	Match  func(pattern, path string) bool
	Ignore func(root string) func(path string, isDir bool) bool

	mu       sync.Mutex
	runner   Runner
	trust    *Trust
	projects map[string]*projectState
	closed   bool
	// watches counts the goroutines of the runs and their folder watches,
	// which Close waits for.
	watches sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
}

type projectState struct {
	dir    string
	order  []string
	procs  map[string]*procState
	loaded bool
}

type procState struct {
	proc   Proc
	def    Def
	dir    string
	handle session.Handle
	// running is the variant of the definition the current run started
	// with: a Load that brings another marks the process Changed.
	running    string
	generation uint64
	stop       context.CancelFunc
	watch      context.CancelFunc
	stopped    bool
	restarts   []time.Time
}

var (
	ErrUntrusted = errors.New("isn't trusted yet: review it in omaterm")
	ErrNotFound  = errors.New("process not found")
	aliveDelay   = 2 * time.Second
	restartBase  = time.Second
	checkPeriod  = 250 * time.Millisecond
)

// New creates a supervisor for panes started by r and trust recorded in t.
func New(r Runner, t *Trust) *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{runner: r, trust: t, projects: map[string]*projectState{}, ctx: ctx, cancel: cancel}
}

// SessionID returns the stable session id for a project process.
func SessionID(projectID, name string) string { return "proc-" + projectID + "-" + name }

// Load sets a project's merged process definitions.
func (s *Supervisor) Load(projectID, dir string, defs []Def) {
	s.mu.Lock()
	ps := s.project(projectID)
	ps.dir = dir
	first := !ps.loaded
	ps.loaded = true
	order := make([]string, 0, len(defs))
	present := map[string]bool{}
	var autostart []string
	for _, d := range defs {
		present[d.Name] = true
		order = append(order, d.Name)
		trusted := s.trust != nil && s.trust.Trusted(dir, d.Variant())
		old := ps.procs[d.Name]
		if old == nil {
			status := Stopped
			if !trusted {
				status = Untrusted
			}
			old = &procState{proc: Proc{Def: d, Status: status, Session: SessionID(projectID, d.Name), Since: time.Now().UnixMilli(), Trusted: trusted}, def: d, dir: dir}
			ps.procs[d.Name] = old
		} else if old.handle != nil {
			// The run goes on with what it started with; the definition
			// the process has, restarts with and is trusted by is this one.
			old.def, old.dir, old.proc.Def, old.proc.Trusted = d, dir, d, trusted
			old.proc.Changed = old.running != d.Variant()
			if old.proc.Changed {
				old.proc.Message = "definition changed; restart to apply"
			} else {
				old.proc.Message = ""
			}
		} else {
			old.def, old.dir, old.proc.Def, old.proc.Trusted = d, dir, d, trusted
			old.proc.Changed = false
			old.proc.Message = ""
			if !trusted {
				old.proc.Status = Untrusted
			} else if old.proc.Status == Untrusted {
				old.proc.Status = Stopped
			}
		}
		if first && trusted && d.Autostart {
			autostart = append(autostart, d.Name)
		}
	}
	for name, p := range ps.procs {
		if present[name] {
			continue
		}
		if p.handle == nil {
			delete(ps.procs, name)
			continue
		}
		p.proc.Changed = true
		p.proc.Message = "no longer defined"
	}
	for _, name := range ps.order {
		if !present[name] && ps.procs[name] != nil && ps.procs[name].handle != nil {
			order = append(order, name)
		}
	}
	ps.order = order
	s.mu.Unlock()
	s.changed()
	for _, name := range autostart {
		_ = s.Start(projectID, name)
	}
}

// List returns a project's processes in definition order.
func (s *Supervisor) List(projectID string) []Proc {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := s.projects[projectID]
	if ps == nil {
		return nil
	}
	out := make([]Proc, 0, len(ps.order))
	for _, name := range ps.order {
		if p := ps.procs[name]; p != nil {
			out = append(out, p.proc)
		}
	}
	return out
}

// Sessions returns processes that have or had a pane.
func (s *Supervisor) Sessions(projectID string) []Proc {
	list := s.List(projectID)
	out := list[:0]
	for _, p := range list {
		if p.Pane != "" {
			out = append(out, p)
		}
	}
	return out
}

// Start starts a process, or resumes one paused after repeated restarts.
func (s *Supervisor) Start(projectID, name string) error {
	return s.start(projectID, name, nil)
}

// start is Start, with what the run before it printed to show above this
// one, as a restart does.
func (s *Supervisor) start(projectID, name string, history []byte) error {
	s.mu.Lock()
	_, p := s.find(projectID, name)
	if p == nil {
		s.mu.Unlock()
		return fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	if !p.proc.Trusted {
		s.mu.Unlock()
		return fmt.Errorf("%s %w", name, ErrUntrusted)
	}
	if p.handle != nil && p.proc.Status != Exited && p.proc.Status != Failed && p.proc.Status != Paused {
		s.mu.Unlock()
		return nil
	}
	p.stopped = false
	p.restarts = nil
	p.proc.Restarts = 0
	p.proc.Status = Starting
	p.proc.Message = ""
	p.proc.Since = time.Now().UnixMilli()
	gen := p.generation + 1
	p.generation = gen
	def, dir := p.def, p.dir
	s.mu.Unlock()
	s.changed()
	base := restartBase
	if base <= 0 {
		base = time.Second
	}
	return s.startRun(projectID, name, def, dir, gen, history, base)
}

// Stop ends a process and prevents it from restarting automatically.
func (s *Supervisor) Stop(projectID, name string) error {
	s.mu.Lock()
	_, p := s.find(projectID, name)
	if p == nil {
		s.mu.Unlock()
		return fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	p.stopped = true
	h, cancel := p.handle, p.stop
	w := p.watch
	p.stop, p.watch = nil, nil
	p.handle = nil
	// Nothing runs: no pid, and no run for the definition to differ
	// from. A definition the user hasn't trusted waits for them.
	p.proc.Status, p.proc.PID, p.proc.Changed = Stopped, 0, false
	if !p.proc.Trusted {
		p.proc.Status = Untrusted
	}
	p.proc.Message = ""
	p.proc.Since = time.Now().UnixMilli()
	s.mu.Unlock()
	if w != nil {
		w()
	}
	if cancel != nil {
		cancel()
	}
	if h != nil {
		s.runner.Remove(h.ID())
	}
	s.emit(Event{Project: projectID, Name: name, Kind: EventStopped})
	s.changed()
	return nil
}

// Restart stops and starts a process without making the stop sticky. The
// new run shows what the old one printed, under a line that says it was
// replaced: a restart is how a user reads the run before it. A definition
// that isn't trusted is refused before anything is stopped.
func (s *Supervisor) Restart(projectID, name string) error {
	var history []byte
	s.mu.Lock()
	_, p := s.find(projectID, name)
	if p == nil {
		s.mu.Unlock()
		return fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	if !p.proc.Trusted {
		s.mu.Unlock()
		return fmt.Errorf("%s %w", name, ErrUntrusted)
	}
	if p.handle != nil {
		history = p.handle.History(true)
		history = append(history, []byte("\r\n\x1b[2m── restarted ──\x1b[0m\r\n")...)
	}
	s.mu.Unlock()
	if err := s.Stop(projectID, name); err != nil {
		return err
	}
	s.mu.Lock()
	_, p = s.find(projectID, name)
	if p != nil {
		p.stopped = false
	}
	s.mu.Unlock()
	return s.start(projectID, name, history)
}

// Trust records the current definition's variant as trusted.
func (s *Supervisor) Trust(projectID, name string) error {
	s.mu.Lock()
	ps, p := s.find(projectID, name)
	if p == nil {
		s.mu.Unlock()
		return fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	dir, variant := ps.dir, p.def.Variant()
	s.mu.Unlock()
	if s.trust != nil {
		if err := s.trust.Add(dir, variant); err != nil {
			return err
		}
	}
	s.mu.Lock()
	_, p = s.find(projectID, name)
	if p != nil {
		p.proc.Trusted = true
		if p.proc.Status == Untrusted {
			p.proc.Status = Stopped
		}
		p.proc.Since = time.Now().UnixMilli()
	}
	s.mu.Unlock()
	s.changed()
	return nil
}

// Close stops watching process exits without ending their panes. It waits
// for the runs' goroutines and the folder watches: nothing of the
// supervisor runs after it returns.
func (s *Supervisor) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.watches.Wait()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.watches.Wait()
}

func (s *Supervisor) project(id string) *projectState {
	p := s.projects[id]
	if p == nil {
		p = &projectState{procs: map[string]*procState{}}
		s.projects[id] = p
	}
	return p
}

func (s *Supervisor) find(projectID, name string) (*projectState, *procState) {
	ps := s.projects[projectID]
	if ps == nil {
		return nil, nil
	}
	return ps, ps.procs[name]
}

func (s *Supervisor) startRun(project, name string, def Def, root string, gen uint64, history []byte, base time.Duration) error {
	shell := s.Shell
	if shell == "" {
		shell = shellcmd.Login()
	}
	id := paneID(name)
	env := make([]string, 0, len(def.Env))
	keys := make([]string, 0, len(def.Env))
	for k := range def.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+def.Env[k])
	}
	dir := root
	if def.Dir != "" {
		dir = filepath.Join(root, def.Dir)
	}
	o := session.StartOptions{ID: id, Session: SessionID(project, name), Role: "process", Dir: dir, Env: env,
		Exec: true, Command: shellcmd.LoginArgs(shell, def.Command), History: history, Cols: 120, Rows: 36}
	h, err := s.runner.Start(o)
	if err != nil {
		s.setState(project, name, gen, Failed, "start failed: "+err.Error(), 0)
		s.emit(Event{Project: project, Name: name, Kind: EventFailed})
		return err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	_, p := s.find(project, name)
	if p == nil || p.generation != gen || p.stopped || s.closed {
		s.mu.Unlock()
		cancel()
		s.runner.Remove(h.ID())
		return nil
	}
	p.handle, p.stop, p.proc.Pane, p.proc.PID = h, cancel, h.ID(), h.Info().PID
	p.proc.Status, p.proc.Since, p.proc.Message = Starting, time.Now().UnixMilli(), ""
	p.running, p.proc.Changed = def.Variant(), false
	// Counted under the lock Close sets closed in, so Close waits for the
	// run's goroutines, or they never start.
	s.watches.Add(2)
	s.mu.Unlock()
	s.changed()
	s.emit(Event{Project: project, Name: name, Kind: EventStarted})
	go func() {
		defer s.watches.Done()
		s.watch(project, name, def, root, gen, h, ctx, base)
	}()
	go func() {
		defer s.watches.Done()
		s.ready(project, name, def.Port, gen, h, ctx, aliveDelay, checkPeriod)
	}()
	s.watching(project, name, def, root, gen)
	return nil
}

func (s *Supervisor) watch(project, name string, def Def, root string, gen uint64, h session.Handle, ctx context.Context, base time.Duration) {
	i, err := h.Wait(ctx, func(i session.Info) bool { return i.Status == session.Exited })
	if err != nil || ctx.Err() != nil {
		return
	}
	code := 0
	if i.ExitCode != nil {
		code = *i.ExitCode
	}
	history := append([]byte(nil), h.History(true)...)
	history = append(history, []byte("\r\n\x1b[2m── restarted (exit "+strconv.Itoa(code)+") ──\x1b[0m\r\n")...)
	s.mu.Lock()
	_, p := s.find(project, name)
	if p == nil || p.generation != gen || p.stopped {
		s.mu.Unlock()
		return
	}
	// The run's context ends with it: its readiness check stops. The
	// wait for a restart is on the supervisor's own context below.
	runCancel := p.stop
	p.handle = nil
	p.stop = nil
	p.proc.LastExit = code
	p.proc.PID = 0
	p.proc.Changed = false
	if runCancel != nil {
		defer runCancel()
	}
	restart := def.Restart == "always" || (def.Restart == "on-failure" && code != 0)
	if !restart {
		if code == 0 {
			p.proc.Status, p.proc.Message = Exited, ""
		} else {
			p.proc.Status, p.proc.Message = Failed, fmt.Sprintf("exited (code %d)", code)
		}
		p.proc.Since = time.Now().UnixMilli()
		s.mu.Unlock()
		s.runner.Remove(h.ID())
		s.changed()
		if code != 0 {
			s.emit(Event{Project: project, Name: name, Kind: EventFailed, Code: code})
		}
		return
	}
	now := time.Now()
	window := s.RestartWindow
	if window <= 0 {
		window = time.Minute
	}
	kept := p.restarts[:0]
	for _, at := range p.restarts {
		if now.Sub(at) <= window {
			kept = append(kept, at)
		}
	}
	p.restarts = append(kept, now)
	max := s.MaxRestarts
	if max <= 0 {
		max = 10
	}
	p.proc.Restarts = len(p.restarts)
	if len(p.restarts) >= max {
		p.proc.Status, p.proc.Since = Paused, now.UnixMilli()
		p.proc.Message = fmt.Sprintf("restart paused after %d restarts in a minute", max)
		s.mu.Unlock()
		s.runner.Remove(h.ID())
		s.changed()
		s.emit(Event{Project: project, Name: name, Kind: EventPaused, Code: code})
		return
	}
	p.proc.Status, p.proc.Since = Starting, now.UnixMilli()
	delay := base * time.Duration(1<<min(len(p.restarts)-1, 5))
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	s.mu.Unlock()
	s.runner.Remove(h.ID())
	s.changed()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return
	case <-timer.C:
	}
	s.mu.Lock()
	_, p = s.find(project, name)
	if p == nil || p.generation != gen || p.stopped || s.closed {
		s.mu.Unlock()
		return
	}
	// The restart runs the definition the process has now, which a Load
	// may have changed since: one the user hasn't trusted waits for them.
	if !p.proc.Trusted {
		p.proc.Status, p.proc.Since = Untrusted, time.Now().UnixMilli()
		p.proc.Message = "the changed definition isn't trusted yet"
		s.mu.Unlock()
		s.changed()
		return
	}
	def, root = p.def, p.dir
	p.generation++
	newGen := p.generation
	s.mu.Unlock()
	_ = s.startRun(project, name, def, root, newGen, history, base)
}

func (s *Supervisor) ready(project, name string, port int, gen uint64, h session.Handle, ctx context.Context, alive, period time.Duration) {
	tick := time.NewTicker(period)
	defer tick.Stop()
	deadline := time.NewTimer(alive)
	defer deadline.Stop()
	for {
		if port > 0 {
			c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
			if err == nil {
				c.Close()
				s.setRunning(project, name, gen, h)
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			s.setRunning(project, name, gen, h)
			return
		case <-tick.C:
			if h.Info().Status == session.Exited {
				return
			}
		}
	}
}

func (s *Supervisor) setRunning(project, name string, gen uint64, h session.Handle) {
	if h.Info().Status == session.Exited {
		return
	}
	s.mu.Lock()
	_, p := s.find(project, name)
	if p == nil || p.generation != gen || p.handle != h {
		s.mu.Unlock()
		return
	}
	p.proc.Status, p.proc.Since = Running, time.Now().UnixMilli()
	s.mu.Unlock()
	s.changed()
}

func (s *Supervisor) setState(project, name string, gen uint64, status Status, message string, pid int) {
	s.mu.Lock()
	_, p := s.find(project, name)
	if p != nil && p.generation == gen {
		p.proc.Status, p.proc.Message, p.proc.PID = status, message, pid
		p.proc.Since = time.Now().UnixMilli()
	}
	s.mu.Unlock()
	s.changed()
}

func (s *Supervisor) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}
func (s *Supervisor) emit(e Event) {
	if s.OnEvent != nil {
		s.OnEvent(e)
	}
}

func paneID(name string) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "proc-" + name + "-00000000"
	}
	return "proc-" + name + "-" + hex.EncodeToString(b)
}
