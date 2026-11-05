package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The tunnel row keeps the settings' shape: the title on the left with
// the live line under it, the choice at the right, Copy after the
// choice, and nothing off the page.
func TestSettingsWebTunnelLayout(t *testing.T) {
	_, tr := tunnelPage(t, func(d *settingsData) {
		d.web.Tunnel = "tailscale"
		d.webTunnelURL = "https://box.tailnet.ts.net"
	})
	title, ok := tr.Find("Reach it from elsewhere")
	if !ok {
		t.Fatalf("no row: %v", tr.Texts())
	}
	url, _ := tr.Find("https://box.tailnet.ts.net")
	if url.X < title.X || url.Y < title.Y || url.Y > title.Y+40 {
		t.Fatalf("the live line at %v, the title at %v", url, title)
	}
	choice, ok := tr.Find("Tailscale (your devices)")
	if !ok {
		t.Fatal("no choice")
	}
	if choice.X <= title.X || choice.Y < title.Y-10 {
		t.Fatalf("the choice at %v, the title at %v", choice, title)
	}
	copy, ok := tr.Find("Copy")
	if !ok || copy.X <= choice.X+choice.W/2 {
		t.Fatalf("Copy at %v, the choice at %v", copy, choice)
	}
	// The footer says what each choice means.
	if !tr.HasText("Tunnels carry web access beyond this computer") {
		t.Fatalf("no footer: %v", tr.Texts())
	}
}

// A failing tunnel's line stays in the row, not over the choice.
func TestSettingsWebTunnelLayoutFailed(t *testing.T) {
	_, tr := tunnelPage(t, func(d *settingsData) {
		d.web.Tunnel = "cloudflare"
		d.webTunnelErr = "cloudflared: exit status 1"
	})
	title, _ := tr.Find("Reach it from elsewhere")
	line, ok := tr.Find("cloudflared: exit status 1")
	if !ok || line.X < title.X || line.Y < title.Y || line.Y > title.Y+40 {
		t.Fatalf("the failure line at %v, the title at %v", line, title)
	}
	if !tr.HasText("Cloudflare (public link)") {
		t.Fatal("the choice is gone")
	}
}

// Cloudflare chosen, the rows of its own domain follow the choice in the
// page's shape: their titles on the left, the how-to under the domain's,
// the fields at the right in line with the controls above them, and
// nothing off the page.
func TestSettingsWebCloudflareLayout(t *testing.T) {
	_, tr := tunnelPage(t, func(d *settingsData) {
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
	reach, domain, token, network := find("Reach it from elsewhere"), find("Your domain"), find("Tunnel token"), find("On your network")
	if !(reach.Y < domain.Y && domain.Y < token.Y && token.Y < network.Y) {
		t.Fatalf("out of order: the choice at %v, the domain %v, the token %v, the network %v", reach, domain, token, network)
	}
	if domain.X != reach.X || token.X != reach.X {
		t.Fatalf("the titles at %v and %v, the choice's at %v", domain, token, reach)
	}
	howTo := find(cloudflareHowTo(7370))
	if howTo.X != domain.X || howTo.Y < domain.Y+domain.H-1 || howTo.Y+howTo.H > token.Y {
		t.Fatalf("the how-to at %v, under the domain at %v, over the token at %v", howTo, domain, token)
	}
	copy := find("Copy")
	end := copy.X + copy.W
	for _, f := range []struct {
		name  string
		title ui.Rect
		below float32
	}{{"webCfHost", domain, token.Y}, {"webCfToken", token, network.Y}} {
		r := find(f.name)
		if r.X < howTo.X+howTo.W || r.X+r.W > end || end-(r.X+r.W) > 12 {
			t.Errorf("the field %s at %v, the how-to ends at %v, the controls at %v", f.name, r, howTo.X+howTo.W, end)
		}
		if r.Y < f.title.Y-10 || r.Y > f.below {
			t.Errorf("the field %s at %v, out of its row (%v)", f.name, r, f.title)
		}
	}
	for _, s := range tr.Texts() {
		if r, _ := tr.Find(s); r.X < 0 || r.X+r.W > settingsWidth {
			t.Errorf("%q off the page at %v", s, r)
		}
	}
}

// Funnel the tailnet hasn't turned on says so with the link that does,
// on a line of its own under the title, and offers to copy it after the
// choice. Cloudflare's rows are not there.
func TestSettingsWebFunnelOffLayout(t *testing.T) {
	const link = "https://login.tailscale.com/f/funnel?node=nABC123DEF"
	_, tr := tunnelPage(t, func(d *settingsData) {
		d.web.Tunnel = "funnel"
		d.webTunnelErr = "Turn on Funnel for this tailnet: " + link
	})
	title, _ := tr.Find("Reach it from elsewhere")
	line, ok := tr.Find("Turn on Funnel for this tailnet:\n" + link)
	if !ok || line.X != title.X || line.Y < title.Y || line.Y > title.Y+40 {
		t.Fatalf("the line at %v, the title at %v: %v", line, title, tr.Texts())
	}
	choice, ok := tr.Find("Tailscale Funnel (public link)")
	if !ok {
		t.Fatal("no choice")
	}
	copy, ok := tr.Find("Copy")
	if !ok || copy.X <= choice.X+choice.W/2 || copy.Y > title.Y+40 {
		t.Fatalf("Copy at %v, the choice at %v", copy, choice)
	}
	if tr.HasText("Your domain") || tr.HasText("Tunnel token") {
		t.Fatal("Cloudflare's rows under Funnel")
	}
	for _, s := range tr.Texts() {
		if r, _ := tr.Find(s); r.X < 0 || r.X+r.W > settingsWidth {
			t.Errorf("%q off the page at %v", s, r)
		}
	}
}

// A link only this computer can open says so on the pair page, by the
// rows' own names, and offers to listen on the network.
func TestSettingsPairNote(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	setConfigHome(t, t.TempDir())
	a, tr := newSettingsTester(t, "pair")
	a.webOn = true
	tr.Frame()
	p := a.settingsUI.data.pair
	if p == nil {
		t.Fatalf("no pairing: %v", tr.Texts())
	}
	p.ready = true
	p.local = true
	p.expires = time.Now().Add(10 * time.Minute) // a code that hasn't run out
	tr.Frame()
	if !tr.HasText("Reach it from elsewhere") || !tr.HasText("Use My Network") {
		t.Fatalf("the warning does not name the row, or offer the network: %v", tr.Texts())
	}
}
