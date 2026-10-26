package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"omaterm/internal/agent"
	"omaterm/internal/client"
	"omaterm/internal/paths"
	"omaterm/internal/proto"
	"omaterm/internal/session"
)

// Remote is the app's hold on the daemon's panes: a session.Host whose
// panes the daemon runs, which outlive the app.
type Remote struct {
	socket string
	// quickly is how long a reconnect tries every 100ms (10s when 0).
	quickly time.Duration
	// viewed is the session the user looks at, sentViewed the last the
	// daemon was told, and sendingViewed whether a goroutine is telling it.
	viewed, sentViewed string
	sendingViewed      bool
	// closeLater are panes whose close didn't reach the daemon.
	closeLater []string
	// TermOptions gives what a pane's terminal in the app looks like: its
	// font, theme and padding.
	TermOptions func() session.StartOptions
	// OnLost tells of panes that went with a daemon that stopped: the
	// Remote has connected to a new one, which runs none of them.
	OnLost func(ids []string)
	// Spawn starts a daemon when none answers: StartProcess.
	Spawn func() error
	// OnCall answers a call of the omaterm command the daemon forwarded,
	// as the app's control socket would.
	OnCall func(method string, params json.RawMessage) (any, error)
	// OnConnection tells that the connection to the daemon went
	// (reconnecting) or came back.
	OnConnection func(reconnecting bool)

	mu      sync.Mutex
	c       *client.Client
	closed  bool
	infos   map[string]session.Info
	changed map[string]chan struct{}
	handles map[string]*remotePane
	hooks   session.Hooks
	any     chan struct{}
	version string
}

var _ session.Host = (*Remote)(nil)

