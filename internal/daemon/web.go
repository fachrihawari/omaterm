package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/paths"
	"omaterm/internal/theme"
	"omaterm/internal/tunnel"
	"omaterm/internal/web"
)

// webStore is web access's store, beside state.json.
func (d *Daemon) webStore() *web.Store {
	return web.OpenStore(web.StorePath(filepath.Dir(d.stateFile())))
}

// startTunnel is tunnel.Start, apart so tests can fake it. tunnelMu
// orders it and the backoffs, which tests swap while a loop runs.
var (
	tunnelMu            sync.Mutex
	startTunnel         = tunnel.Start
	webTunnelBackoff    = time.Second
	webTunnelBackoffMax = 30 * time.Second
)

// webTunnelAnswer is how long reloadWeb waits for a tunnel it just asked
// for to say where it serves: long enough for the answer to be shown by
// the call that asked, short enough for that call not to hang.
var webTunnelAnswer = 6 * time.Second

// reloadWeb starts, stops or moves the web server as web.json says, runs
// the tunnel that carries it, and tells where it serves. A tunnel that
// fails leaves web access on, its reason in the status.
func (d *Daemon) reloadWeb() (web.Status, error) {
	store := d.webStore()
	cfg, err := store.Config()
	if err != nil {
		return web.Status{Error: err.Error()}, err
	}
	addr := cfg.ListenAddr()
	// A tunnel forwards from the loopback, whatever the server listens
	// on: it reaches this machine either way.
	target := "http://127.0.0.1:" + strconv.Itoa(cfg.ListenPort())
	want := wantTunnel(cfg)
	d.webMu.Lock()
	if d.web != nil && (!cfg.Enabled || d.webAddr != addr) {
		d.web.Close()
		d.web = nil
		log.Printf("web access: stopped")
	}
	// The tunnel follows the server: none while web access is off, and a
	// new one when it moves or the setting changes.
	if d.webTunnel != nil && (!cfg.Enabled || want == "" ||
		d.webTunnel.spec.Provider != want || d.webTunnel.spec.Target != target) {
		d.webTunnel.stop()
		d.webTunnel = nil
	}
	if !cfg.Enabled {
		d.webMu.Unlock()
		return web.Status{}, nil
	}
	if d.web == nil {
		s, err := web.Listen(addr, d, store)
		if err != nil {
			d.webMu.Unlock()
			return web.Status{Addr: addr, Error: err.Error()}, err
		}
		d.web, d.webAddr = s, addr
		log.Printf("web access: http://%s", addr)
	}
	var fresh *webTunnel
	if want != "" && d.webTunnel == nil {
		fresh = startWebTunnel(tunnel.Spec{Provider: want, Target: target, Name: "web access"})
		d.webTunnel = fresh
	}
	st := web.Status{On: true, Addr: d.webAddr}
	if d.webTunnel != nil {
		st.TunnelURL, st.TunnelError = d.webTunnel.status()
	}
	d.webMu.Unlock()
	if fresh != nil {
		// Past the lock, which web.status and the daemon's loop take:
		// the answer can take seconds.
		fresh.waitAnswer()
		st.TunnelURL, st.TunnelError = fresh.status()
	}
	return st, nil
}

// wantTunnel is the provider web.json asks a tunnel of: tailscale or
// cloudflare, "" for none or a name it doesn't know.
func wantTunnel(cfg web.Config) tunnel.Provider {
	switch p := tunnel.Provider(cfg.Tunnel); p {
	case tunnel.Tailscale, tunnel.Cloudflare:
		return p
	}
	return ""
}

// webStatus tells where web access serves and what its tunnel does,
// without changing anything.
func (d *Daemon) webStatus() web.Status {
	d.webMu.Lock()
	defer d.webMu.Unlock()
	st := web.Status{On: d.web != nil, Addr: d.webAddr}
	if d.webTunnel != nil {
		st.TunnelURL, st.TunnelError = d.webTunnel.status()
	}
	return st
}

// webTunnel is the tunnel that carries web access while it serves: a
// goroutine makes it, tells where it serves, and makes it again with a
// backoff when it ends or fails, until stopped.
type webTunnel struct {
	spec    tunnel.Spec
	stopIt  sync.Once
	answers sync.Once
	stopCh  chan struct{}
	// doneCh closes when the goroutine leaves, answer when the first
	// start has said what came of it.
	doneCh, answer chan struct{}
	mu             sync.Mutex
	cancel         context.CancelFunc // the start in flight, ended by stop
	url            string
	err            string
}

// startWebTunnel runs the tunnel spec asks and keeps it up, in its own
// goroutine.
func startWebTunnel(spec tunnel.Spec) *webTunnel {
	r := &webTunnel{spec: spec,
		stopCh: make(chan struct{}), doneCh: make(chan struct{}), answer: make(chan struct{})}
	go r.loop()
	return r
}

