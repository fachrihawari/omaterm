package main

// Web access: the sessions in a browser, which the session daemon serves
// on the loopback for a tunnel to carry. The window turns it on and pairs
// devices; the omaterm web command does the same.

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/daemon"
	"omaterm/internal/paths"
	"omaterm/internal/web"
)

// webPairing is the pairing dialog's content: a code, its link and the
// link's QR code; local tells a link only this machine can open.
type webPairing struct {
	ready   bool
	link    string
	code    string
	expires time.Time
	qr      [][]bool
	local   bool
	err     string
}

func webStore() *web.Store { return web.OpenStore(web.StorePath(paths.ConfigDir())) }

// webAvailable tells whether the session daemon runs the panes, which web
// access needs: it serves them.
func (a *appState) webAvailable() bool {
	_, ok := a.panes.(*daemon.Remote)
	return ok
}

// setWeb turns web access on or off.
func (a *appState) setWeb(c *ui.Context, on bool) {
	a.setWebThen(c, on, nil)
}

// setWebThen is setWeb, running then once web access took the change.
func (a *appState) setWebThen(c *ui.Context, on bool, then func()) {
	if on && !a.webAvailable() {
		toast(c, "Web access needs the session daemon, which is off")
		return
	}
	a.changeWebSaying(c, func(cfg *web.Config) { cfg.Enabled = on }, "", then)
}

// setWebNetwork has web access listen on the network, or only on the
// loopback, turning it on.
func (a *appState) setWebNetwork(c *ui.Context, on bool) {
	if !a.webAvailable() {
		toast(c, "Web access needs the session daemon, which is off")
		return
	}
	a.changeWebSaying(c, func(cfg *web.Config) {
		cfg.Enabled = true
		cfg.Host = map[bool]string{true: "0.0.0.0"}[on]
	}, "", nil)
}

// setWebTunnel picks how omaterm carries web access beyond this machine,
// turning it on: "" for no tunnel, "tailscale" or "cloudflare".
func (a *appState) setWebTunnel(c *ui.Context, provider string) {
	if provider != "" && !a.webAvailable() {
		toast(c, "Web access needs the session daemon, which is off")
		return
	}
	said := ""
	if provider == "" {
		said = "The tunnel is off"
	}
	a.changeWebSaying(c, func(cfg *web.Config) {
		if provider != "" {
			cfg.Enabled = true
		}
		cfg.Tunnel = provider
	}, said, nil)
}

// changeWebSaying changes web access's config and has the daemon follow,
// telling said once it took rather than whether web access is on (for a
// change of its port or URL), and running then. The daemon is asked off
// the UI thread: turning web access off waits for the pages in flight,
// which waited on this window, so the window froze for seconds.
func (a *appState) changeWebSaying(c *ui.Context, change func(*web.Config), said string, then func()) {
	defer a.invalidateWindows() // both windows show it
	cfg, err := webStore().SetConfig(change)
	if err != nil {
		toast(c, "Could not save: "+err.Error())
		return
	}
	// Told where it was asked: Settings, while it is open.
	say := func(msg string) {
		if a.settingsWin != nil {
			a.settingsToast(msg)
		} else {
			a.toast(nil, msg)
		}
	}
	go func() {
		st, err := daemon.ReloadWeb(false)
		a.post(func() {
			defer a.invalidateWindows()
			if err != nil && cfg.Enabled {
				// It didn't start (the port is taken…): it isn't on.
				cfg, _ := webStore().SetConfig(func(cfg *web.Config) { cfg.Enabled = false })
				a.webOn, a.webNetwork = false, cfg.OnNetwork()
				say("Web access could not start: " + err.Error())
				return
			}
			a.webOn, a.webNetwork = cfg.Enabled, cfg.OnNetwork()
			switch {
			case said != "":
				say(said)
			case cfg.OnNetwork():
				say("Web access on your network, over plain HTTP: use it on networks you trust")
			case st.TunnelURL != "":
				say("Web access on: " + st.TunnelURL)
			case cfg.Tunnel != "" && st.TunnelError != "":
				say("Web access on: http://" + st.Addr + " — the tunnel isn't running: " + st.TunnelError)
			case cfg.Enabled:
				say("Web access on: http://" + st.Addr)
			default:
				say("Web access off")
			}
			if then != nil {
				then()
			}
		})
	}()
}