// Connect connects to the daemon, starting it when start is set and none
// runs.
func Connect(start bool) (*Remote, error) {
	socket := paths.DaemonSocketPath()
	c, err := client.Dial(socket)
	if errors.Is(err, client.ErrUnreachable) && start {
		if err = StartProcess(); err != nil {
			return nil, err
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if c, err = client.Dial(socket); err == nil {
				break
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return newRemote(c, socket)
}

// Dial connects to a daemon at a socket (tests).
func Dial(socket string) (*Remote, error) {
	c, err := client.Dial(socket)
	if err != nil {
		return nil, err
	}
	return newRemote(c, socket)
}

func newRemote(c *client.Client, socket string) (*Remote, error) {
	r := &Remote{socket: socket, infos: map[string]session.Info{}, changed: map[string]chan struct{}{},
		handles: map[string]*remotePane{}, any: make(chan struct{}, 1), Spawn: func() error { return startProcess(true) }}
	if err := r.use(c); err != nil {
		return nil, err
	}
	return r, nil
}

// use makes c the Remote's connection: it takes the daemon's panes and
// follows them.
func (r *Remote) use(c *client.Client) error {
	var infos map[string]session.Info
	if err := c.Call("panes", nil, &infos); err != nil {
		c.Close()
		return err
	}
	if err := c.Call("subscribe", nil, nil); err != nil {
		c.Close()
		return err
	}
	// New panes see the display and session bus of the app's login
	// session, which may be newer than the daemon's.
	env := map[string]string{}
	for _, k := range SharedEnv {
		if v, ok := os.LookupEnv(k); ok {
			env[k] = v
		}
	}
	c.Call("env.update", env, nil)
	r.mu.Lock()
	r.c, r.version = c, c.Welcome.App
	// A new daemon, or one the app left, knows of no session viewed: the
	// one on screen got unread marks until the user switched away and
	// back.
	r.sentViewed = "\x00"
	viewed := r.viewed
	r.mu.Unlock()
	r.update(infos)
	go r.events(c)
	r.SetViewed(viewed)
	r.mu.Lock()
	pending := len(r.closeLater) > 0
	r.mu.Unlock()
	if pending {
		go r.closePending()
	}
	return nil
}

func (r *Remote) client() *client.Client {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.c
}

// Version is the daemon's build.
func (r *Remote) Version() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.version
}

// Restart stops the daemon, its panes with it, and starts a new one;
// OnLost hears of the panes, to start them again.
func (r *Remote) Restart() error {
	c := r.client()
	c.Call("shutdown", nil, nil)
	c.Close() // events sees the end and connects to a new daemon
	return nil
}

// reconnect connects to the daemon again once the connection ends,
// starting one when none runs. It tries every 100ms at first, then less
// often, until it connects or the Remote closes: it gave up after 10
// seconds, saying it was connected, and left every pane blank.
func (r *Remote) reconnect() {
	if r.OnConnection != nil {
		r.OnConnection(true)
	}
	var lost []string
	r.mu.Lock()
	for id := range r.infos {
		lost = append(lost, id)
	}
	r.mu.Unlock()
	quickly := r.quickly
	if quickly == 0 {
		quickly = 10 * time.Second
	}
	start, wait := time.Now(), 100*time.Millisecond
	for ; ; time.Sleep(wait) {
		if time.Since(start) > quickly {
			wait = min(wait*2, 5*time.Second)
		}
		r.mu.Lock()
		closed := r.closed
		r.mu.Unlock()
		if closed {
			return
		}
		c, err := client.Dial(r.socket)
		if err != nil && r.Spawn != nil {
			r.Spawn()
			for i := 0; i < 50 && err != nil; i++ {
				time.Sleep(100 * time.Millisecond)
				c, err = client.Dial(r.socket)
			}
		}
		if err != nil {
			continue
		}
		r.CloseAll()
		if r.use(c) != nil {
			continue
		}
		infos := r.Infos()
		gone := lost[:0]
		for _, id := range lost {
			if _, ok := infos[id]; !ok {
				gone = append(gone, id)
			}
		}
		if len(gone) > 0 && r.OnLost != nil {
			r.OnLost(gone)
		}
		if r.OnConnection != nil {
			r.OnConnection(false)
		}
		return
	}
}

// StartProcess starts the daemon: its service, when installed, else this
// executable with __daemon, in a session of its own, so it outlives the
// app.
func StartProcess() error {
	return startProcess(false)
}

// startProcess starts the daemon; a takeover waits for the one before to
// let go of the lock.
func startProcess(takeover bool) error {
	if ServiceInstalled() && systemctl("start", ServiceName()) == nil {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	// An updated app's old executable is gone; its link in the bin
	// directory names the new one.
	if _, err := os.Stat(exe); err != nil {
		if real, err := filepath.EvalSymlinks(filepath.Join(paths.BinDir(), "omaterm")); err == nil {
			exe = real
		}
	}
	logPath := filepath.Join(paths.StateDir(), "daemon.log")
	os.MkdirAll(filepath.Dir(logPath), 0o700)
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if st, err := os.Stat(logPath); err == nil && st.Size() > 4<<20 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	logf, err := os.OpenFile(logPath, flags, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "__daemon")
	if takeover {
		cmd.Env = append(os.Environ(), "OMATERM_TAKEOVER=1")
	}
	cmd.Dir = paths.Home()
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func (r *Remote) events(c *client.Client) {
	defer func() {
		r.mu.Lock()
		current := r.c == c && !r.closed
		r.mu.Unlock()
		if current {
			r.reconnect()
		}
	}()
	for m := range c.Events() {
		switch m.Event {
		case "panes":
			var infos map[string]session.Info
			if json.Unmarshal(m.Result, &infos) == nil {
				r.update(infos)
			}
		case "call":
			var fc ForwardedCall
			if json.Unmarshal(m.Result, &fc) == nil {
				go r.answer(c, fc)
			}
		case "notice":
			var n struct{ Pane, Title, Body string }
			r.mu.Lock()
			onNotice := r.hooks.OnNotice
			r.mu.Unlock()
			if json.Unmarshal(m.Result, &n) == nil && onNotice != nil {
				go onNotice(n.Pane, n.Title, n.Body)
			}
		}
	}
}

// update takes the daemon's panes, telling the hooks of what changed.
func (r *Remote) update(infos map[string]session.Info) {
	r.mu.Lock()
	hooks := r.hooks
	type change struct {
		info session.Info
		prev session.Status
	}
	var statuses []change
	var exits []session.Info
	for id, n := range infos {
		old, had := r.infos[id]
		if had && old.Status != n.Status {
			statuses = append(statuses, change{n, old.Status})
			if n.Status == session.Exited {
				exits = append(exits, n)
			}
		}
		if !had || !sameInfo(old, n) {
			if ch := r.changed[id]; ch != nil {
				close(ch)
			}
			r.changed[id] = make(chan struct{})
		}
	}
	for id, ch := range r.changed {
		if _, ok := infos[id]; !ok {
			close(ch)
			delete(r.changed, id)
		}
	}
	r.infos = infos
	r.mu.Unlock()
	select {
	case r.any <- struct{}{}:
	default:
	}
	for _, s := range statuses {
		if hooks.OnStatus != nil {
			go hooks.OnStatus(s.info, s.prev)
		}
	}
	for _, e := range exits {
		code := 0
		if e.ExitCode != nil {
			code = *e.ExitCode
		}
		if hooks.OnExit != nil {
			go hooks.OnExit(e.ID, code)
		}
	}
}

func sameInfo(a, b session.Info) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// Start starts a pane in the daemon.
func (r *Remote) Start(o session.StartOptions) (session.Handle, error) {
	p := StartParams{ID: o.ID, Session: o.Session, Project: o.Project, Dir: o.Dir, Command: o.Command,
		Exec: o.Exec, Env: o.Env, Cols: o.Cols, Rows: o.Rows, History: o.History}
	var info session.Info
	if err := r.call("pane.start", p, &info); err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.infos[o.ID] = info
	if r.changed[o.ID] == nil {
		r.changed[o.ID] = make(chan struct{})
	}
	r.mu.Unlock()
	return r.handle(o.ID), nil
}

func (r *Remote) handle(id string) *remotePane {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.handles[id]
	if h == nil {
		h = &remotePane{r: r, id: id}
		r.handles[id] = h
	}
	return h
}

// Get returns a pane the daemon runs, nil for none.
func (r *Remote) Get(id string) session.Handle {
	r.mu.Lock()
	_, ok := r.infos[id]
	r.mu.Unlock()
	if !ok {
		return nil
	}
	return r.handle(id)
}

func (r *Remote) Each(fn func(session.Handle)) {
	r.mu.Lock()
	ids := make([]string, 0, len(r.infos))
	for id := range r.infos {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	for _, id := range ids {
		fn(r.handle(id))
	}
}

func (r *Remote) Infos() map[string]session.Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]session.Info, len(r.infos))
	for id, i := range r.infos {
		out[id] = i
	}
	return out
}

func (r *Remote) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.infos)
}

