// Package daemon is omaterm's session daemon: the process that owns the
// panes' pseudo-terminals, so that shells and agents outlive the app. The
// app attaches to it: it starts and closes panes through its socket, and
// shows each pane through an attach connection that streams the pane's
// screen (terminal.Mirror frames) and takes what is typed. Quitting the
// app detaches it; opening it again attaches where it left off.
//
// Its design follows Kero's kerod (github.com/egoist/kero, MIT,
// docs/third_party/kero-LICENSE): one terminal emulator per pane, here,
// which keeps the screen and scrollback; clients mirror it.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/agent"
	"omaterm/internal/ctl"
	"omaterm/internal/devurl"
	"omaterm/internal/mail"
	"omaterm/internal/model"
	"omaterm/internal/notice"
	"omaterm/internal/paths"
	"omaterm/internal/procs"
	"omaterm/internal/proto"
	"omaterm/internal/session"
	"omaterm/internal/web"
	"omaterm/service"
)

// Options are how the daemon runs.
type Options struct {
	// Serve runs it in the foreground for good, as a service does: it
	// never exits for being idle.
	Serve bool
	// Socket and StateDir override where it listens and keeps histories
	// (tests).
	Socket    string
	StateDir  string
	StateFile string
	// IdleExit is how long it stays with no pane and no client: 30 s when
	// zero.
	IdleExit time.Duration
	// Version is the build's, which the app compares with its own.
	Version string
	// Notifier shows the desktop notifications the daemon sends while no
	// app follows it: notify-send when nil.
	Notifier Notifier
	// Restore starts the sessions of state.json at once, from their
	// scrollback, as a service does at login: no app is there to.
	Restore bool
	// ProxyAddr is where the Dev URLs proxy listens: 127.0.0.1:443 when
	// empty (tests give a free port).
	ProxyAddr string
}

// Daemon is the running daemon.
type Daemon struct {
	opts    Options
	reg     *session.Registry
	procs   *procs.Supervisor
	mailbox *mail.Mailbox
	srv     *ctl.Server
	quit    chan struct{}
	once    sync.Once
	theme   *terminal.Theme
	lock    *os.File
	started time.Time
	limit   notice.Limiter
	// stateMu orders the changes to state.json the daemon makes while no
	// app runs.
	stateMu      sync.Mutex
	procMu       sync.Mutex
	procProjects map[string]model.Project
	procMtimes   map[string]fileStamp
	// historyMu orders a pane's close against the writing of its
	// scrollback, which reads it off the loop goroutine.
	historyMu sync.Mutex

	mu      sync.Mutex
	subs    map[int]func(proto.Message)
	nextSub int
	pending bool
	clients int
	// inFlight are control calls being handled; exiting is set as the
	// daemon leaves for being idle, after which calls are turned away.
	inFlight int
	exiting  bool
	idleAt   time.Time
	// attached are each pane's attachments.
	attached map[string]map[*attachment]struct{}
	// env is the app's display and session variables (env.update), which
	// new panes take over the daemon's own: the daemon may have started
	// in another session than the app's latest.
	env map[string]string
	// calls are the omaterm command's calls forwarded to the app, by id,
	// waiting for its answer.
	calls    map[int64]chan proto.Message
	nextCall int64
	// watchers are told the panes changed: the web server's pages, which
	// unlike subscribers are no app.
	watchers  map[int]func()
	nextWatch int

	webMu   sync.Mutex
	web     *web.Server
	webAddr string // the address asked of web
	// webTunnel carries web access beyond this machine while it serves;
	// nil when none is wanted.
	webTunnel *webTunnel
	// webTheme is the app's look for the web page, as it last sent it;
	// nil until one is sent or read back from web-theme.json.
	webTheme *web.Theme

	// devMu orders what the Dev URLs proxy is and the routes it has; devView
	// is what the processes show of them, under its own lock so that the
	// supervisor can read it while it holds its own.
	devMu     sync.Mutex
	dev       *devurl.Server
	devErr    string // why the proxy doesn't run
	devRetry  bool   // the address was refused, so binding it again may work
	devRouter *devurl.Router
	devViewMu sync.Mutex
	devView   *devView

	// shares are the processes shared through a tunnel, and shareNotes why
	// a share of one ended; shareMu orders both.
	shareMu    sync.Mutex
	shares     map[procKey]*share
	shareNotes map[procKey]string
}