// pairDevice turns web access on if need be, and shows a pairing code.
func (a *appState) pairDevice(c *ui.Context) {
	show := func() {
		a.webPair = a.startPairing()
		open := true
		a.webPairOpen = &open
	}
	if a.webOn {
		show()
		return
	}
	a.setWebThen(c, true, show)
}

// startPairing makes a pairing code, its link and QR code, in the
// background.
func (a *appState) startPairing() *webPairing {
	p := &webPairing{}
	go func() {
		store := webStore()
		cfg, err := store.Config()
		var code string
		var expires time.Time
		if err == nil {
			code, expires, err = store.NewCode()
		}
		// The tunnel the daemon runs, when one does, says where the link
		// lands; without a daemon the config alone tells.
		st, _ := daemon.StatusWeb()
		base := web.BaseURL(cfg, st.TunnelURL)
		link := web.PairLink(base, code)
		rows, qerr := web.QR(link)
		a.post(func() {
			p.ready = true
			defer a.invalidateWindows()
			if err != nil || qerr != nil {
				p.err = firstErr(err, qerr).Error()
				return
			}
			p.link, p.code, p.expires, p.qr = link, code, expires, rows
			p.local = strings.HasPrefix(base, "http://127.0.0.1")
		})
	}()
	return p
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// webPairDialog shows the pairing code: a QR code a phone's camera opens,
// the link, and the code to type.
func (a *appState) webPairDialog(c *ui.Context) {
	if a.webPairOpen == nil || a.webPair == nil {
		return
	}
	if !*a.webPairOpen {
		a.webPairOpen, a.webPair = nil, nil
		return
	}
	p := a.webPair
	ui.DialogBase(c, a.webPairOpen, func(backdrop, panel ui.Element) {
		a.dialogFrame(c, backdrop, panel)
		dialogTitle(c, "Pair a Device", false)
		// The code and what goes with it centered under the title.
		ui.Column(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() { a.webPairBody(c, p) })
		ui.Row(c).Gap(8).Justify(ui.End).FillWidth().Margin(8, 0, 0, 0).Children(func() {
			if a.dialogButton(c, "Done", true, false) {
				*a.webPairOpen = false
			}
		})
	})
}

// webPairBody is the Pair dialog's code: its QR, the code and the link.
func (a *appState) webPairBody(c *ui.Context, p *webPairing) {
	switch {
	case !p.ready:
		ui.Text(c, "Making a code…").FontSize(12).TextColor(pal.TextMuted).Padding(40, 0)
	case p.err != "":
		ui.Text(c, p.err).FontSize(12).TextColor(pal.Bad)
	default:
		qrView(c, p.qr, 4)
		ui.Text(c, "Scan with the device's camera, or open the link and enter the code.").
			FontSize(12).TextColor(pal.TextMuted).TextAlign(ui.Center)
		ui.Text(c, p.code).FontSize(22).FontWeight(700).Font(monoFont).TextColor(pal.Text)
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, p.link).FontSize(11).TextColor(pal.TextSoft).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).Grow(1).Tooltip(p.link)
			copyBtn := ui.ButtonBase(c).Padding(4, 10).Radius(6).Border(1, pal.BorderStrong).Shrink(0).Children(func() {
				ui.Text(c, "Copy").FontSize(11).TextColor(pal.TextSoft)
			})
			if copyBtn.Clicked() {
				mygo.Clipboard.WriteText(p.link)
				c.Toast("Copied the link")
			}
		})
		note := "Works once, until " + p.expires.Format("15:04") + "."
		if p.local {
			note += " Only this computer can open it: turn on Web Access on Your Network, or pick a tunnel under Reach It from Elsewhere."
		}
		ui.Text(c, note).FontSize(11).TextColor(pal.TextFaint).TextAlign(ui.Center)
	}
}

// qrView draws a QR code as modules of cell points, dark on white.
func qrView(c *ui.Context, rows [][]bool, cell float32) {
	ui.Column(c).Background(qrLight).Radius(6).Children(func() {
		for y, row := range rows {
			ui.Row(c.Key(y)).Children(func() {
				for x, dark := range row {
					b := ui.Box(c.Key(x)).Width(cell).Height(cell)
					if dark {
						b.Background(qrDark)
					}
				}
			})
		}
	})
}
