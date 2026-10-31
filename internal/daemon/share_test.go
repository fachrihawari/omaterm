//go:build linux || darwin

package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/procs"
	"omaterm/internal/proto"
	"omaterm/internal/tunnel"
)

// sharer fakes the tunnels a share starts, and keeps what it was asked.
type sharer struct {
	mu      sync.Mutex
	specs   []tunnel.Spec
	tunnels []*fakeTunnel
	fail    error
}

func (s *sharer) start(_ context.Context, spec tunnel.Spec) (tunnel.Tunnel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.specs = append(s.specs, spec)
	if s.fail != nil {
		return nil, s.fail
	}
	t := &fakeTunnel{url: "https://" + spec.Name + "-" + strconv.Itoa(len(s.specs)) + ".example.net", done: make(chan struct{})}
	s.tunnels = append(s.tunnels, t)
	return t, nil
}

func (s *sharer) last() (tunnel.Spec, *fakeTunnel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.specs) == 0 {
		return tunnel.Spec{}, nil
	}
	return s.specs[len(s.specs)-1], s.tunnels[len(s.tunnels)-1]
}

func (s *sharer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.specs)
}

// shareFixture is a daemon with a project whose process "web" has a url and
// runs the test binary as its server, trusted, and a client to it.
func shareFixture(t *testing.T) (*Daemon, *client.Client, *sharer) {
	t.Helper()
	d, socket, projectDir := offlineDaemon(t, false, nil)
	d.opts.Notifier = func(string, string, string) {}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writeProcesses(t, projectDir, "processes:\n  web:\n    command: OMATERM_TEST_HTTP=1 exec "+exe+"\n    url: shop\n  tool:\n    command: sleep 30\n")
	c := processClient(t, socket)
	call[[]proto.Process](t, c, "process.sync", proto.ProcessParams{Project: "p1"})
	for _, name := range []string{"web", "tool"} {
		call[proto.Process](t, c, "process.trust", proto.ProcessParams{Project: "p1", Name: name})
	}
	s := &sharer{}
	t.Cleanup(swapStartTunnel(s.start))
	return d, c, s
}

func procState(t *testing.T, c *client.Client, name string) proto.Process {
	t.Helper()
	return procByName(call[[]proto.Process](t, c, "process.list", proto.ProcessParams{Project: "p1"}), name)
}

func startWeb(t *testing.T, c *client.Client) proto.Process {
	t.Helper()
	call[proto.Process](t, c, "process.start", proto.ProcessParams{Project: "p1", Name: "web"})
	var p proto.Process
	waitFor(t, "web running", func() bool {
		p = procState(t, c, "web")
		return p.Status == procs.Running
	})
	return p
}

func shareParams(name, via string) proto.ProcessParams {
	return proto.ProcessParams{Project: "p1", Name: name, Via: via}
}

func TestShareReachesTheProcessPortAndShowsInItsState(t *testing.T) {
	_, c, s := shareFixture(t)
	web := startWeb(t, c)
	got := call[proto.Process](t, c, "process.share", shareParams("web", "tailscale"))
	spec, tun := s.last()
	if want := "http://127.0.0.1:" + strconv.Itoa(web.Port); spec.Target != want || spec.Provider != tunnel.Tailscale || spec.Name != "web" || spec.Port != 8443 {
		t.Fatalf("spec %+v, want target %s, tailscale, port 8443", spec, want)
	}
	if got.Share != tun.URL() || got.ShareVia != "tailscale" {
		t.Fatalf("process %q via %q, want %q", got.Share, got.ShareVia, tun.URL())
	}
	// The state's session carries it, and so does the list.
	for _, sn := range call[proto.State](t, c, "state", nil).Sessions {
		if sn.Process == "web" && (sn.Proc == nil || sn.Proc.Share != tun.URL()) {
			t.Fatalf("the state's process: %+v", sn.Proc)
		}
	}
	if p := procState(t, c, "web"); p.Share != tun.URL() {
		t.Fatalf("list: %+v", p)
	}
}

func TestShareFollowsTheProcess(t *testing.T) {
	_, c, s := shareFixture(t)
	startWeb(t, c)
	call[proto.Process](t, c, "process.share", shareParams("web", "cloudflare"))
	_, first := s.last()

	// A stop ends the tunnel and the share's address, not the share.
	call[proto.Process](t, c, "process.stop", proto.ProcessParams{Project: "p1", Name: "web"})
	waitFor(t, "the tunnel to stop", first.isStopped)
	waitFor(t, "the address to go", func() bool { return procState(t, c, "web").Share == "" })

	// It comes back on the port the process has then.
	web := startWeb(t, c)
	waitFor(t, "the share to start again", func() bool { return s.count() == 2 })
	spec, second := s.last()
	if want := "http://127.0.0.1:" + strconv.Itoa(web.Port); spec.Target != want || spec.Provider != tunnel.Cloudflare {
		t.Fatalf("second spec %+v, want target %s", spec, want)
	}
	waitFor(t, "the new address", func() bool { return procState(t, c, "web").Share == second.URL() })

	// A restart is a new port: the share moves to it.
	call[proto.Process](t, c, "process.restart", proto.ProcessParams{Project: "p1", Name: "web"})
	waitFor(t, "the second tunnel to stop", second.isStopped)
	waitFor(t, "a third tunnel", func() bool { return s.count() == 3 })
	var web2 proto.Process
	waitFor(t, "running again", func() bool {
		web2 = procState(t, c, "web")
		return web2.Status == procs.Running
	})
	waitFor(t, "the third tunnel on the new port", func() bool {
		spec, _ := s.last()
		return spec.Target == "http://127.0.0.1:"+strconv.Itoa(web2.Port)
	})
}