func (d *Daemon) currentTheme() *terminal.Theme {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.theme
}

// envList is env as KEY=value.
func (d *Daemon) envList() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for k, v := range d.env {
		out = append(out, k+"="+v)
	}
	return out
}

// Run runs the daemon until it is stopped, or idle for a while.
func Run(opts Options) error {
	d, err := Start(opts)
	if err != nil {
		return err
	}
	sigs := make(chan os.Signal, 1)
	signal.Ignore(syscall.SIGHUP, syscall.SIGPIPE)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	select {
	case sig := <-sigs:
		log.Printf("stopping: %v", sig)
	case <-d.quit:
	}
	d.Stop()
	return nil
}

// Start starts the daemon and returns it running.
func Start(opts Options) (*Daemon, error) {
	if opts.Socket == "" {
		opts.Socket = paths.DaemonSocketPath()
	}
	if opts.StateDir == "" {
		opts.StateDir = paths.StateDir()
	}
	if opts.IdleExit == 0 {
		opts.IdleExit = 30 * time.Second
	}
	if err := os.MkdirAll(filepath.Join(opts.StateDir, "sessions"), 0o700); err != nil {
		return nil, err
	}
	d := &Daemon{opts: opts, reg: session.NewRegistry(), quit: make(chan struct{}),
		subs: map[int]func(proto.Message){}, attached: map[string]map[*attachment]struct{}{}, idleAt: time.Now(),
		started: time.Now(), env: map[string]string{}, calls: map[int64]chan proto.Message{}, watchers: map[int]func(){},
		procProjects: map[string]model.Project{}, procMtimes: map[string]fileStamp{},
		devRouter: devurl.NewRouter(), shares: map[procKey]*share{}, shareNotes: map[procKey]string{}}
	d.procs = procs.New(daemonRunner{d: d}, procs.OpenTrust(procs.TrustPath()))
	d.procs.OnEvent = d.processEvent
	d.procs.OnChange = func() {
		d.rebuildRoutes()
		d.reconcileShares()
		d.changed()
	}
	d.procs.Decorate = func(project string, p *procs.Proc) {
		d.decorateProc(project, p)
		d.decorateShare(project, p)
	}
	d.procs.Watch = WatchFolder
	d.procs.Match = service.MatchGlob
	d.procs.Ignore = service.IgnoreFunc
	d.reg.OnOutput = d.output
	d.reg.OnExit = func(id string, code int) { d.changed() }
	d.reg.OnStatus = func(info session.Info, prev session.Status) {
		d.changed()
		d.notifyStatus(info, prev)
	}
	d.reg.OnNotice = func(id, title, body string) {
		d.broadcast(proto.Message{Event: "notice", Result: mustJSON(map[string]string{"pane": id, "title": title, "body": body})})
		d.notifyProgram(id, title, body)
	}
	os.MkdirAll(filepath.Dir(opts.Socket), 0o700)
	var err error
	d.lock, err = lock(opts.Socket + ".lock")
	// A successor (OMATERM_TAKEOVER=1) waits for the daemon it replaces
	// to save its sessions and let go.
	for deadline := time.Now().Add(15 * time.Second); err != nil && os.Getenv("OMATERM_TAKEOVER") == "1" && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		d.lock, err = lock(opts.Socket + ".lock")
	}
	if err != nil {
		return nil, ctl.ErrRunning
	}
	srv, err := ctl.Listen(opts.Socket, d, opts.Version)
	if err != nil {
		d.lock.Close()
		return nil, err
	}
	d.srv = srv
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-d.quit; cancel() }()
	go d.reg.Run(ctx)
	d.mailbox = mail.New(d.reg)
	go d.mailbox.Run(ctx)
	go d.loop(ctx)
	if opts.Restore {
		d.restore()
	}
	d.loadProcesses()
	go d.sweepHistories()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-d.reg.Changed():
				d.changed()
			}
		}
	}()
	if _, err := d.reloadWeb(); err != nil {
		log.Printf("web access: %v", err)
	}
	d.reloadProxy()
	log.Printf("omaterm daemon %s started, pid %d, %s", opts.Version, os.Getpid(), opts.Socket)
	for _, path := range paths.RemoveLegacy(opts.StateDir) {
		log.Printf("removed %s, left by the first omaterm", path)
	}
	return d, nil
}

