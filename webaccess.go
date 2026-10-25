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
func (a *appState) setWeb(c *ui.Context, on bool) bool {
	if on && !a.webAvailable() {
		toast(c, "Web access needs the session daemon, which is off")
		return false
	}
	cfg, err := webStore().SetConfig(func(cfg *web.Config) { cfg.Enabled = on })
	if err != nil {
		toast(c, "Could not save: "+err.Error())
		return false
	}
	a.webOn = cfg.Enabled
	st, err := daemon.ReloadWeb(false)
	switch {
	case err != nil && on:
		toast(c, "Web access could not start: "+err.Error())
		return false
	case on:
		toast(c, "Web access on: http://"+st.Addr)
	default:
		toast(c, "Web access off")
	}
	return true
}

// pairDevice turns web access on if need be, and shows a pairing code.
func (a *appState) pairDevice(c *ui.Context) {
	if !a.webOn && !a.setWeb(c, true) {
		return
	}
	p := &webPairing{}
	a.webPair = p
	open := true
	a.webPairOpen = &open
	go func() {
		store := webStore()
		cfg, err := store.Config()
		var code string
		var expires time.Time
		if err == nil {
			code, expires, err = store.NewCode()
		}
		base := web.BaseURL(cfg)
		link := web.PairLink(base, code)
		rows, qerr := web.QR(link)
		a.post(func() {
			p.ready = true
			if err != nil || qerr != nil {
				p.err = firstErr(err, qerr).Error()
				return
			}
			p.link, p.code, p.expires, p.qr = link, code, expires, rows
			p.local = strings.HasPrefix(base, "http://127.0.0.1")
		})
	}()
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
		backdrop.Background(pal.Scrim)
		panel.Background(pal.Popover).Radius(10).Border(1, pal.BorderStrong).
			Width(380).Padding(18).Gap(12).AlignItems(ui.Center).
			Shadow(0, 10, 30, 0, pal.FloatShadow)
		ui.Text(c, "Pair a Device").FontSize(14).Bold().TextColor(pal.Text)
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
				note += " Only this computer can open it: run omaterm web tunnel to reach it from your phone."
			}
			ui.Text(c, note).FontSize(11).TextColor(pal.TextFaint).TextAlign(ui.Center)
		}
		ui.Row(c).Gap(8).Justify(ui.End).FillWidth().Children(func() {
			if ui.PrimaryButton(c, "Done").Height(32).FontSize(12).Clicked() {
				*a.webPairOpen = false
			}
		})
	})
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
