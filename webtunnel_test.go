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
	"omaterm/internal/tunnel"
	"omaterm/internal/web"
)

// tunnelPage renders the Web Access page with the tunnel state a test
// wants, on an app whose web access is on.
func tunnelPage(t *testing.T, state func(d *settingsData)) (*appState, *ui.Tester) {
	t.Helper()
	return wayPage(t, "web", state)
}

// wayPage renders a page of Web Access, the page itself or a way's, with
// the state a test wants, on an app whose web access is on and whose
// panes the session daemon runs.
func wayPage(t *testing.T, page string, state func(d *settingsData)) (*appState, *ui.Tester) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // no real daemon answers it
	daemonRuns(t, true)
	a, tr := newSettingsTester(t, page)
	a.webOn = true
	a.settingsUI.dataAt = time.Now() // the state set here, not the store's
	d := &a.settingsUI.data
	d.web = web.Config{Enabled: true}
	d.avail, d.availAllAt = map[tunnel.Provider]string{}, time.Now()
	if state != nil {
		state(d)
	}
	tr.SetSize(settingsWidth, 1600)
	tr.Frame()
	return a, tr
}

// daemonRuns has the pages drawn as with the session daemon running, or
// not, for the test.
func daemonRuns(t *testing.T, runs bool) {
	was := webDaemonRuns
	webDaemonRuns = func(*appState) bool { return runs }
	t.Cleanup(func() { webDaemonRuns = was })
}

// The page goes in three steps: turn it on, choose how the phone reaches
// it, pair the phone. With the daemon off, its switch comes first.
func TestSettingsWebSteps(t *testing.T) {
	_, tr := tunnelPage(t, nil)
	var last float32
	for _, s := range []string{"1. Turn it on", "Web access", "2. Choose how your phone reaches it",
		"Tailscale", "Tailscale Funnel", "Cloudflare", "Same Wi-Fi", "3. Pair your phone", "Pair a Device…", "Copy Link", "Paired devices", "Advanced"} {
		r, ok := tr.Find(s)
		if !ok {
			t.Fatalf("no %q in %v", s, tr.Texts())
		}
		if r.Y <= last {
			t.Fatalf("%q at %v, not under what comes before it", s, r)
		}
		last = r.Y
	}
	if tr.HasText("Keep sessions running when the window closes") {
		t.Fatal("the daemon's switch, with the daemon on")
	}

	t.Run("no daemon", func(t *testing.T) {
		_, tr := tunnelPage(t, nil)
		daemonRuns(t, false)
		tr.Frame()
		keep, ok := tr.Find("Keep sessions running when the window closes")
		on, _ := tr.Find("Web access")
		if !ok || keep.Y > on.Y {
			t.Fatalf("the daemon's switch at %v, web access at %v: %v", keep, on, tr.Texts())
		}
		if !tr.HasText("Needs the session daemon") {
			t.Fatalf("web access does not say why it is off: %v", tr.Texts())
		}
	})
}

// Each way's row says what became of it: in use, starting, not working,
// chosen while web access is off, or that it needs setting up here.
func TestSettingsWebWayStates(t *testing.T) {
	for _, c := range []struct {
		name  string
		off   bool
		state func(d *settingsData)
		pill  string
	}{
		{"running", false, func(d *settingsData) { d.web.Tunnel, d.webTunnelURL = "tailscale", "https://box.tailnet.ts.net" }, "In use"},
		{"starting", false, func(d *settingsData) { d.web.Tunnel = "cloudflare" }, "Starting…"},
		{"failed", false, func(d *settingsData) { d.web.Tunnel, d.webTunnelErr = "cloudflare", "cloudflared: exit status 1" }, "Not working"},
		{"unavailable", false, func(d *settingsData) { d.web.Tunnel, d.webTunnelWhy = "tailscale", "tailscale is not installed" }, "Not working"},
		{"off", true, func(d *settingsData) { d.web.Tunnel = "funnel" }, "Chosen"},
		{"missing", false, func(d *settingsData) { d.avail[tunnel.Cloudflare] = "cloudflared is not installed" }, "Needs setup"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, tr := tunnelPage(t, c.state)
			a.webOn = !c.off
			tr.Frame()
			if !tr.HasText(c.pill) {
				t.Fatalf("no %q in %v", c.pill, tr.Texts())
			}
		})
	}
	t.Run("network", func(t *testing.T) {
		a, tr := tunnelPage(t, nil)
		a.webNetwork = true
		tr.Frame()
		wifi, _ := tr.Find("Same Wi-Fi")
		pill, ok := tr.Find("In use")
		if !ok || pill.Y < wifi.Y-10 || pill.Y > wifi.Y+30 {
			t.Fatalf("the pill at %v, Same Wi-Fi at %v", pill, wifi)
		}
	})
}

