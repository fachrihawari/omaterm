package main

// The ways a phone reaches web access, each with a page of its own in
// Settings: what it is, what it needs on this computer and the phone,
// and a button to use it. The Web Access page lists them as its second
// step, between turning it on and pairing a phone.

import (
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/tunnel"
)

// webWay is a way a phone reaches web access.
type webWay struct {
	// page is its page's ID; row is its row's on the Web Access page,
	// which a search finds.
	page, row string
	title     string
	icon      *ui.SVG
	// sub is the row's line: who opens the link, and whether it stays.
	sub string
	// hero is its page's introduction: how it works.
	hero string
	// provider is the tunnel it runs; "" is the network, which runs none.
	provider tunnel.Provider
}

var webWays = []webWay{
	{page: "web-tailscale", row: "webTailscale", title: "Tailscale", icon: iconGlobeLock, provider: tunnel.Tailscale,
		sub: "Your own devices, signed in to Tailscale. The link stays the same.",
		hero: "Your phone and this computer join your tailnet, the private network Tailscale makes of your devices, and only they can open the link. " +
			"It is https://<this computer>.<your tailnet>.ts.net, and stays the same."},
	{page: "web-funnel", row: "webFunnel", title: "Tailscale Funnel", icon: iconGlobe, provider: tunnel.Funnel,
		sub: "A public link, with nothing to install on the phone. The link stays the same.",
		hero: "Tailscale serves a public https link on this computer’s tailnet name, which any browser opens; only devices you pair get past its sign-in. " +
			"The phone needs no app, and the link stays the same."},
	{page: "web-cloudflare", row: "webCloudflare", title: "Cloudflare", icon: iconGlobe, provider: tunnel.Cloudflare,
		sub: "A public link, with nothing to install on the phone. On your own domain, the link stays the same.",
		hero: "cloudflared carries web access to a public https link, which any browser opens; only devices you pair get past its sign-in. " +
			"With no account it makes a quick link, new each time it starts. On your own domain, the link stays."},
	{page: "web-wifi", row: "webNetwork", title: "Same Wi-Fi", icon: iconRadio,
		sub: "Phones on this network, over plain HTTP. No notifications or voice.",
		hero: "omaterm listens on this computer’s address on your network, and a phone on the same Wi-Fi opens it over plain HTTP, " +
			"which anyone on the network could read. Use it on networks you trust. Notifications, voice and the home-screen app need HTTPS, which the others have."},
}

// webWayPages are the ways' pages, under Web Access.
func webWayPages() []settingsPageDef {
	pages := make([]settingsPageDef, 0, len(webWays))
	for _, w := range webWays {
		w := w
		pages = append(pages, settingsPageDef{ID: w.page, Parent: "web", Title: w.title, Icon: w.icon, Hero: w.hero,
			Body: func(a *appState, c *ui.Context) { a.settingsWebWay(c, w) }})
	}
	return pages
}

// Where the ways' parts are got, and set up.
const (
	tailscaleDownload   = "https://tailscale.com/download"
	tailscaleDNSAdmin   = "https://login.tailscale.com/admin/dns"
	cloudflaredDownload = "https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/"
	cloudflareDash      = "https://dash.cloudflare.com/"
	cloudflareZeroTrust = "https://one.dash.cloudflare.com/"
)

// webWayInUse tells whether web access uses the way.
func (a *appState) webWayInUse(w webWay) bool {
	if w.provider == "" {
		return a.webOn && a.webNetwork
	}
	return a.settingsUI.data.web.Tunnel == string(w.provider)
}

// webWayMissing is why the way can't run on this computer: its command
// is missing, or signed out; "" when it can, or when it isn't known yet.
func (a *appState) webWayMissing(w webWay) string {
	if w.provider == "" {
		return ""
	}
	return a.settingsUI.data.avail[w.provider]
}

// webWayState is what became of the way, as its row's pill and its
// page's title say it: "" when web access doesn't use it.
func (a *appState) webWayState(w webWay) (string, pillKind) {
	d := &a.settingsUI.data
	switch {
	case !a.webWayInUse(w):
		return "", pillPlain
	case w.provider == "":
		return "In use", pillOk
	case !a.webOn:
		return "Chosen", pillPlain
	case d.webTunnelURL != "":
		return "In use", pillOk
	case d.webTunnelErr != "" || d.webTunnelWhy != "":
		return "Not working", pillWarn
	}
	return "Starting…", pillPlain
}