// Remove ends a pane in the daemon.
func (r *Remote) Remove(id string) {
	r.mu.Lock()
	h := r.handles[id]
	delete(r.handles, id)
	delete(r.infos, id)
	r.mu.Unlock()
	if h != nil {
		h.detach()
	}
	if r.call("pane.close", proto.IDParams{ID: id}, nil) != nil {
		// The daemon is slow or away: the pane, perhaps an agent still
		// editing the repo, ran on unseen until the next launch. It is
		// closed as the daemon answers again.
		r.mu.Lock()
		r.closeLater = append(r.closeLater, id)
		r.mu.Unlock()
		go r.closePending()
	}
}

// closePending closes the panes whose close didn't get through.
func (r *Remote) closePending() {
	for try := 0; try < 3; try++ {
		r.mu.Lock()
		ids, c := r.closeLater, r.c
		r.closeLater = nil
		r.mu.Unlock()
		var failed []string
		for _, id := range ids {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			if c.CallContext(ctx, "pane.close", proto.IDParams{ID: id}, nil) != nil {
				failed = append(failed, id)
			}
			cancel()
		}
		r.mu.Lock()
		r.closeLater = append(r.closeLater, failed...)
		left := len(r.closeLater)
		r.mu.Unlock()
		if left == 0 {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// CloseAll lets go of the panes: their shells go on in the daemon.
func (r *Remote) CloseAll() {
	r.mu.Lock()
	hs := make([]*remotePane, 0, len(r.handles))
	for _, h := range r.handles {
		hs = append(hs, h)
	}
	r.handles = map[string]*remotePane{}
	r.mu.Unlock()
	for _, h := range hs {
		h.detach()
	}
}

// Close ends the connection to the daemon.
func (r *Remote) Close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.CloseAll()
	r.client().Close()
}

// SetViewed tells the daemon the session the user looks at. One goroutine
// at a time sends the latest, so they arrive in order and none waits on
// a daemon that stopped answering: a goroutine each could deliver an
// older session last, and leaked on a stuck daemon.
func (r *Remote) SetViewed(id string) {
	r.mu.Lock()
	r.viewed = id
	busy := r.sendingViewed
	r.sendingViewed = true
	r.mu.Unlock()
	if !busy {
		go r.sendViewed()
	}
}

func (r *Remote) sendViewed() {
	for {
		r.mu.Lock()
		id, c := r.viewed, r.c
		if id == r.sentViewed || r.closed {
			r.sendingViewed = false
			r.mu.Unlock()
			return
		}
		r.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), uiCallTimeout)
		err := c.CallContext(ctx, "viewed", proto.IDParams{ID: id}, nil)
		cancel()
		r.mu.Lock()
		if err != nil {
			// A reconnect sends it again.
			r.sendingViewed = false
			r.mu.Unlock()
			return
		}
		r.sentViewed = id
		r.mu.Unlock()
	}
}