// waitStarted waits for the pane another call is starting.
func (d *Daemon) waitStarted(id string) (*session.Pane, error) {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if pn := d.reg.Get(id); pn != nil {
			return pn, nil
		}
	}
	return nil, proto.Errorf(proto.CodeNotFound, "pane "+id+" did not start")
}

// Stop saves every pane's history, ends the panes and stops listening.
func (d *Daemon) Stop() {
	d.once.Do(func() {
		log.Printf("stopped, %d panes saved", d.reg.Len())
		d.procs.Close()
		d.saveHistories(true)
		close(d.quit)
		d.stopWeb()
		d.stopProxy()
		d.stopShares()
		d.srv.Close()
		d.reg.CloseAll()
		d.lock.Close()
	})
}

// Done is closed once the daemon stops.
func (d *Daemon) Done() <-chan struct{} { return d.quit }

// loop saves histories every 10 seconds, and ends the daemon once it has
// had no pane and no client for a while, unless it serves.
func (d *Daemon) loop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if n++; n%10 == 0 {
				d.saveHistories(false)
			}
			if n%proxyRetryTicks == 0 {
				d.retryProxy()
			}
			if d.processFilesChanged() {
				d.loadProcesses()
			}
			// Web access keeps it: a browser may come for the sessions.
			serving := d.webServing()
			d.mu.Lock()
			idle := d.reg.Len() == 0 && d.clients == 0 && d.inFlight == 0 && !d.opts.Serve && !serving
			if !idle {
				d.idleAt = now
			}
			idleFor := now.Sub(d.idleAt)
			exit := idle && idleFor > d.opts.IdleExit
			d.exiting = exit // from here, calls are turned away
			d.mu.Unlock()
			if exit {
				log.Printf("idle: exiting")
				d.once.Do(func() {
					d.procs.Close()
					close(d.quit)
					d.stopWeb()
					d.stopProxy()
					d.srv.Close()
					d.lock.Close()
				})
				return
			}
		}
	}
}

func (d *Daemon) historyPath(id string) string {
	return filepath.Join(d.opts.StateDir, "sessions", id+".vt")
}

func (d *Daemon) saveHistories(force bool) {
	d.reg.Each(func(p *session.Pane) {
		if h := p.History(force); h != nil {
			d.writeHistory(p.ID(), h)
		}
	})
}

// writeHistory writes a pane's scrollback, unless the pane closed while
// it was read: written then, it stayed on disk after the close removed it.
func (d *Daemon) writeHistory(id string, h []byte) {
	d.historyMu.Lock()
	defer d.historyMu.Unlock()
	if d.reg.Get(id) != nil {
		session.SaveHistory(d.historyPath(id), h)
	}
}

// removePane ends a pane and removes its scrollback, in turn with
// saveHistories.
func (d *Daemon) removePane(id string) {
	d.historyMu.Lock()
	d.reg.Remove(id)
	os.Remove(d.historyPath(id))
	d.historyMu.Unlock()
}

