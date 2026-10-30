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
	Since    int64  `json:"since"`
	Trusted  bool   `json:"trusted"`
	Changed  bool   `json:"changed,omitempty"`
	Message  string `json:"message,omitempty"`
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

	mu       sync.Mutex
	runner   Runner
	trust    *Trust
	projects map[string]*projectState
	closed   bool
	ctx      context.Context
	cancel   context.CancelFunc
}

type projectState struct {
	dir    string
	order  []string
	procs  map[string]*procState
	loaded bool
}

type procState struct {
	proc       Proc
	def        Def
	dir        string
	handle     session.Handle
	generation uint64
	stop       context.CancelFunc
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
			old.proc.Changed = old.def.Variant() != d.Variant()
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
	return s.startRun(projectID, name, def, dir, gen, nil, base)
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
	p.stop = nil
	p.handle = nil
	p.proc.Status = Stopped
	p.proc.Message = ""
	p.proc.Since = time.Now().UnixMilli()
	s.mu.Unlock()
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

// Restart stops and starts a process without making the stop sticky.
func (s *Supervisor) Restart(projectID, name string) error {
	if err := s.Stop(projectID, name); err != nil {
		return err
	}
	s.mu.Lock()
	_, p := s.find(projectID, name)
	if p != nil {
		p.stopped = false
	}
	s.mu.Unlock()
	return s.Start(projectID, name)
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

// Close stops watching process exits without ending their panes.
func (s *Supervisor) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
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
	s.mu.Unlock()
	s.changed()
	s.emit(Event{Project: project, Name: name, Kind: EventStarted})
	go s.watch(project, name, def, root, gen, h, ctx, base)
	go s.ready(project, name, def.Port, gen, h, ctx, aliveDelay, checkPeriod)
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
	p.handle = nil
	p.stop = nil
	p.proc.LastExit = code
	p.proc.PID = 0
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
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	s.mu.Lock()
	_, p = s.find(project, name)
	if p == nil || p.generation != gen || p.stopped || s.closed {
		s.mu.Unlock()
		return
	}
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