func (r *Remote) Changed() <-chan struct{} { return r.any }

// Run and Tick do nothing: the daemon follows its panes itself.
func (r *Remote) Run(ctx context.Context) { <-ctx.Done() }
func (r *Remote) Tick(time.Time)          {}

// SetHooks sets the hooks, which hear at once of the panes that exited
// while nobody followed them.
func (r *Remote) SetHooks(h session.Hooks) {
	r.mu.Lock()
	r.hooks = h
	var exited []session.Info
	for _, i := range r.infos {
		if i.Status == session.Exited {
			exited = append(exited, i)
		}
	}
	r.mu.Unlock()
	for _, e := range exited {
		code := 0
		if e.ExitCode != nil {
			code = *e.ExitCode
		}
		if h.OnExit != nil {
			go h.OnExit(e.ID, code)
		}
	}
}

func (r *Remote) Owned() bool { return false }

// --- a pane the daemon runs -------------------------------------------------

type remotePane struct {
	r  *Remote
	id string

	mu     sync.Mutex
	term   *terminal.Terminal
	stream *stream
}

func (p *remotePane) ID() string { return p.id }

func (p *remotePane) Info() session.Info {
	p.r.mu.Lock()
	defer p.r.mu.Unlock()
	return p.r.infos[p.id]
}

func (p *remotePane) Changed() <-chan struct{} {
	p.r.mu.Lock()
	defer p.r.mu.Unlock()
	ch := p.r.changed[p.id]
	if ch == nil {
		ch = make(chan struct{})
		p.r.changed[p.id] = ch
	}
	return ch
}

// uiCallTimeout bounds the calls the window makes as it draws or acts:
// a session daemon that stopped answering must not freeze the window.
var uiCallTimeout = 5 * time.Second

// call calls the daemon, giving up after uiCallTimeout.
func (r *Remote) call(method string, params, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), uiCallTimeout)
	defer cancel()
	return r.client().CallContext(ctx, method, params, out)
}

// Term is a terminal of the app's showing the pane: what the daemon's
// shows, frame by frame, and what is typed in it goes to the daemon.
func (p *remotePane) Term() *terminal.Terminal {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.term != nil {
		return p.term
	}
	s, err := attach(p.r.socket, p.id)
	if err != nil {
		return nil
	}
	o := session.StartOptions{}
	if p.r.TermOptions != nil {
		o = p.r.TermOptions()
	}
	t, err := terminal.New(terminal.Options{
		Conn: s, ResizeByConn: true,
		Font: o.Font, Theme: o.Theme, MinimumContrast: o.MinimumContrast,
		Padding: o.Padding, PaddingColor: terminal.PaddingExtend,
		Cursor: o.Cursor, NoBlink: o.NoBlink, CopyOnSelect: o.CopyOnSelect,
		RightClick: o.RightClick, ClipboardRead: o.ClipboardRead, Scrollback: o.Scrollback,
		OpenLink: o.OpenLink, OnZoom: o.OnZoom,
	})
	if err != nil {
		s.Close()
		return nil
	}
	s.setTerm(t)
	p.term, p.stream = t, s
	return t
}

// TermIfOpen is the app's terminal of the pane, nil while it shows none.
func (p *remotePane) TermIfOpen() *terminal.Terminal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.term
}

// Detach closes the app's terminal of a pane, whose attach connection
// goes; the pane goes on, and Term attaches again.
func (r *Remote) Detach(id string) {
	r.mu.Lock()
	h := r.handles[id]
	r.mu.Unlock()
	if h != nil {
		h.detach()
	}
}

// detach closes the app's terminal of the pane; the pane goes on.
func (p *remotePane) detach() {
	p.mu.Lock()
	t := p.term
	p.term, p.stream = nil, nil
	p.mu.Unlock()
	if t != nil {
		t.Close()
	}
}

func (p *remotePane) Write(b []byte) error {
	return p.r.call("send", proto.SendParams{ID: p.id, Data: string(b)}, nil)
}

func (p *remotePane) Text(scrollback bool, lines int) string {
	var out proto.ReadResult
	p.r.call("read", proto.ReadParams{ID: p.id, Scrollback: scrollback, Lines: lines}, &out)
	return out.Text
}

// History is the daemon's to keep.
func (p *remotePane) History(bool) []byte { return nil }

