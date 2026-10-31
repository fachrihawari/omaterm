package main

import (
	"testing"
	"time"
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

// The pair page keeps its local note in step with the tunnel row's new
// name.
func TestSettingsPairNote(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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
	if !tr.HasText("Reach It from Elsewhere") {
		t.Fatalf("the note does not name the row: %v", tr.Texts())
	}
}
