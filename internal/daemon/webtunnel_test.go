//go:build linux || darwin

package daemon

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"omaterm/internal/tunnel"
	"omaterm/internal/web"
)

// fakeTunnel is a tunnel a test drives by hand: end closes it as one
// that ended by itself, Stop as one the daemon stopped.
type fakeTunnel struct {
	url  string
	done chan struct{}
	// fail is the error Err gives once the tunnel has ended.
	fail error

	mu      sync.Mutex
	stopped bool
}

func (f *fakeTunnel) URL() string { return f.url }
func (f *fakeTunnel) Err() error  { return f.fail }
func (f *fakeTunnel) Done() <-chan struct{} {
	return f.done
}

// end closes the tunnel as one that ended by itself.
func (f *fakeTunnel) end() { close(f.done) }

func (f *fakeTunnel) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.stopped {
		f.stopped = true
		close(f.done)
	}
	return nil
}

func (f *fakeTunnel) isStopped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

// swapStartTunnel replaces the tunnel the daemon runs until undone.
func swapStartTunnel(fake func(context.Context, tunnel.Spec) (tunnel.Tunnel, error)) (undo func()) {
	tunnelMu.Lock()
	was := startTunnel
	startTunnel = fake
	tunnelMu.Unlock()
	return func() {
		tunnelMu.Lock()
		startTunnel = was
		tunnelMu.Unlock()
	}
}

// setWebTunnelBackoff makes the daemon retry a tunnel at once, until
// undone.
func setWebTunnelBackoff(d time.Duration) (undo func()) {
	tunnelMu.Lock()
	was, wasMax := webTunnelBackoff, webTunnelBackoffMax
	webTunnelBackoff, webTunnelBackoffMax = d, d
	tunnelMu.Unlock()
	return func() {
		tunnelMu.Lock()
		webTunnelBackoff, webTunnelBackoffMax = was, wasMax
		tunnelMu.Unlock()
	}
}

// freePort is a port nothing listens on.
func freePort() int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// reload asks the daemon to follow web.json again.
func reload(t *testing.T, d *Daemon) web.Status {
	t.Helper()
	res, err := d.Handle(context.Background(), "web.reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.(web.Status)
}

// Web access's tunnel runs with it — its URL in its status, and
// web.status saying so without reloading — and is made again when it
// ends by itself. Turning web access off stops it.
func TestWebTunnelRunsRestartsAndStops(t *testing.T) {
	d, _ := startDaemon(t, time.Minute)
	undoBackoff := setWebTunnelBackoff(10 * time.Millisecond)
	defer undoBackoff()
	var mu sync.Mutex
	var specs []tunnel.Spec
	started := make(chan *fakeTunnel, 8)
	undoStart := swapStartTunnel(func(ctx context.Context, s tunnel.Spec) (tunnel.Tunnel, error) {
		mu.Lock()
		specs = append(specs, s)
		mu.Unlock()
		f := &fakeTunnel{url: "https://box.tailnet.ts.net", done: make(chan struct{})}
		select {
		case started <- f:
		default:
		}
		return f, nil
	})
	defer undoStart()

	port := freePort()
	d.webStore().SetConfig(func(c *web.Config) { c.Enabled, c.Port, c.Tunnel = true, port, "tailscale" })
	st := reload(t, d)
	if st.On != true || st.TunnelURL != "https://box.tailnet.ts.net" {
		t.Fatalf("status %+v", st)
	}
	mu.Lock()
	spec := specs[0]
	mu.Unlock()
	if want := (tunnel.Spec{Provider: tunnel.Tailscale, Target: "http://127.0.0.1:" + strconv.Itoa(port), Name: "web access"}); spec != want {
		t.Fatalf("spec %+v, want %+v", spec, want)
	}
	// web.status tells the same without changing anything.
	res, err := d.Handle(context.Background(), "web.status", nil)
	if err != nil {
		t.Fatal(err)
	}
	if st2 := res.(web.Status); !st2.On || st2.TunnelURL != st.TunnelURL {
		t.Fatalf("web.status %+v", st2)
	}

	// The tunnel ends by itself: another is made.
	first := <-started
	first.end()
	waitFor(t, "a second tunnel", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(specs) == 2
	})

	// Web access off stops the second.
	second := <-started
	d.webStore().SetConfig(func(c *web.Config) { c.Enabled = false })
	if st := reload(t, d); st.On {
		t.Fatalf("still on: %+v", st)
	}
	waitFor(t, "the tunnel stopped", second.isStopped)
}