// A way's page says what became of it, with its link, why it fails or
// the link that turns it on, and what it needs.
func TestSettingsWebWayPages(t *testing.T) {
	for _, c := range []struct {
		name, page string
		state      func(d *settingsData)
		has, not   []string
	}{
		{
			name: "running", page: "web-tailscale",
			state: func(d *settingsData) { d.web.Tunnel, d.webTunnelURL = "tailscale", "https://box.tailnet.ts.net" },
			has:   []string{"In use", "https://box.tailnet.ts.net", "Copy Link", "Stop Using", "Tailscale on this computer", "Tailscale on your phone", "HTTPS on your tailnet"},
			not:   []string{"Use Tailscale"},
		},
		{
			name: "not in use", page: "web-tailscale",
			has: []string{"Not in use", "Use Tailscale", "Get the App", "Open Admin Console"},
			not: []string{"Stop Using", "Get Tailscale"},
		},
		{
			name: "missing", page: "web-tailscale",
			state: func(d *settingsData) { d.avail[tunnel.Tailscale] = "tailscale is not installed" },
			has:   []string{"tailscale is not installed", "Get Tailscale", "Get what it needs, below, first."},
		},
		{
			name: "failed", page: "web-cloudflare",
			state: func(d *settingsData) { d.web.Tunnel, d.webTunnelErr = "cloudflare", "cloudflared: exit status 1" },
			has:   []string{"Not working", "cloudflared: exit status 1", "Stop Using"},
		},
		{
			name: "funnel off", page: "web-funnel",
			state: func(d *settingsData) {
				d.web.Tunnel = "funnel"
				d.webTunnelErr = "Turn on Funnel for this tailnet: https://login.tailscale.com/f/funnel?node=n1"
			},
			has: []string{"Turn on Funnel for this tailnet:\nhttps://login.tailscale.com/f/funnel?node=n1", "Funnel turned on", "Turn On Funnel…"},
			not: []string{"Tailscale on your phone"},
		},
		{
			name: "quick", page: "web-cloudflare",
			state: func(d *settingsData) {
				d.web.Tunnel, d.webTunnelURL = "cloudflare", "https://some-words-here.trycloudflare.com"
			},
			has: []string{"https://some-words-here.trycloudflare.com", "The link changes each time the tunnel starts again.",
				"1. Your domain on Cloudflare", "2. Create a tunnel", "3. Give it a public hostname", "http://localhost:7370",
				"4. Your domain", "5. The tunnel’s token", "Without them, the link is a quick one"},
		},
		{
			name: "own domain", page: "web-cloudflare",
			state: func(d *settingsData) {
				d.web.Tunnel = "cloudflare"
				d.web.CloudflareHost, d.cfHost, d.webTokenSaved = "term.example.com", "term.example.com", true
				d.webTunnelURL = "https://term.example.com"
			},
			has: []string{"https://term.example.com", "The link is https://term.example.com, and stays the same."},
			not: []string{"The link changes each time the tunnel starts again."},
		},
		{
			name: "in place of another", page: "web-funnel",
			state: func(d *settingsData) { d.web.Tunnel = "cloudflare" },
			has:   []string{"In place of Cloudflare, which web access uses now.", "Use Tailscale Funnel"},
		},
		{
			name: "wifi", page: "web-wifi",
			has: []string{"Use Same Wi-Fi", "The phone on the same network"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, tr := wayPage(t, c.page, c.state)
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

// Use on a way's page saves its choice, turning web access on, and Stop
// Using takes it back. The session daemon runs the panes as far as the
// choice is concerned, and answers the reload that follows from its own
// store, which here has nothing enabled — so no tunnel is run.
func TestSettingsWebWayUses(t *testing.T) {
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

	a, tr := newSettingsTester(t, "web-tailscale")
	a.setPanes(r) // web access needs the daemon
	defer stopTermPanes(a)
	a.settingsUI.data.avail, a.settingsUI.data.availAllAt = map[tunnel.Provider]string{}, time.Now() // whatever this machine has
	tr.Frame()
	if err := tr.Click("Use Tailscale"); err != nil {
		t.Fatalf("%v: %v", err, tr.Texts())
	}
	tr.Frame()
	if cfg, err := webStore().Config(); err != nil || cfg.Tunnel != "tailscale" || !cfg.Enabled {
		t.Fatalf("the choice was not saved: %+v %v", cfg, err)
	}
	a.settingsUI.dataAt = time.Time{} // read the store again
	tr.Frame()
	if err := tr.Click("Stop Using"); err != nil {
		t.Fatalf("%v: %v", err, tr.Texts())
	}
	tr.Frame()
	if cfg, _ := webStore().Config(); cfg.Tunnel != "" {
		t.Fatalf("still chosen: %q", cfg.Tunnel)
	}
}

// Without the session daemon a way's page says so, and offers no Use
// that would only fail.
func TestSettingsWebWayWithoutDaemon(t *testing.T) {
	_, tr := wayPage(t, "web-funnel", nil)
	daemonRuns(t, false)
	tr.Frame()
	if !tr.HasText("Web access needs the session daemon") {
		t.Fatalf("no why in %v", tr.Texts())
	}
}

// TestWebTunnelShots draws the Web Access page and its ways' pages, dark
// and light, for a look before it is merged:
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
			name, page string
			state      func(d *settingsData)
		}{
			{"steps", "web", nil},
			{"in-use", "web", func(d *settingsData) {
				d.web.Tunnel = "tailscale"
				d.webTunnelURL, d.webLink = "https://box.tailnet.ts.net", "https://box.tailnet.ts.net"
				now := time.Now().UnixMilli()
				d.devices = []web.Device{{ID: "d1", Name: "iPhone · Safari", Created: now, LastSeen: now}}
			}},
			{"tailscale", "web-tailscale", func(d *settingsData) {
				d.web.Tunnel = "tailscale"
				d.webTunnelURL = "https://box.tailnet.ts.net"
			}},
			{"funnel-off", "web-funnel", func(d *settingsData) {
				d.web.Tunnel = "funnel"
				d.webTunnelErr = "Turn on Funnel for this tailnet: https://login.tailscale.com/f/funnel?node=nABC123DEF"
			}},
			{"cloudflare-missing", "web-cloudflare", func(d *settingsData) {
				d.avail[tunnel.Cloudflare] = "cloudflared is not installed"
			}},
			{"cloudflare-domain", "web-cloudflare", func(d *settingsData) {
				d.web.Tunnel = "cloudflare"
				d.web.CloudflareHost, d.cfHost, d.webTokenSaved = "term.example.com", "term.example.com", true
				d.webTunnelURL = "https://term.example.com"
			}},
			{"wifi", "web-wifi", nil},
		} {
			a, tr := wayPage(t, c.page, c.state)
			a.setThemeMode(mode)
			tr.Frame()
			tr.Frame()
			f, err := os.Create(filepath.Join(dir, "settings-web-"+mode+"-"+c.name+".png"))
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
	a, tr := newSettingsTester(t, "web-cloudflare")
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