// sweepHistories removes, at start, the scrollback of panes no session
// holds any more (closed while no app or daemon ran, or by an older
// version), once it is a day old: it stayed on disk for good.
func (d *Daemon) sweepHistories() {
	d.stateMu.Lock()
	st := d.loadState()
	d.stateMu.Unlock()
	known := map[string]bool{}
	for _, s := range st.Sessions {
		for _, p := range s.Panes {
			known[p.ID] = true
		}
	}
	dir := filepath.Join(d.opts.StateDir, "sessions")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".vt")
		if !ok {
			// A write a crash left half done.
			if strings.HasSuffix(e.Name(), ".tmp") {
				if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
					os.Remove(filepath.Join(dir, e.Name()))
				}
			}
			continue
		}
		if known[id] || d.reg.Get(id) != nil {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// --- the control connection ----------------------------------------------

// StartParams start a pane: the session.StartOptions that travel.
type StartParams struct {
	ID, Session, Project, Dir string
	ProjectName, ProjectDir   string
	Role                      string
	Command                   []string
	Exec                      bool
	Env                       []string
	Cols, Rows                int
	History                   []byte
}

// Handle runs a method of the daemon's socket.
func (d *Daemon) Handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	// A call in flight keeps the daemon from its idle exit: an omaterm
	// new arriving as it exited had its session saved and its shell
	// started, then killed with the daemon.
	d.mu.Lock()
	if d.exiting {
		d.mu.Unlock()
		return nil, proto.Errorf(proto.CodeBusy, "the session daemon is exiting: try again")
	}
	d.inFlight++
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.inFlight--
		d.mu.Unlock()
	}()
	return d.handle(ctx, method, raw)
}

