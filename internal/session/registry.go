package session

import (
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"runtime/debug"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"omaterm/internal/pty"
	"omaterm/internal/shellcmd"
)

// Registry holds the app's panes and runs their tick.
type Registry struct {
	mu     sync.Mutex
	panes  map[string]*Pane
	order  []string
	viewed string // the session the user looks at, "" for none
	// starting holds the ids of panes being started: a second start of
	// one, as a restore racing the app's start, is refused, not run twice.
	starting map[string]bool

	changedCh chan struct{}

	// OnExit is told of a pane whose shell exited; OnNotice of a desktop
	// notification a program sent; OnStatus of each status change, with
	// the pane's Info after it. They run on a goroutine of their own,
	// never with a lock held, and must hand off to the UI thread.
	OnExit   func(id string, code int)
	OnNotice func(id, title, body string)
	OnStatus func(info Info, prev Status)
	// OnOutput is told, on the goroutine reading a pane's program, that
	// output came: a daemon frames its clients' screens then. It must
	// return at once.
	OnOutput func(id string)
}

func (r *Registry) output(id string) {
	if fn := r.OnOutput; fn != nil {
		fn(id)
	}
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{panes: map[string]*Pane{}, starting: map[string]bool{}, changedCh: make(chan struct{}, 1)}
}

// ErrExists is Start's error for an id that is taken.
var ErrExists = errors.New("session: pane exists")

// Start runs a pane.
func (r *Registry) Start(o StartOptions) (*Pane, error) {
	r.mu.Lock()
	if _, ok := r.panes[o.ID]; ok || r.starting[o.ID] {
		r.mu.Unlock()
		return nil, ErrExists
	}
	r.starting[o.ID] = true
	r.mu.Unlock()
	p, err := r.start(o)
	r.mu.Lock()
	delete(r.starting, o.ID)
	if err == nil {
		r.panes[o.ID] = p
		r.order = append(r.order, o.ID)
	}
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	r.changed()
	return p, nil
}

// start runs a pane, with its id reserved.
func (r *Registry) start(o StartOptions) (*Pane, error) {
	if o.Cols <= 0 || o.Rows <= 0 {
		o.Cols, o.Rows = 120, 36
	}
	dir := o.Dir
	if st, err := os.Stat(dir); dir == "" || err != nil || !st.IsDir() {
		dir, _ = os.UserHomeDir()
	}
	p := &Pane{reg: r, changed: make(chan struct{}), rows: o.Rows}
	p.info = Info{ID: o.ID, Session: o.Session, Project: o.Project, Dir: dir, Command: o.Command,
		Status: Idle, Since: time.Now().UnixMilli(), Progress: -1}

	path, args, shell := commandLine(o)
	proc, err := pty.Start(pty.Options{
		Path: path, Args: args, Dir: dir,
		Env:  environment(o.Session, o.ID, shell, o.Env),
		Cols: o.Cols, Rows: o.Rows,
	})
	opts := terminal.Options{
		Font: o.Font, Theme: o.Theme, MinimumContrast: o.MinimumContrast,
		Padding: o.Padding, PaddingColor: terminal.PaddingExtend,
		Cursor: o.Cursor, NoBlink: o.NoBlink, CopyOnSelect: o.CopyOnSelect,
		RightClick: o.RightClick, ClipboardRead: o.ClipboardRead, Scrollback: o.Scrollback,
		OpenLink: o.OpenLink, OnZoom: o.OnZoom,
		OnTitle: p.onTitle, OnNotify: p.onNotify, OnBell: p.onBell,
	}
	if errors.Is(err, pty.ErrUnsupported) {
		// No pseudo-terminal of our own (Windows): the terminal runs the
		// program, and the status comes from its title alone.
		opts.Command, opts.Dir, opts.Env = o.Command, dir, o.Env
		opts.OnExit = func(code int) { r.paneExited(o.ID, code) }
	} else if err != nil {
		return nil, err
	} else {
		p.proc = proc
		p.info.PID = proc.PID()
		if shell != "" {
			p.info.Shell = shellcmd.Name(shell)
		}
		p.awaiting = len(o.Command) > 0 && !o.Exec
		opts.Conn = conn{p}
	}
	t, err := terminal.New(opts)
	if err != nil {
		if proc != nil {
			proc.Kill()
			proc.Close()
		}
		return nil, err
	}
	if len(o.History) > 0 {
		t.Feed(o.History)
	}
	t.Resize(o.Cols, o.Rows)
	p.term = t
	return p, nil
}