// loop keeps the tunnel up while it is wanted.
func (r *webTunnel) loop() {
	defer close(r.doneCh)
	tunnelMu.Lock()
	start, backoff, backoffMax, backoff0 := startTunnel, webTunnelBackoff, webTunnelBackoffMax, webTunnelBackoff
	tunnelMu.Unlock()
	for {
		ctx, cancel := context.WithCancel(context.Background())
		r.setCancel(cancel)
		t, err := start(ctx, r.spec)
		cancel()
		r.setCancel(nil)
		if err != nil {
			// Web access stays on without its tunnel; it says why here,
			// and is tried again.
			var inUse *tunnel.PortInUseError
			if errors.As(err, &inUse) {
				err = fmt.Errorf("%w, or use the Public URL", err)
			}
			r.report("", err)
			log.Printf("%s: tunnel failed: %v", r.spec.Name, err)
			if !r.wait(backoff) {
				return
			}
			backoff = min(backoff*2, backoffMax)
			continue
		}
		r.report(t.URL(), nil)
		log.Printf("%s: tunnel at %s", r.spec.Name, t.URL())
		backoff = backoff0
		select {
		case <-r.stopCh:
			t.Stop()
			return
		case <-t.Done():
			// The tunnel ended by itself: say so, and make it again.
			err := t.Err()
			r.report("", err)
			if err != nil {
				log.Printf("%s: tunnel ended: %v", r.spec.Name, err)
			}
			if !r.wait(backoff) {
				return
			}
			backoff = min(backoff*2, backoffMax)
		}
	}
}

// report tells the tunnel's URL, or why it isn't running, and lets the
// first answer's waiter go.
func (r *webTunnel) report(url string, err error) {
	r.mu.Lock()
	r.url, r.err = url, ""
	if err != nil {
		r.err = err.Error()
	}
	r.mu.Unlock()
	r.answers.Do(func() { close(r.answer) })
}

// wait backs off before another start, unless asked to stop.
func (r *webTunnel) wait(d time.Duration) bool {
	select {
	case <-r.stopCh:
		return false
	case <-time.After(d):
		return true
	}
}

// setCancel notes the start in flight, which stop ends at once.
func (r *webTunnel) setCancel(cancel context.CancelFunc) {
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()
}

// waitAnswer waits for the first start to say what came of it, so the
// call that asked can tell where the tunnel serves.
func (r *webTunnel) waitAnswer() {
	select {
	case <-r.answer:
	case <-time.After(webTunnelAnswer):
	}
}

// status is the tunnel's URL, or why it isn't running.
func (r *webTunnel) status() (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.url, r.err
}

// stop ends the run and the tunnel it keeps, waiting for both.
func (r *webTunnel) stop() {
	r.stopIt.Do(func() { close(r.stopCh) })
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel() // a start in flight fails at once
	}
	<-r.doneCh
}

// webThemePath is where the app's last look for the web page is kept,
// beside web.json, so the page looks like the window after a restart too.
func (d *Daemon) webThemePath() string {
	return filepath.Join(filepath.Dir(d.stateFile()), "web-theme.json")
}

// currentWebTheme is the look the page takes: the app's last, else the
// built-in dark one.
func (d *Daemon) currentWebTheme() web.Theme {
	d.webMu.Lock()
	t := d.webTheme
	d.webMu.Unlock()
	if t != nil {
		return *t
	}
	if b, err := os.ReadFile(d.webThemePath()); err == nil {
		var saved web.Theme
		if json.Unmarshal(b, &saved) == nil && len(saved.Tokens) > 0 {
			d.webMu.Lock()
			d.webTheme = &saved
			d.webMu.Unlock()
			return saved
		}
	}
	return fallbackWebTheme()
}

// fallbackWebTheme is the built-in dark look, derived once: the events
// socket asks for the theme with every state it sends.
var fallbackWebTheme = sync.OnceValue(func() web.Theme { return web.ThemeOf(theme.BuiltinDark()) })

// setWebTheme keeps the app's look for the page, and tells the pages,
// which fetch it again.
func (d *Daemon) setWebTheme(t web.Theme) {
	d.webMu.Lock()
	same := d.webTheme != nil && d.webTheme.Stamp == t.Stamp && t.Stamp != ""
	d.webTheme = &t
	d.webMu.Unlock()
	if same {
		return
	}
	if b, err := json.Marshal(t); err == nil {
		if err := os.WriteFile(d.webThemePath(), b, 0o600); err != nil {
			log.Printf("web access: keeping the theme: %v", err)
		}
	}
	d.changed()
}

// webServing tells whether web access serves.
func (d *Daemon) webServing() bool {
	d.webMu.Lock()
	defer d.webMu.Unlock()
	return d.web != nil
}

func (d *Daemon) stopWeb() {
	d.webMu.Lock()
	defer d.webMu.Unlock()
	if d.web != nil {
		d.web.Close()
		d.web = nil
	}
	if d.webTunnel != nil {
		d.webTunnel.stop()
		d.webTunnel = nil
	}
}

// ReloadWeb tells the session daemon web access changed, starting the
// daemon when asked and none runs.
func ReloadWeb(start bool) (web.Status, error) {
	c, err := client.Dial(paths.DaemonSocketPath())
	if errors.Is(err, client.ErrUnreachable) && start {
		if err := StartProcess(); err != nil {
			return web.Status{}, err
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if c, err = client.Dial(paths.DaemonSocketPath()); err == nil {
				break
			}
		}
	}
	if err != nil {
		return web.Status{}, err
	}
	defer c.Close()
	var st web.Status
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.CallContext(ctx, "web.reload", nil, &st); err != nil {
		return st, err
	}
	if st.Error != "" {
		return st, errors.New(st.Error)
	}
	return st, nil
}

// StatusWeb asks the session daemon where web access serves and what its
// tunnel does, without changing anything. Without a daemon it fails, and
// the caller makes do without it.
func StatusWeb() (web.Status, error) {
	c, err := client.Dial(paths.DaemonSocketPath())
	if err != nil {
		return web.Status{}, err
	}
	defer c.Close()
	var st web.Status
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.CallContext(ctx, "web.status", nil, &st); err != nil {
		return st, err
	}
	return st, nil
}