func (d *Daemon) handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if mail.IsMethod(method) {
		// The daemon's own, as its panes are: never the app's.
		return d.mailbox.Serve(ctx, method, raw)
	}
	pane := func(id string) (*session.Pane, error) {
		if p := d.reg.Get(id); p != nil {
			return p, nil
		}
		return nil, proto.Errorf(proto.CodeNotFound, "no pane "+id)
	}
	switch method {
	case "version":
		return map[string]any{"app": d.opts.Version, "proto": proto.Version, "pid": os.Getpid()}, nil
	case "panes":
		return d.reg.Infos(), nil
	case "process.list", "process.start", "process.stop", "process.restart", "process.trust", "process.sync", "process.share", "process.unshare":
		return d.processCall(method, raw)
	case "pane.start":
		var p StartParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, proto.Errorf(proto.CodeUsage, err.Error())
		}
		if pn := d.reg.Get(p.ID); pn != nil {
			return pn.Info(), nil
		}
		o := session.StartOptions{ID: p.ID, Session: p.Session, Project: p.Project, ProjectName: p.ProjectName, ProjectDir: p.ProjectDir, Role: p.Role,
			Dir: p.Dir, Command: p.Command, Exec: p.Exec, Env: append(d.envList(), p.Env...), Cols: p.Cols, Rows: p.Rows, History: p.History,
			Theme: d.currentTheme()}
		d.termPrefs().apply(&o)
		pn, err := d.reg.Start(o)
		if errors.Is(err, session.ErrExists) {
			// Being started meanwhile, as by a restore at login: it is
			// that pane, once it runs.
			pn, err = d.waitStarted(p.ID)
		}
		if err != nil {
			return nil, err
		}
		d.changed()
		return pn.Info(), nil
	case "pane.close":
		var p proto.IDParams
		json.Unmarshal(raw, &p)
		if !d.appAttached() {
			// The omaterm command's: the pane goes from its session too.
			return d.closePane(p.ID)
		}
		d.closeAttachments(p.ID)
		d.removePane(p.ID)
		d.changed()
		return true, nil
	case "send":
		var p proto.SendParams
		json.Unmarshal(raw, &p)
		pn, err := pane(p.ID)
		if err != nil {
			return nil, err
		}
		err = pn.Send(ctx, p.Data, p.Paste)
		var held session.HoldError
		if errors.As(err, &held) {
			return nil, proto.Errorf(proto.CodeBusy, "pane "+p.ID+": "+err.Error())
		}
		return true, err
	case "key":
		var p proto.KeyParams
		json.Unmarshal(raw, &p)
		pn, err := pane(p.ID)
		if err != nil {
			return nil, err
		}
		for _, k := range p.Keys {
			if err := pn.Key(k); err != nil {
				return nil, proto.Errorf(proto.CodeUsage, err.Error())
			}
		}
		return true, nil
	case "pane.type":
		// What the user typed, from the app (session.Handle.Write).
		var p proto.SendParams
		json.Unmarshal(raw, &p)
		pn, err := pane(p.ID)
		if err != nil {
			return nil, err
		}
		return true, pn.Write([]byte(p.Data))
	case "read":
		var p proto.ReadParams
		json.Unmarshal(raw, &p)
		pn, err := pane(p.ID)
		if err != nil {
			return nil, err
		}
		return proto.ReadResult{Text: pn.Text(p.Scrollback, p.Lines)}, nil
	case "notify":
		var p proto.NotifyParams
		json.Unmarshal(raw, &p)
		pn, err := pane(p.ID)
		if err != nil {
			return nil, err
		}
		pn.Flag(p.Title, p.Body)
		return true, nil
	case "report":
		var p proto.ReportParams
		json.Unmarshal(raw, &p)
		pn, err := pane(p.ID)
		if err != nil {
			return nil, err
		}
		if err := pn.Report(p.Body); err != nil {
			return nil, proto.Errorf(proto.CodeUsage, err.Error())
		}
		return true, nil
	case "pane.setAgentSession":
		var p struct {
			ID      string
			Session *agent.Session
		}
		json.Unmarshal(raw, &p)
		pn, err := pane(p.ID)
		if err != nil {
			return nil, err
		}
		pn.SetAgentSession(p.Session)
		return true, nil
	case "pane.setTurn":
		var p struct {
			ID   string
			Turn *session.Turn
		}
		json.Unmarshal(raw, &p)
		pn, err := pane(p.ID)
		if err != nil {
			return nil, err
		}
		pn.SetTurn(p.Turn)
		return true, nil
	case "viewed":
		var p proto.IDParams
		json.Unmarshal(raw, &p)
		d.reg.SetViewed(p.ID)
		return true, nil
	case "shutdown":
		// Who asked, should the user wonder why their sessions restarted.
		log.Printf("stopping: shutdown asked")
		go d.Stop()
		return true, nil
	case "env.update":
		var env map[string]string
		json.Unmarshal(raw, &env)
		d.mu.Lock()
		for k, v := range env {
			if envShared(k) {
				d.env[k] = v
			}
		}
		d.mu.Unlock()
		return true, nil
	case "call.answer":
		var m proto.Message
		json.Unmarshal(raw, &m)
		d.mu.Lock()
		ch := d.calls[m.ID]
		delete(d.calls, m.ID) // the first app to answer wins
		d.mu.Unlock()
		if ch != nil {
			ch <- m
		}
		return true, nil
	case "proxy.status":
		return d.proxyStatus(), nil
	case "proxy.reload":
		return d.withChecks(d.reloadProxy()), nil
	case "web.reload":
		return d.reloadWeb()
	case "web.status":
		return d.webStatus(), nil
	case "web.theme":
		return d.currentWebTheme(), nil
	case "web.setTheme":
		var t web.Theme
		if err := json.Unmarshal(raw, &t); err != nil || len(t.Tokens) == 0 {
			return nil, proto.Errorf(proto.CodeUsage, "bad theme")
		}
		d.setWebTheme(t)
		return true, nil
	case "clients":
		d.mu.Lock()
		defer d.mu.Unlock()
		c := proto.Clients{Apps: len(d.subs), Attached: map[string]int{}}
		for id, ats := range d.attached {
			if len(ats) > 0 {
				c.Attached[id] = len(ats)
			}
		}
		return c, nil
	}
	return d.handleOffline(ctx, method, raw)
}

