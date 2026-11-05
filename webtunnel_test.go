package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/daemon"
	"omaterm/internal/paths"
	"omaterm/internal/theme"
	"omaterm/internal/web"
)

// tunnelPage renders the Web Access page with the tunnel state a test
// wants, on an app whose web access is on.
func tunnelPage(t *testing.T, state func(d *settingsData)) (*appState, *ui.Tester) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // no real daemon answers it
	a, tr := newSettingsTester(t, "web")
	a.webOn = true
	d := &a.settingsUI.data
	d.web = web.Config{Enabled: true}
	if state != nil {
		state(d)
	}
	tr.Frame()
	return a, tr
}

// The Reach It from Elsewhere row shows the popup's choice, and what
// became of it: the live URL, why it isn't running, why it couldn't run
// here at all, or that it is starting.
func TestSettingsWebTunnelStates(t *testing.T) {
	for _, c := range []struct {
		name     string
		state    func(d *settingsData)
		has, not []string
	}{
		{
			name:  "off",
			state: func(d *settingsData) { d.web.Tunnel = "" },
			has:   []string{"Reach it from elsewhere", "Off"},
		},
		{
			name: "running",
			state: func(d *settingsData) {
				d.web.Tunnel = "tailscale"
				d.webTunnelURL = "https://box.tailnet.ts.net"
			},
			has: []string{"Reach it from elsewhere", "Tailscale (your devices)", "https://box.tailnet.ts.net", "Copy"},
		},
		{
			name: "failed",
			state: func(d *settingsData) {
				d.web.Tunnel = "cloudflare"
				d.webTunnelErr = "cloudflared: exit status 1"
			},
			has: []string{"Cloudflare (public link)", "cloudflared: exit status 1"},
		},
		{
			name: "unavailable",
			state: func(d *settingsData) {
				d.web.Tunnel = "tailscale"
				d.webTunnelWhy = "tailscale is not installed"
			},
			has: []string{"Tailscale (your devices)", "tailscale is not installed"},
		},
		{
			name:  "starting",
			state: func(d *settingsData) { d.web.Tunnel = "cloudflare" },
			has:   []string{"Cloudflare (public link)", "Starting…"},
		},
		{
			name: "funnel",
			state: func(d *settingsData) {
				d.web.Tunnel = "funnel"
				d.webTunnelURL = "https://box.tailnet.ts.net"
			},
			has: []string{"Tailscale Funnel (public link)", "https://box.tailnet.ts.net", "Copy"},
			not: []string{"Your domain", "changes each time"},
		},
		{
			name: "quick",
			state: func(d *settingsData) {
				d.web.Tunnel = "cloudflare"
				d.webTunnelURL = "https://some-words-here.trycloudflare.com"
			},
			has: []string{"https://some-words-here.trycloudflare.com", "The link changes each time the tunnel starts again.",
				"Your domain", "Tunnel token", "Zero Trust › Networks › Tunnels › Create a tunnel"},
		},
		{
			name: "own domain",
			state: func(d *settingsData) {
				d.web.Tunnel = "cloudflare"
				d.web.CloudflareHost, d.cfHost, d.webTokenSaved = "term.example.com", "term.example.com", true
				d.webTunnelURL = "https://term.example.com"
			},
			has: []string{"https://term.example.com", "Your domain", "Tunnel token"},
			not: []string{"The link changes each time the tunnel starts again."},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, tr := tunnelPage(t, c.state)
			for _, has := range c.has {
				if !tr.HasText(has) {
					t.Errorf("no %q in %v", has, tr.Texts())
				}
			}
			for _, not := range c.not {
				if tr.HasText(not) {
					t.Errorf("%q in %v", not, tr.Texts())
				}
			}
		})
	}
}