// A tunnel that fails to start leaves web access on, its reason in the
// status, and is tried again until it works.
func TestWebTunnelFailsThenRecovers(t *testing.T) {
	d, _ := startDaemon(t, time.Minute)
	undoBackoff := setWebTunnelBackoff(10 * time.Millisecond)
	defer undoBackoff()
	var mu sync.Mutex
	tries := 0
	undoStart := swapStartTunnel(func(ctx context.Context, s tunnel.Spec) (tunnel.Tunnel, error) {
		mu.Lock()
		defer mu.Unlock()
		tries++
		if tries == 1 {
			return nil, errors.New("cloudflared is not installed")
		}
		return &fakeTunnel{url: "https://box.tailnet.ts.net", done: make(chan struct{})}, nil
	})
	defer undoStart()

	port := freePort()
	d.webStore().SetConfig(func(c *web.Config) { c.Enabled, c.Port, c.Tunnel = true, port, "cloudflare" })
	st := reload(t, d)
	if !st.On || st.TunnelError != "cloudflared is not installed" {
		t.Fatalf("a failed tunnel stopped web access: %+v", st)
	}
	waitFor(t, "the tunnel to be made again", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return tries >= 2
	})
	waitFor(t, "the tunnel's URL", func() bool {
		return d.webStatus().TunnelURL == "https://box.tailnet.ts.net"
	})
}

// A change of the tunnel setting stops the old tunnel and runs the new
// one; so does moving web access to another port. A reload that asks
// for the same tunnel leaves it be.
func TestWebTunnelFollowsTheSetting(t *testing.T) {
	d, _ := startDaemon(t, time.Minute)
	var mu sync.Mutex
	var specs []tunnel.Spec
	var running []*fakeTunnel
	undoStart := swapStartTunnel(func(ctx context.Context, s tunnel.Spec) (tunnel.Tunnel, error) {
		mu.Lock()
		specs = append(specs, s)
		f := &fakeTunnel{url: "https://box.tailnet.ts.net", done: make(chan struct{})}
		running = append(running, f)
		mu.Unlock()
		return f, nil
	})
	defer undoStart()

	port := freePort()
	set := func(change func(*web.Config)) {
		t.Helper()
		d.webStore().SetConfig(change)
		reload(t, d)
	}
	set(func(c *web.Config) { c.Enabled, c.Port, c.Tunnel = true, port, "tailscale" })
	set(func(c *web.Config) { c.Tunnel = "tailscale" }) // nothing changed
	mu.Lock()
	if len(specs) != 1 {
		mu.Unlock()
		t.Fatalf("%d tunnels for the same setting", len(specs))
	}
	mu.Unlock()

	set(func(c *web.Config) { c.Tunnel = "cloudflare" })
	mu.Lock()
	if len(specs) != 2 || specs[1].Provider != tunnel.Cloudflare {
		mu.Unlock()
		t.Fatalf("no new tunnel for the new setting: %+v", specs)
	}
	old := running[0]
	mu.Unlock()
	waitFor(t, "the old tunnel stopped", old.isStopped)

	// Another port forwards from another target, so the tunnel is made
	// again.
	port2 := freePort()
	set(func(c *web.Config) { c.Port = port2 })
	mu.Lock()
	target := specs[len(specs)-1].Target
	mu.Unlock()
	if target != "http://127.0.0.1:"+strconv.Itoa(port2) {
		t.Fatalf("the tunnel still targets %s", target)
	}
	if !d.webServing() {
		t.Fatal("web access stopped with its tunnel")
	}
}

// Tailscale already serving something else on 443 keeps web access's tunnel
// from replacing it: web access stays on, and the status says what to do.
func TestWebTunnelRefusesAPortTheUserServes(t *testing.T) {
	d, _ := startDaemon(t, time.Minute)
	undoBackoff := setWebTunnelBackoff(time.Hour)
	defer undoBackoff()
	undoStart := swapStartTunnel(func(ctx context.Context, s tunnel.Spec) (tunnel.Tunnel, error) {
		return nil, &tunnel.PortInUseError{Port: 443}
	})
	defer undoStart()
	d.webStore().SetConfig(func(c *web.Config) { c.Enabled, c.Port, c.Tunnel = true, freePort(), "tailscale" })
	st := reload(t, d)
	want := "Tailscale already serves another address on 443: free it, or use the Public URL"
	if !st.On || st.TunnelError != want {
		t.Fatalf("status %+v, want the error %q", st, want)
	}
}