// Subscribe adds a subscriber of the panes: an event "panes" with every
// pane's Info as they change, and "notice" for programs' notifications.
func (d *Daemon) Subscribe(fn func(proto.Message)) func() {
	d.mu.Lock()
	d.nextSub++
	id := d.nextSub
	d.subs[id] = fn
	d.clients++
	d.mu.Unlock()
	d.changed()
	return func() {
		d.mu.Lock()
		delete(d.subs, id)
		d.clients--
		gone := len(d.subs) == 0
		// Calls passed to an app that is gone get no answer: fail them
		// now, rather than leave the omaterm command waiting.
		var pending []chan proto.Message
		if gone {
			for id, ch := range d.calls {
				pending = append(pending, ch)
				delete(d.calls, id)
			}
		}
		d.mu.Unlock()
		for _, ch := range pending {
			select {
			case ch <- proto.Message{Error: "the omaterm window closed before it answered", Code: proto.CodeAppClosed}:
			default:
			}
		}
		// With no app, no session is in view: what they do from now on is
		// unread, and notifies.
		if gone {
			d.reg.SetViewed("")
		}
	}
}

func (d *Daemon) broadcast(m proto.Message) {
	d.mu.Lock()
	subs := make([]func(proto.Message), 0, len(d.subs))
	for _, fn := range d.subs {
		subs = append(subs, fn)
	}
	d.mu.Unlock()
	for _, fn := range subs {
		fn(m)
	}
}

// changed sends the panes to the subscribers, once a burst of changes
// settles.
func (d *Daemon) changed() {
	d.mu.Lock()
	if d.pending {
		d.mu.Unlock()
		return
	}
	d.pending = true
	d.mu.Unlock()
	time.AfterFunc(25*time.Millisecond, func() {
		d.mu.Lock()
		d.pending = false
		d.mu.Unlock()
		d.broadcast(proto.Message{Event: "panes", Result: mustJSON(d.reg.Infos())})
		d.mu.Lock()
		watchers := make([]func(), 0, len(d.watchers))
		for _, fn := range d.watchers {
			watchers = append(watchers, fn)
		}
		d.mu.Unlock()
		for _, fn := range watchers {
			fn()
		}
	})
}

// Watch calls fn as the panes change, until stopped. A watcher, unlike a
// subscriber, is no app: notifications and calls go on as without one.
func (d *Daemon) Watch(fn func()) func() {
	d.mu.Lock()
	d.nextWatch++
	id := d.nextWatch
	d.watchers[id] = fn
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		delete(d.watchers, id)
		d.mu.Unlock()
	}
}

// AttachPipe attaches to a pane in process: the connection carries what
// an attach socket would, the welcome line and then frames.
func (d *Daemon) AttachPipe(pane string) net.Conn {
	ours, theirs := net.Pipe()
	go func() {
		defer ours.Close()
		// A bug in a web client's attachment ends it, not the daemon and
		// every session in it, as on the socket.
		defer func() {
			if r := recover(); r != nil {
				log.Printf("web attach panicked: %v\n%s", r, debug.Stack())
			}
		}()
		d.attach(ours, bufio.NewReader(ours), proto.Hello{Kind: "attach", Version: proto.Version, Pane: pane},
			proto.Welcome{Version: proto.Version, PID: os.Getpid(), Started: d.started.UnixMilli(), App: d.opts.Version}, true)
	}()
	return theirs
}

// --- attach connections ---------------------------------------------------

// attachment is a client showing a pane: its frames go out through a
// queue, so a slow client never holds the pane.
type attachment struct {
	conn   net.Conn
	mirror *terminal.Mirror
	out    chan []byte
	done   chan struct{}
	once   sync.Once
	// framing tells that a frame is due shortly; frameMu keeps frames
	// made and queued in order.
	framing bool
	frameMu sync.Mutex
	// web tells a web page's attachment; size is the last size the
	// attachment asked for, and sized when (guarded by Daemon.mu).
	web   bool
	size  []int
	sized time.Time
}

func (at *attachment) send(kind byte, payload []byte) {
	f := proto.AppendFrame(nil, kind, payload)
	select {
	case at.out <- f:
	case <-at.done:
	default:
		at.finish() // too far behind: it attaches again, from a first frame
	}
}

