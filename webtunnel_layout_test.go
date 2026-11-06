package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// offPage fails the test for any text off the page.
func offPage(t *testing.T, tr *ui.Tester) {
	t.Helper()
	for _, s := range tr.Texts() {
		if r, _ := tr.Find(s); r.X < 0 || r.X+r.W > settingsWidth {
			t.Errorf("%q off the page at %v", s, r)
		}
	}
}

// The ways keep the settings' shape: their titles in one column, each
// one's line under it, its pill at the right, and nothing off the page.
func TestSettingsWebWaysLayout(t *testing.T) {
	_, tr := tunnelPage(t, func(d *settingsData) {
		d.web.Tunnel, d.webTunnelURL = "tailscale", "https://box.tailnet.ts.net"
	})
	find := func(s string) ui.Rect {
		t.Helper()
		r, ok := tr.Find(s)
		if !ok {
			t.Fatalf("no %q in %v", s, tr.Texts())
		}
		return r
	}
	// A way's row is a button named after it, the whole row.
	first := find(webWays[0].sub)
	var last ui.Rect
	for _, w := range webWays {
		row, sub := find(w.title), find(w.sub)
		if sub.X != first.X || sub.Y < row.Y || sub.Y+sub.H > row.Y+row.H || row.Y < last.Y+last.H {
			t.Errorf("%s: its row at %v, its line at %v, the first line at %v", w.title, row, sub, first)
		}
		last = row
	}
	row, pill := find("Tailscale"), find("In use")
	if pill.X < settingsWidth/2 || pill.Y < row.Y || pill.Y+pill.H > row.Y+row.H {
		t.Fatalf("the pill at %v, Tailscale's row at %v", pill, row)
	}
	offPage(t, tr)
}

// A way that fails says why under its page's title, not over its
// buttons.
func TestSettingsWebWayLayoutFailed(t *testing.T) {
	_, tr := wayPage(t, "web-cloudflare", func(d *settingsData) {
		d.web.Tunnel, d.webTunnelErr = "cloudflare", "cloudflared: exit status 1"
	})
	title, _ := tr.Find("Not working")
	line, ok := tr.Find("cloudflared: exit status 1")
	if !ok || line.X != title.X || line.Y < title.Y || line.Y > title.Y+40 {
		t.Fatalf("the failure line at %v, the title at %v", line, title)
	}
	stop, ok := tr.Find("Stop Using")
	if !ok || stop.X < line.X+line.W {
		t.Fatalf("Stop Using at %v, the line at %v", stop, line)
	}
	offPage(t, tr)
}

// Cloudflare's page goes in its steps' order: what it needs, then the
// five steps of one's own domain, their titles in one column, the
// buttons and fields at the right, ending together, and nothing off the
// page.
func TestSettingsWebCloudflareLayout(t *testing.T) {
	_, tr := wayPage(t, "web-cloudflare", func(d *settingsData) {
		d.web.Tunnel = "cloudflare"
		d.web.CloudflareHost, d.cfHost, d.webTokenSaved = "term.example.com", "term.example.com", true
		d.webTunnelURL = "https://term.example.com"
	})
	find := func(s string) ui.Rect {
		t.Helper()
		r, ok := tr.Find(s)
		if !ok {
			t.Fatalf("no %q in %v", s, tr.Texts())
		}
		return r
	}
	var last ui.Rect
	steps := []string{"What it needs", "cloudflared on this computer", "To keep the link: your own domain",
		"1. Your domain on Cloudflare", "2. Create a tunnel", "3. Give it a public hostname", "4. Your domain", "5. The tunnel’s token"}
	for i, s := range steps {
		r := find(s)
		if r.Y <= last.Y {
			t.Fatalf("%q at %v, over what comes before it (%v)", s, r, last)
		}
		if i > 3 && r.X != last.X {
			t.Errorf("%q at %v, out of line with the step before (%v)", s, r, last)
		}
		last = r
	}
	end := func(r ui.Rect) float32 { return r.X + r.W }
	open := find("Open Cloudflare")
	for _, s := range []string{"Open Zero Trust", "Copy Service", "webCfHost", "webCfToken"} {
		if r := find(s); end(r)-end(open) > 1 || end(open)-end(r) > 14 {
			t.Errorf("%s ends at %v, Open Cloudflare at %v", s, end(r), end(open))
		}
	}
	offPage(t, tr)
}

// Funnel the tailnet hasn't turned on says so with the link that does,
// on a line of its own under the title, and its needs offer to open it.
func TestSettingsWebFunnelOffLayout(t *testing.T) {
	const link = "https://login.tailscale.com/f/funnel?node=nABC123DEF"
	_, tr := wayPage(t, "web-funnel", func(d *settingsData) {
		d.web.Tunnel = "funnel"
		d.webTunnelErr = "Turn on Funnel for this tailnet: " + link
	})
	title, _ := tr.Find("Not working")
	line, ok := tr.Find("Turn on Funnel for this tailnet:\n" + link)
	if !ok || line.X != title.X || line.Y < title.Y || line.Y > title.Y+40 {
		t.Fatalf("the line at %v, the title at %v: %v", line, title, tr.Texts())
	}
	need, _ := tr.Find("Funnel turned on")
	turn, ok := tr.Find("Turn On Funnel…")
	if !ok || turn.X < settingsWidth/2 || turn.Y < need.Y-10 || turn.Y > need.Y+40 {
		t.Fatalf("Turn On Funnel… at %v, its row at %v", turn, need)
	}
	if tr.HasText("4. Your domain") {
		t.Fatal("Cloudflare's steps under Funnel")
	}
	offPage(t, tr)
}

// A link only this computer can open says so on the pair page, by the
// steps' own names, and offers to listen on the network.
func TestSettingsPairNote(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	setConfigHome(t, t.TempDir())
	a, tr := newSettingsTester(t, "pair")
	a.webOn = true
	tr.Frame()
	if a.settingsUI.data.pair == nil {
		t.Fatalf("no pairing: %v", tr.Texts())
	}
	// A code made here, in place of the one made in the background,
	// whose link is this machine's own.
	a.settingsUI.data.pair = &webPairing{ready: true, local: true, code: "ABCD-1234", link: "http://127.0.0.1:7370/#code=ABCD-1234",
		expires: time.Now().Add(10 * time.Minute)}
	tr.Frame()
	if !tr.HasText("choose how your phone reaches it") || !tr.HasText("Use My Network") {
		t.Fatalf("the warning does not name the step, or offer the network: %v", tr.Texts())
	}
}