func TestShareOfAProcessThatIsNotUpWaitsForIt(t *testing.T) {
	_, c, s := shareFixture(t)
	got := call[proto.Process](t, c, "process.share", shareParams("web", "tailscale"))
	if got.Share != "" || s.count() != 0 {
		t.Fatalf("shared a process that isn't running: %+v, %d tunnels", got, s.count())
	}
	startWeb(t, c)
	waitFor(t, "the share once it runs", func() bool { return procState(t, c, "web").Share != "" })
}

func TestShareNeedsAPort(t *testing.T) {
	_, c, s := shareFixture(t)
	err := c.Call("process.share", shareParams("tool", "tailscale"), nil)
	if err == nil || !strings.Contains(err.Error(), "no port to share") || s.count() != 0 {
		t.Fatalf("error %v, %d tunnels", err, s.count())
	}
	if err := c.Call("process.share", shareParams("web", "ngrok"), nil); err == nil {
		t.Fatal("an unknown provider was accepted")
	}
	if err := c.Call("process.share", shareParams("nope", "tailscale"), nil); err == nil {
		t.Fatal("an unknown process was shared")
	}
}

func TestShareThatFailsSaysSoOnTheProcess(t *testing.T) {
	_, c, s := shareFixture(t)
	startWeb(t, c)
	s.fail = errors.New("boom")
	err := c.Call("process.share", shareParams("web", "cloudflare"), nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error %v", err)
	}
	p := procState(t, c, "web")
	if p.Share != "" || p.Message != "sharing failed: boom" {
		t.Fatalf("process %+v", p)
	}
	// It cleared: a restart doesn't try again.
	n := s.count()
	call[proto.Process](t, c, "process.restart", proto.ProcessParams{Project: "p1", Name: "web"})
	waitFor(t, "running", func() bool { return procState(t, c, "web").Status == procs.Running })
	time.Sleep(200 * time.Millisecond)
	if s.count() != n {
		t.Fatal("a failed share was tried again")
	}
	// Unsharing forgets the note.
	call[proto.Process](t, c, "process.unshare", proto.ProcessParams{Project: "p1", Name: "web"})
	if p := procState(t, c, "web"); p.Message != "" {
		t.Fatalf("the note stayed: %q", p.Message)
	}
}

func TestUnshareEndsTheTunnelForGood(t *testing.T) {
	_, c, s := shareFixture(t)
	startWeb(t, c)
	call[proto.Process](t, c, "process.share", shareParams("web", "tailscale"))
	_, tun := s.last()
	call[proto.Process](t, c, "process.unshare", proto.ProcessParams{Project: "p1", Name: "web"})
	if !tun.isStopped() || procState(t, c, "web").Share != "" {
		t.Fatalf("still shared: stopped %v, %+v", tun.isStopped(), procState(t, c, "web"))
	}
	call[proto.Process](t, c, "process.restart", proto.ProcessParams{Project: "p1", Name: "web"})
	waitFor(t, "running", func() bool { return procState(t, c, "web").Status == procs.Running })
	time.Sleep(200 * time.Millisecond)
	if s.count() != 1 {
		t.Fatal("an unshared process was shared again")
	}
}

func TestShareEndsWhenTheTunnelDies(t *testing.T) {
	_, c, s := shareFixture(t)
	startWeb(t, c)
	call[proto.Process](t, c, "process.share", shareParams("web", "cloudflare"))
	_, tun := s.last()
	tun.fail = errors.New("connection lost")
	tun.end()
	waitFor(t, "the note", func() bool { return strings.Contains(procState(t, c, "web").Message, "sharing ended: connection lost") })
	if p := procState(t, c, "web"); p.Share != "" {
		t.Fatalf("still shared: %+v", p)
	}
}

func TestSharesStopWithTheDaemonAndTakeOwnTailscalePorts(t *testing.T) {
	d, c, s := shareFixture(t)
	startWeb(t, c)
	call[proto.Process](t, c, "process.share", shareParams("web", "tailscale"))
	_, tun := s.last()
	d.Stop()
	if !tun.isStopped() {
		t.Fatal("the share outlived the daemon")
	}
	d.shareMu.Lock()
	d.shares = map[procKey]*share{{"p1", "a"}: {tsPort: 8443}, {"p1", "b"}: {tsPort: 10000}}
	got := d.freeSharePortLocked()
	d.shareMu.Unlock()
	if got != 10001 {
		t.Fatalf("free port %d, want 10001", got)
	}
}

// With cloudflared a script on the PATH, as the tunnel package's tests have
// it: the real tunnel code runs, no real tool.
func TestShareThroughAFakeCloudflared(t *testing.T) {
	_, c, _ := shareFixture(t)
	t.Cleanup(swapStartTunnel(tunnel.Start))
	startWeb(t, c)
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"INF |  https://some-words-here.trycloudflare.com |\" >&2\nexec /bin/sleep 300\n"
	if err := os.WriteFile(filepath.Join(dir, "cloudflared"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got := call[proto.Process](t, c, "process.share", shareParams("web", "cloudflare"))
	if got.Share != "https://some-words-here.trycloudflare.com" || got.ShareVia != "cloudflare" {
		t.Fatalf("process %+v", got)
	}
	call[proto.Process](t, c, "process.unshare", proto.ProcessParams{Project: "p1", Name: "web"})
	if p := procState(t, c, "web"); p.Share != "" {
		t.Fatalf("after unshare: %+v", p)
	}
}