func (at *attachment) finish() { at.once.Do(func() { close(at.done) }) }

// Attach serves an attach connection: the pane's screen as frames, and
// what the client types, its size and its colors.
func (d *Daemon) Attach(conn net.Conn, r *bufio.Reader, hello proto.Hello, welcome proto.Welcome) {
	d.attach(conn, r, hello, welcome, false)
}

func (d *Daemon) attach(conn net.Conn, r *bufio.Reader, hello proto.Hello, welcome proto.Welcome, web bool) {
	p := d.reg.Get(hello.Pane)
	enc := json.NewEncoder(conn)
	if p == nil {
		welcome.Error = "no pane " + hello.Pane
		enc.Encode(welcome)
		return
	}
	m, err := p.Term().NewMirror()
	if err != nil {
		welcome.Error = err.Error()
		enc.Encode(welcome)
		return
	}
	at := &attachment{conn: conn, mirror: m, out: make(chan []byte, 8192), done: make(chan struct{}), web: web}
	defer m.Close()
	welcome.Cols, welcome.Rows = p.Term().Size()
	if enc.Encode(welcome) != nil {
		return
	}
	d.mu.Lock()
	if d.attached[hello.Pane] == nil {
		d.attached[hello.Pane] = map[*attachment]struct{}{}
	}
	d.attached[hello.Pane][at] = struct{}{}
	d.clients++
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.attached[hello.Pane], at)
		d.clients--
		back := d.appSize(hello.Pane, at)
		d.mu.Unlock()
		at.finish()
		if back != nil {
			d.resize(hello.Pane, p, back[0], back[1], back[2], back[3])
		}
	}()
	at.sendFrame()
	go func() {
		defer conn.Close()
		for {
			select {
			case f := <-at.out:
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if _, err := conn.Write(f); err != nil {
					at.finish()
					return
				}
			case <-at.done:
				// Flush what is queued, as a closing frame.
				for {
					select {
					case f := <-at.out:
						conn.Write(f)
					default:
						return
					}
				}
			}
		}
	}()
	for {
		kind, payload, err := proto.ReadFrame(r)
		if err != nil {
			return
		}
		switch kind {
		case proto.FrameInput:
			p.Write(payload)
		case proto.FrameKey:
			// A key by name, encoded as the program asked keys to be.
			p.Key(string(payload))
		case proto.FramePaste:
			// The user's paste: bracketed when the program takes those.
			p.PasteText(string(payload))
		case proto.FrameResize:
			if v, ok := proto.ParseUint16s(payload, 4); ok && v[0] > 0 && v[1] > 0 {
				d.mu.Lock()
				at.size, at.sized = v, time.Now()
				d.mu.Unlock()
				d.resize(hello.Pane, p, v[0], v[1], v[2], v[3])
			}
		case proto.FrameTheme:
			if th, ok := ParseTheme(payload); ok {
				d.mu.Lock()
				d.theme = th
				d.mu.Unlock()
				d.reg.Each(func(pn *session.Pane) { pn.Term().UseTheme(th) })
			}
		}
	}
}

// appSize is the size a pane goes back to as a web page that sized it,
// gone, leaves it: the size the app's window last asked for, unless
// another page sized it since. The app asks again only when its view
// changes size, so without it the window would keep the page's size.
// d.mu is held.
func (d *Daemon) appSize(pane string, gone *attachment) []int {
	if !gone.web || gone.size == nil {
		return nil
	}
	var last *attachment
	for at := range d.attached[pane] {
		if at.size != nil && (last == nil || at.sized.After(last.sized)) {
			last = at
		}
	}
	if last == nil || last.web {
		return nil
	}
	return last.size
}

// resize gives a pane the size its client fits: the client that resized
// last wins. The clients are told where the screen took it, between the
// frames of the old size and those of the new.
// maxCols and maxRows bound a size a client asks for: a web page is a
// paired device, and a screen of 65535² cells would take the daemon's
// memory, and every session with it.
const maxCols, maxRows = 1000, 500