// webWayRow is the way's row on the Web Access page: what it is, its
// state, and the page that sets it up.
func (a *appState) webWayRow(c *ui.Context, w webWay) {
	if a.settingsRow(c, w.title, settingsRowOpts{id: w.row, sub: w.sub, link: w.page}, func() {
		state, kind := a.webWayState(w)
		switch {
		case state != "":
			settingsPill(c, state, kind)
		case a.webWayMissing(w) != "":
			settingsPill(c, "Needs setup", pillPlain)
		}
	}) {
		a.settingsGo(w.page)
	}
}

// settingsWebWay is a way's page: whether web access uses it, with a
// button to use it or stop, then what it needs, each part with where to
// get it or turn it on.
func (a *appState) settingsWebWay(c *ui.Context, w webWay) {
	if onWindows {
		a.settingsNotOnWindows(c, w.page, "Web access isn't available on Windows yet", "The sessions are served by the session daemon, which doesn't run on Windows yet.")
		return
	}
	d := &a.settingsUI.data
	inUse := a.webWayInUse(w)
	daemonOn := a.webAvailable()
	missing := a.webWayMissing(w)
	a.settingsGroup(c, w.page, "", "", func() {
		sub, mono, note := w.sub, false, ""
		switch {
		case !daemonOn:
			sub = "Web access needs the session daemon: turn on “Keep sessions running” on the Web Access page first."
		case !inUse && missing != "":
			sub = "Get what it needs, below, first."
		case inUse && w.provider == "":
			sub, mono = d.webLink, d.webLink != ""
		case inUse:
			sub, mono, note = a.webTunnelNote(), d.webTunnelURL != "", a.webTunnelCaveat()
			if sub == "" {
				sub = "Chosen: it starts with web access."
			}
		case w.provider != "" && d.web.Tunnel != "":
			note = "In place of " + webWayTitle(tunnel.Provider(d.web.Tunnel)) + ", which web access uses now."
		}
		title, _ := a.webWayState(w)
		if title == "" {
			title = "Not in use"
		}
		a.settingsRow(c, title, settingsRowOpts{id: w.page + "-use", sub: sub, mono: mono, note: note}, func() {
			if inUse {
				if w.provider != "" && d.webTunnelURL != "" && a.settingsButton(c, "Copy Link", false, false, false) {
					mygo.Clipboard.WriteText(d.webTunnelURL)
					toast(c, "Copied the link")
				}
				if a.settingsButton(c, "Stop Using", false, false, false) {
					a.useWebWay(c, w, false)
				}
				return
			}
			if a.settingsButton(c, "Use "+w.title, true, false, !daemonOn || missing != "") {
				a.useWebWay(c, w, true)
			}
		})
	})
	switch w.provider {
	case tunnel.Tailscale, tunnel.Funnel:
		a.settingsTailscaleNeeds(c, w)
	case tunnel.Cloudflare:
		a.settingsCloudflareNeeds(c, w)
	default:
		a.settingsGroup(c, w.page+"-needs", "What it needs", "", func() {
			a.settingsRow(c, "The phone on the same network", settingsRowOpts{id: w.page + "-same",
				sub: "The Wi-Fi this computer is on. A phone on its mobile data can’t open it."}, nil)
		})
	}
}

// webWayTitle is the name of the way a tunnel provider is.
func webWayTitle(p tunnel.Provider) string {
	for _, w := range webWays {
		if w.provider == p {
			return w.title
		}
	}
	return string(p)
}

// useWebWay has web access use the way, or stop using it.
func (a *appState) useWebWay(c *ui.Context, w webWay, use bool) {
	if w.provider == "" {
		a.setWebNetwork(c, use)
	} else if use {
		a.setWebTunnel(c, string(w.provider))
	} else {
		a.setWebTunnel(c, "")
	}
	a.settingsUI.dataAt = time.Time{}
}

// webOpenButton is a button that opens a web page in the browser.
func (a *appState) webOpenButton(c *ui.Context, label, url string, primary bool) {
	if a.settingsButton(c, label, primary, false, false) {
		c.OpenURL(url)
	}
}

