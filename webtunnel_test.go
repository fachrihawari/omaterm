package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

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
		name  string
		state func(d *settingsData)
		has   []string
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
	} {
		t.Run(c.name, func(t *testing.T) {
			_, tr := tunnelPage(t, c.state)
			for _, has := range c.has {
				if !tr.HasText(has) {
					t.Errorf("no %q in %v", has, tr.Texts())
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // its web.json, not the run's
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
// in the light palette, for a look before it is merged:
// SETTINGS_SHOTS=dir go test -run TestWebTunnelShots .
func TestWebTunnelShots(t *testing.T) {
	dir := os.Getenv("SETTINGS_SHOTS")
	if dir == "" {
		t.Skip("set SETTINGS_SHOTS to a directory")
	}
	os.MkdirAll(dir, 0o755)
	defer setPalette(theme.BuiltinDark())
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
	} {
		a, tr := tunnelPage(t, c.state)
		a.setThemeMode("light")
		tr.Frame()
		tr.Frame()
		f, err := os.Create(filepath.Join(dir, "settings-web-tunnel-"+c.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, tr.Image())
		f.Close()
		stopTermPanes(a)
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
}