func (d *Daemon) resize(id string, p *session.Pane, cols, rows, cw, ch int) {
	cols, rows = min(max(cols, 1), maxCols), min(max(rows, 1), maxRows)
	cw, ch = min(cw, 200), min(ch, 200)
	if c, r := p.Term().Size(); c == cols && r == rows {
		return
	}
	d.frame(id)
	p.Term().ResizePixels(cols, rows, cw, ch)
	d.mu.Lock()
	for at := range d.attached[id] {
		at.send(proto.FrameResized, proto.Uint16s(cols, rows))
	}
	d.mu.Unlock()
	d.frame(id)
}

// output frames a pane's clients shortly after its program writes: the
// terminal takes the output as the read returns, and a program's redraw
// comes in pieces. A second look a moment later catches what came last.
func (d *Daemon) output(id string) {
	d.mu.Lock()
	ats := d.attached[id]
	due := false
	for at := range ats {
		if !at.framing {
			at.framing, due = true, true
		}
	}
	d.mu.Unlock()
	if !due {
		return
	}
	time.AfterFunc(8*time.Millisecond, func() {
		d.mu.Lock()
		for at := range d.attached[id] {
			at.framing = false
		}
		d.mu.Unlock()
		d.frame(id)
		time.AfterFunc(50*time.Millisecond, func() { d.frame(id) })
	})
}

// frame sends each client of a pane what brings its screen to the pane's.
func (d *Daemon) frame(id string) {
	d.mu.Lock()
	ats := make([]*attachment, 0, len(d.attached[id]))
	for at := range d.attached[id] {
		ats = append(ats, at)
	}
	d.mu.Unlock()
	for _, at := range ats {
		if at.sendFrame() {
			time.AfterFunc(50*time.Millisecond, func() { d.frame(id) })
		}
	}
}

// sendFrame sends the client what brings its screen to the pane's, and
// reports whether another frame is due. Frames build on the one before,
// so one is made and queued at a time: two timers framing at once sent
// them out of order, garbling the client's screen. A bug in framing ends
// this client, not the daemon.
func (at *attachment) sendFrame() (due bool) {
	at.frameMu.Lock()
	defer at.frameMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("framing a pane panicked: %v\n%s", r, debug.Stack())
			due = false
			at.finish()
		}
	}()
	// Still sending the last ones (a slow phone on a tunnel): make none
	// now. The next frame brings the screen up to date anyway, where
	// queuing one per redraw held up to thousands of full screens.
	if len(at.out) > 2 {
		return true
	}
	f, due := at.mirror.Frame()
	if len(f) > 0 {
		at.send(proto.FrameOutput, f)
	}
	return due
}

func (d *Daemon) closeAttachments(id string) {
	d.mu.Lock()
	ats := d.attached[id]
	delete(d.attached, id)
	d.mu.Unlock()
	for at := range ats {
		at.send(proto.FrameClosed, nil)
		at.finish()
	}
}

// ThemePayload encodes a terminal's colors for FrameTheme.
func ThemePayload(t *terminal.Theme) []byte {
	b := make([]byte, 0, 4*19)
	add := func(c ui.Color) { b = append(b, c.R, c.G, c.B, c.A) }
	add(t.Foreground)
	add(t.Background)
	add(t.Cursor)
	for _, c := range t.Palette {
		add(c)
	}
	return b
}

// ParseTheme decodes a FrameTheme payload.
func ParseTheme(b []byte) (*terminal.Theme, bool) {
	if len(b) != 4*19 {
		return nil, false
	}
	col := func(i int) ui.Color { return ui.Color{R: b[4*i], G: b[4*i+1], B: b[4*i+2], A: b[4*i+3]} }
	t := &terminal.Theme{Foreground: col(0), Background: col(1), Cursor: col(2)}
	for i := range t.Palette {
		t.Palette[i] = col(3 + i)
	}
	return t, true
}

// ErrNotRunning is Stop's error when no daemon answers.
var ErrNotRunning = errors.New("the session daemon is not running")

var _ = fmt.Sprint