// settingsTailscaleNeeds is what Tailscale and Funnel need: Tailscale on
// this computer, signed in; HTTPS on the tailnet; and Tailscale on the
// phone, or Funnel turned on.
func (a *appState) settingsTailscaleNeeds(c *ui.Context, w webWay) {
	d := &a.settingsUI.data
	a.settingsGroup(c, w.page+"-needs", "What it needs", "", func() {
		missing := a.webWayMissing(w)
		_, known := d.avail[w.provider]
		check, sub := 0, "Installed, and signed in to your account."
		if known {
			check = 1
		}
		if missing != "" {
			check, sub = -1, missing
		}
		a.settingsRow(c, "Tailscale on this computer", settingsRowOpts{id: w.page + "-here", sub: sub, check: check}, func() {
			if missing != "" {
				a.webOpenButton(c, "Get Tailscale", tailscaleDownload, true)
			}
		})
		if w.provider == tunnel.Tailscale {
			a.settingsRow(c, "Tailscale on your phone", settingsRowOpts{id: w.page + "-phone",
				sub: "The Tailscale app, signed in to the same account as this computer."}, func() {
				a.webOpenButton(c, "Get the App", tailscaleDownload, false)
			})
		} else {
			// Tailscale waits for the tailnet to turn Funnel on, at a link
			// of its own that the tunnel's error names.
			link := ""
			if a.webWayInUse(w) {
				link = tunnel.EnableLink(d.webTunnelErr)
			}
			check, sub := 0, "Once for your tailnet. The first time Funnel starts, omaterm shows the link that turns it on."
			if link != "" {
				check, sub = -1, "Not on yet for your tailnet: open the link to turn it on, then come back."
			}
			a.settingsRow(c, "Funnel turned on", settingsRowOpts{id: w.page + "-on", sub: sub, check: check}, func() {
				if link != "" {
					a.webOpenButton(c, "Turn On Funnel…", link, true)
				}
			})
		}
		a.settingsRow(c, "HTTPS on your tailnet", settingsRowOpts{id: w.page + "-https",
			sub: "Once for your tailnet, in Tailscale’s admin console: DNS › HTTPS Certificates › Enable."}, func() {
			a.webOpenButton(c, "Open Admin Console", tailscaleDNSAdmin, false)
		})
	})
}

// settingsCloudflareNeeds is what Cloudflare needs: cloudflared on this
// computer, and, to keep the link, a tunnel of the user's own domain, set
// up in Cloudflare step by step.
func (a *appState) settingsCloudflareNeeds(c *ui.Context, w webWay) {
	d := &a.settingsUI.data
	a.settingsGroup(c, w.page+"-needs", "What it needs", "", func() {
		missing := a.webWayMissing(w)
		_, known := d.avail[w.provider]
		check, sub := 0, "Installed."
		if known {
			check = 1
		}
		if missing != "" {
			check, sub = -1, missing
		}
		a.settingsRow(c, "cloudflared on this computer", settingsRowOpts{id: w.page + "-here", sub: sub, check: check}, func() {
			if missing != "" {
				a.webOpenButton(c, "Get cloudflared", cloudflaredDownload, true)
			}
		})
	})
	port := strconv.Itoa(d.web.ListenPort())
	foot := "Without them, the link is a quick one, new each time the tunnel starts: after a restart, the phone pairs again."
	if d.webTokenSaved && d.web.CloudflareHost != "" {
		foot = "The link is https://" + d.web.CloudflareHost + ", and stays the same."
	}
	a.settingsGroup(c, w.page+"-domain", "To keep the link: your own domain", foot, func() {
		a.settingsRow(c, "1. Your domain on Cloudflare", settingsRowOpts{id: w.page + "-dns",
			sub: "A free Cloudflare account, with your domain added to it."}, func() {
			a.webOpenButton(c, "Open Cloudflare", cloudflareDash, false)
		})
		a.settingsRow(c, "2. Create a tunnel", settingsRowOpts{id: w.page + "-create",
			sub: "In Zero Trust: Networks › Tunnels › Create a tunnel, of the Cloudflared type."}, func() {
			a.webOpenButton(c, "Open Zero Trust", cloudflareZeroTrust, false)
		})
		service := "http://localhost:" + port
		a.settingsRow(c, "3. Give it a public hostname", settingsRowOpts{id: w.page + "-host",
			sub: "A name on your domain, as term.example.com, with the service " + service + "."}, func() {
			if a.settingsButton(c, "Copy Service", false, false, false) {
				mygo.Clipboard.WriteText(service)
				toast(c, "Copied "+service)
			}
		})
		a.settingsCloudflare(c)
	})
}