func (p *remotePane) Wait(ctx context.Context, until func(session.Info) bool) (session.Info, error) {
	for {
		info, ch := p.Info(), p.Changed()
		if until(info) {
			return info, nil
		}
		if p.r.Get(p.id) == nil {
			return info, session.ErrClosed // gone from the daemon
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return info, ctx.Err()
		}
	}
}

func (p *remotePane) Flag(title, body string) {
	p.r.call("notify", proto.NotifyParams{ID: p.id, Title: title, Body: body}, nil)
}

func (p *remotePane) SetAgentSession(s *agent.Session) {
	p.r.call("pane.setAgentSession", map[string]any{"ID": p.id, "Session": s}, nil)
}

func (p *remotePane) SetTurn(t *session.Turn) {
	p.r.call("pane.setTurn", map[string]any{"ID": p.id, "Turn": t}, nil)
}

// --- an attach connection ---------------------------------------------------

// stream is a terminal's Conn to a pane of the daemon: it reads the
// pane's frames, and writes what is typed, the size the view fits, and
// its colors.
type stream struct {
	conn    net.Conn
	r       *bufio.Reader
	wmu     sync.Mutex
	pending []byte
	// cols and rows are the pane's size as attached, or as the daemon
	// last resized it before the terminal was set; term is the app's
	// terminal, set once made (tmu guards them: the terminal's reader
	// runs Read as soon as it is made).
	tmu        sync.Mutex
	cols, rows int
	term       *terminal.Terminal
}

// setTerm gives the stream its terminal, sized as the daemon last said.
func (s *stream) setTerm(t *terminal.Terminal) {
	s.tmu.Lock()
	defer s.tmu.Unlock()
	s.term = t
	if s.cols > 0 && s.rows > 0 {
		t.Resize(s.cols, s.rows)
	}
}

func attach(socket, pane string) (*stream, error) {
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(conn).Encode(proto.Hello{Kind: "attach", Version: proto.Version, Pane: pane}); err != nil {
		conn.Close()
		return nil, err
	}
	// The handshake runs as the window draws: a daemon that stopped
	// answering must not freeze it.
	conn.SetDeadline(time.Now().Add(uiCallTimeout))
	r := bufio.NewReaderSize(conn, 64<<10)
	line, err := proto.ReadLine(r, 64<<10)
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	var w proto.Welcome
	if err := json.Unmarshal(line, &w); err != nil {
		conn.Close()
		return nil, err
	}
	if w.Error != "" {
		conn.Close()
		return nil, errors.New(w.Error)
	}
	return &stream{conn: conn, r: r, cols: w.Cols, rows: w.Rows}, nil
}

// Read returns the pane's output frames; a resize the daemon made takes
// effect where it came, between the frames of the old size and the new.
func (s *stream) Read(b []byte) (int, error) {
	for len(s.pending) == 0 {
		kind, payload, err := proto.ReadFrame(s.r)
		if err != nil {
			return 0, io.EOF
		}
		switch kind {
		case proto.FrameOutput:
			s.pending = payload
		case proto.FrameResized:
			if v, ok := proto.ParseUint16s(payload, 2); ok {
				s.tmu.Lock()
				s.cols, s.rows = v[0], v[1]
				if s.term != nil {
					s.term.Resize(v[0], v[1])
				}
				s.tmu.Unlock()
			}
		case proto.FrameClosed:
			return 0, io.EOF
		}
	}
	n := copy(b, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *stream) send(kind byte, payload []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.conn.Write(proto.AppendFrame(nil, kind, payload))
	return err
}

func (s *stream) Write(b []byte) (int, error) {
	if err := s.send(proto.FrameInput, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (s *stream) ResizePixels(cols, rows, cw, ch int) error {
	return s.send(proto.FrameResize, proto.Uint16s(cols, rows, cw, ch))
}

func (s *stream) UseTheme(t *terminal.Theme) error {
	if t == nil {
		return nil
	}
	return s.send(proto.FrameTheme, ThemePayload(t))
}

func (s *stream) Close() error { return s.conn.Close() }

// answer answers a forwarded call through OnCall.
func (r *Remote) answer(c *client.Client, fc ForwardedCall) {
	m := proto.Message{ID: fc.ID}
	var res any
	err := proto.Errorf(proto.CodeAppClosed, "the app does not answer the omaterm command")
	if r.OnCall != nil {
		res, err = r.OnCall(fc.Method, fc.Params)
	}
	if err != nil {
		m.Error, m.Code = err.Error(), proto.CodeOf(err)
	} else if b, err := json.Marshal(res); err == nil {
		m.Result = b
	}
	c.Call("call.answer", m, nil)
}