func lookPath(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

// Get returns a pane, nil for none.
func (r *Registry) Get(id string) *Pane {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.panes[id]
}

// Each calls fn with every pane, in the order they started.
func (r *Registry) Each(fn func(*Pane)) {
	for _, p := range r.list() {
		fn(p)
	}
}

func (r *Registry) list() []*Pane {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Pane, 0, len(r.order))
	for _, id := range r.order {
		if p := r.panes[id]; p != nil {
			out = append(out, p)
		}
	}
	return out
}

// Len is how many panes run.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.panes)
}

// Infos returns every pane's Info, by id.
func (r *Registry) Infos() map[string]Info {
	out := map[string]Info{}
	for _, p := range r.list() {
		out[p.info.ID] = p.Info()
	}
	return out
}

// Remove closes a pane and forgets it.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	p := r.panes[id]
	delete(r.panes, id)
	for i, o := range r.order {
		if o == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	r.mu.Unlock()
	if p != nil {
		p.Close()
		r.changed()
	}
}

// CloseAll closes every pane.
func (r *Registry) CloseAll() {
	for _, p := range r.list() {
		r.Remove(p.info.ID)
	}
}

// SetViewed tells which session the user looks at ("" for none, as when
// the window is in the background): its panes' changes are seen, and its
// unread marks go.
func (r *Registry) SetViewed(sessionID string) {
	r.mu.Lock()
	r.viewed = sessionID
	r.mu.Unlock()
	if sessionID == "" {
		return
	}
	for _, p := range r.list() {
		p.mu.Lock()
		if p.info.Session == sessionID && p.info.Unread {
			p.info.Unread = false
			p.changedLocked()
		}
		p.mu.Unlock()
	}
}

func (r *Registry) isViewed(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.viewed != "" && r.viewed == sessionID
}

// Changed signals, coalesced, that some pane's Info changed.
func (r *Registry) Changed() <-chan struct{} { return r.changedCh }

func (r *Registry) changed() {
	select {
	case r.changedCh <- struct{}{}:
	default:
	}
}

// Run ticks every pane until ctx ends.
func (r *Registry) Run(ctx context.Context) {
	t := time.NewTicker(400 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			r.Tick(now)
		}
	}
}

// Tick follows every pane's program once: its foreground job, directory
// and screen.
func (r *Registry) Tick(now time.Time) {
	for _, p := range r.list() {
		r.tickOne(p, now)
	}
}

// tickOne ticks a pane. A bug there costs that pane its tick, not the
// process: in the session daemon, that would end every session.
func (r *Registry) tickOne(p *Pane, now time.Time) {
	defer func() {
		if e := recover(); e != nil {
			log.Printf("ticking pane %s panicked: %v\n%s", p.ID(), e, debug.Stack())
		}
	}()
	p.tick(now)
}

func (r *Registry) paneExited(id string, code int) {
	if fn := r.OnExit; fn != nil {
		go fn(id, code)
	}
}

func (r *Registry) notice(id, title, body string) {
	if fn := r.OnNotice; fn != nil {
		go fn(id, title, body)
	}
}

// statusChanged is told of a status change; the pane's lock is held, so
// the hook runs on a goroutine with a copy of its Info.
func (r *Registry) statusChanged(p *Pane, prev, st Status) {
	if fn := r.OnStatus; fn != nil {
		info := p.info
		info.Status = st
		go fn(info, prev)
	}
}