// Choosing a way to reach web access from elsewhere saves it, turning
// web access on. The session daemon runs the panes as far as the choice
// is concerned, and answers the reload that follows from its own store,
// which here has nothing enabled — so no tunnel is run.
func TestSettingsWebTunnelChooses(t *testing.T) {
	runtime := t.TempDir() // the daemon's socket, and nothing else's
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	setConfigHome(t, t.TempDir()) // its web.json, not the run's
	dir := t.TempDir()
	d, err := daemon.Start(daemon.Options{Socket: paths.DaemonSocketPath(), StateDir: dir,
		StateFile: filepath.Join(dir, "state.json"), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	r, err := daemon.Dial(paths.DaemonSocketPath())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	a, tr := newSettingsTester(t, "web")
	a.setPanes(r) // web access needs the daemon
	a.webOn = true
	tr.Frame()
	if err := tr.Click("Off"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if err := tr.Click("Tailscale (your devices)"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	tr.Frame()
	cfg, err := webStore().Config()
	if err != nil || cfg.Tunnel != "tailscale" || !cfg.Enabled {
		t.Fatalf("the choice was not saved: %+v %v", cfg, err)
	}
	stopTermPanes(a)
}

// Turning web access on while a tunnel is chosen does not turn it off
// again when the daemon has nothing to say about it: the row keeps the
// choice, with the daemon gone.
func TestSettingsWebTunnelWithoutDaemon(t *testing.T) {
	a, tr := tunnelPage(t, func(d *settingsData) {
		d.web.Tunnel = "tailscale"
		d.webTunnelWhy = "Tailscale is signed out"
	})
	if !tr.HasText("Tailscale is signed out") {
		t.Fatalf("no why in %v", tr.Texts())
	}
	stopTermPanes(a)
}

// TestWebTunnelShots draws the Web Access page with each tunnel state,
// dark and light, for a look before it is merged:
// SETTINGS_SHOTS=dir go test -run TestWebTunnelShots .
func TestWebTunnelShots(t *testing.T) {
	dir := os.Getenv("SETTINGS_SHOTS")
	if dir == "" {
		t.Skip("set SETTINGS_SHOTS to a directory")
	}
	os.MkdirAll(dir, 0o755)
	defer setPalette(theme.BuiltinDark())
	for _, mode := range []string{"dark", "light"} {
		for _, c := range []struct {
			name  string
			state func(d *settingsData)
		}{
			{"off", func(d *settingsData) { d.web.Tunnel = "" }},
			{"running", func(d *settingsData) {
				d.web.Tunnel = "tailscale"
				d.webTunnelURL = "https://box.tailnet.ts.net"
				d.webLink = "https://box.tailnet.ts.net"
			}},
			{"failed", func(d *settingsData) {
				d.web.Tunnel = "cloudflare"
				d.webTunnelErr = "cloudflared: exit status 1"
			}},
			{"cloudflare-quick", func(d *settingsData) {
				d.web.Tunnel = "cloudflare"
				d.webTunnelURL = "https://some-words-here.trycloudflare.com"
				d.webLink = d.webTunnelURL
			}},
			{"cloudflare-domain", func(d *settingsData) {
				d.web.Tunnel = "cloudflare"
				d.web.CloudflareHost, d.cfHost, d.webTokenSaved = "term.example.com", "term.example.com", true
				d.webTunnelURL = "https://term.example.com"
				d.webLink = d.webTunnelURL
			}},
			{"funnel-off", func(d *settingsData) {
				d.web.Tunnel = "funnel"
				d.webTunnelErr = "Turn on Funnel for this tailnet: https://login.tailscale.com/f/funnel?node=nABC123DEF"
			}},
			{"funnel", func(d *settingsData) {
				d.web.Tunnel = "funnel"
				d.webTunnelURL = "https://box.tailnet.ts.net"
				d.webLink = d.webTunnelURL
			}},
		} {
			a, tr := tunnelPage(t, c.state)
			a.setThemeMode(mode)
			tr.Frame()
			tr.Frame()
			f, err := os.Create(filepath.Join(dir, "settings-web-tunnel-"+mode+"-"+c.name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, tr.Image())
			f.Close()
			stopTermPanes(a)
		}
	}
}

// The tunnel's note follows the daemon's answer rather than the
// setting alone: a tunnel set but not yet answered reads as starting
// only while web access is on.
func TestWebTunnelNote(t *testing.T) {
	a := newTestApp(t)
	a.webOn = false // whatever another test left in the store
	d := &a.settingsUI.data
	if got := a.webTunnelNote(); got != "" {
		t.Fatalf("no tunnel: %q", got)
	}
	d.web.Tunnel = "cloudflare"
	if got := a.webTunnelNote(); got != "" {
		t.Fatalf("off and nothing to say: %q", got)
	}
	a.webOn = true
	if got := a.webTunnelNote(); got != "Starting…" {
		t.Fatalf("starting: %q", got)
	}
	d.webTunnelWhy = "cloudflared is not installed"
	if got := a.webTunnelNote(); got != "cloudflared is not installed" {
		t.Fatalf("why: %q", got)
	}
	d.webTunnelErr = "cloudflared: exit status 1"
	if got := a.webTunnelNote(); got != "cloudflared: exit status 1" {
		t.Fatalf("err: %q", got)
	}
	d.webTunnelURL = "https://box.tailnet.ts.net"
	if got := a.webTunnelNote(); got != "https://box.tailnet.ts.net" {
		t.Fatalf("url: %q", got)
	}
	// The link that turns Funnel on goes on a line of its own.
	d.web.Tunnel, d.webTunnelURL = "funnel", ""
	d.webTunnelErr = "Turn on Funnel for this tailnet: https://login.tailscale.com/f/funnel?node=n1"
	if got := a.webTunnelNote(); got != "Turn on Funnel for this tailnet:\nhttps://login.tailscale.com/f/funnel?node=n1" {
		t.Fatalf("funnel off: %q", got)
	}
}

// A quick tunnel's link is said to change; the user's own domain's, with
// its token, is not.
func TestWebTunnelCaveat(t *testing.T) {
	a := newTestApp(t)
	d := &a.settingsUI.data
	d.web.Tunnel, d.webTunnelURL = "cloudflare", "https://some-words-here.trycloudflare.com"
	if got := a.webTunnelCaveat(); got == "" {
		t.Fatal("a quick tunnel's link is not said to change")
	}
	d.web.CloudflareHost = "term.example.com" // no token yet: still a quick tunnel
	if got := a.webTunnelCaveat(); got == "" {
		t.Fatal("a domain without its token is not said to change")
	}
	d.webTokenSaved, d.webTunnelURL = true, "https://term.example.com"
	if got := a.webTunnelCaveat(); got != "" {
		t.Fatalf("the user's own domain: %q", got)
	}
	d.web.Tunnel, d.webTunnelURL = "funnel", "https://box.tailnet.ts.net"
	if got := a.webTunnelCaveat(); got != "" {
		t.Fatalf("funnel: %q", got)
	}
}

// The domain and token typed in Settings are saved, the domain from its
// URL pasted whole; the token leaves the field, and no text of the page
// shows it. Clearing the domain forgets the token too.
func TestSettingsWebCloudflareSaves(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // no real daemon answers it
	setConfigHome(t, t.TempDir())
	// Off, so the reload that fails without a daemon leaves it as it is.
	if _, err := webStore().SetConfig(func(c *web.Config) { c.Tunnel = "cloudflare" }); err != nil {
		t.Fatal(err)
	}
	const token = "eyJhIjoiYWNjb3VudCIsInQiOiJ0dW5uZWwiLCJzIjoic2VjcmV0In0="
	a, tr := newSettingsTester(t, "web")
	defer stopTermPanes(a)
	tr.SetSize(settingsWidth, 1600)
	tr.Frame()
	if err := tr.Click("webCfHost"); err != nil {
		t.Fatalf("%v: %v", err, tr.Texts())
	}
	tr.Frame()
	tr.Type("https://term.example.com/")
	tr.Key(0, ui.KeyEnter)
	tr.Frame()
	if cfg, _ := webStore().Config(); cfg.CloudflareHost != "term.example.com" {
		t.Fatalf("the domain saved as %q", cfg.CloudflareHost)
	}
	if d := a.settingsUI.data; d.cfHost != "term.example.com" {
		t.Fatalf("the field shows %q", d.cfHost)
	}

	if err := tr.Click("webCfToken"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	tr.Type(token)
	tr.Key(0, ui.KeyEnter)
	tr.Frame()
	if cfg, _ := webStore().Config(); cfg.CloudflareToken != token {
		t.Fatal("the token was not saved")
	}
	a.settingsUI.dataAt = time.Time{} // read the store again
	tr.Frame()
	d := a.settingsUI.data
	if d.cfToken != "" || d.web.CloudflareToken != "" || !d.webTokenSaved {
		t.Fatalf("the page holds the token: field %q, config %q, saved %v", d.cfToken, d.web.CloudflareToken, d.webTokenSaved)
	}
	for _, s := range tr.Texts() {
		if strings.Contains(s, token) {
			t.Fatalf("the page shows the token: %q", s)
		}
	}

	if err := tr.Click("webCfHost"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	tr.Key(0, ui.KeyEnd)
	for range len("term.example.com") {
		tr.Key(0, ui.KeyBackspace)
	}
	tr.Key(0, ui.KeyEnter)
	tr.Frame()
	if cfg, _ := webStore().Config(); cfg.CloudflareHost != "" || cfg.CloudflareToken != "" || cfg.Tunnel != "cloudflare" {
		t.Fatalf("not back to a quick tunnel: host %q, token kept %v, tunnel %q", cfg.CloudflareHost, cfg.CloudflareToken != "", cfg.Tunnel)
	}
}
