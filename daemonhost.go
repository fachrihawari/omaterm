package main

import (
	"errors"
	"log"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/client"
	"omaterm/internal/daemon"
	"omaterm/internal/paths"
	"omaterm/internal/session"
)

// useDaemon moves the panes to the session daemon, which runs their
// shells apart from the window: closing it leaves them running, and the
// next launch shows them as they went on. Without a daemon (the setting
// is off, OMATERM_NO_DAEMON is set, it cannot start, or on Windows) the
// panes run in the app as before.
func (a *appState) useDaemon() {
	if !a.settings.Daemon || os.Getenv("OMATERM_NO_DAEMON") != "" || runtime.GOOS == "windows" {
		return
	}
	r, err := daemon.Connect(true)
	var refused *client.RefusedError
	if errors.As(err, &refused) {
		// A daemon of another protocol runs the sessions: the window asks
		// before restarting it, and shows no pane until then.
		open := true
		a.daemonRefusedOpen, a.daemonRefusedPID = &open, refused.Welcome.PID
		return
	}
	if err != nil {
		log.Printf("session daemon: %v; panes run in the app", err)
		return
	}
	r.TermOptions = func() session.StartOptions {
		return session.StartOptions{
			Font:            terminal.Font{Family: a.termFont, Size: a.fontSize},
			Theme:           omaTermTheme(),
			MinimumContrast: a.minimumContrast(),
			Padding:         terminal.Padding{X: 10, Y: 6},
		}
	}
	// Panes a stopped daemon took with it start again from their saved
	// scrollback, as after a restart.
	r.OnLost = func(ids []string) {
		a.post(func() {
			for _, id := range ids {
				a.restoring[id] = true
			}
			a.queuedToasts = append(a.queuedToasts, "Session daemon restarted: sessions restored")
		})
	}
	r.OnConnection = func(reconnecting bool) {
		a.post(func() { a.daemonReconnecting = reconnecting })
	}
	a.panes = r
	a.hookPanes()

	// Panes no session holds (their session was closed while the app
	// could not tell the daemon) end.
	known := map[string]bool{}
	for _, s := range a.state().Sessions {
		for _, p := range s.Panes {
			known[p.ID] = true
		}
	}
	for id := range r.Infos() {
		if !known[id] {
			r.Remove(id)
		}
	}
	if v := r.Version(); v != appVersion() {
		a.daemonStale = v
	}
}

// replaceRefusedDaemon stops the daemon of another protocol, which saves
// its sessions' scrollback, and uses a new one: the sessions start again
// from it. UI thread.
func (a *appState) replaceRefusedDaemon() {
	pid := a.daemonRefusedPID
	a.daemonRefusedPID = 0
	go func() {
		if p, err := os.FindProcess(pid); err == nil && pid > 0 {
			p.Signal(syscall.SIGTERM)
		}
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			c, err := client.Dial(paths.DaemonSocketPath())
			var refused *client.RefusedError
			if err != nil && !errors.As(err, &refused) {
				break // gone
			}
			if c != nil {
				c.Close()
			}
		}
		a.post(func() {
			a.useDaemon()
			if _, ok := a.panes.(*daemon.Remote); ok {
				a.queuedToasts = append(a.queuedToasts, "Session daemon restarted: sessions restored")
			}
		})
	}()
}

// daemonDialog asks to restart a session daemon the app cannot use.
func (a *appState) daemonDialog(c *ui.Context) {
	if a.daemonRefusedOpen == nil {
		return
	}
	switch a.confirmDialog(c, a.daemonRefusedOpen, "Restart Your Sessions",
		"They run in a session daemon from another version of omaterm, which this one cannot talk to. "+
			"Restarting it brings them back with their scrollback, and agents resume their conversations.",
		false, "Run Without It", "Restart Sessions") {
	case -1:
		return
	case 1:
		a.replaceRefusedDaemon()
	}
	a.daemonRefusedOpen = nil
}

// daemonBanner offers to restart the sessions in this version's daemon
// once an update left them in the older one.
func (a *appState) daemonBanner(c *ui.Context) {
	if a.daemonReconnecting {
		ui.Row(c.Key("daemon-reconnecting")).Margin(0, 0, a.paneGap(), 0).Padding(8, 12).Gap(10).AlignItems(ui.Center).
			Radius(8).Background(pal.Raised).Border(1, pal.Border).Children(func() {
			ui.Icon(c, iconRefresh).Width(14).Height(14).TextColor(pal.Warn)
			ui.Text(c, "Reconnecting to the session daemon…").FontSize(12).TextColor(pal.TextSoft)
		})
		return
	}
	if a.daemonStale == "" {
		return
	}
	ui.Row(c.Key("daemon-banner")).Margin(0, 0, a.paneGap(), 0).Padding(8, 12).Gap(10).AlignItems(ui.Center).
		Radius(8).Background(pal.Raised).Border(1, pal.Border).Children(func() {
		ui.Icon(c, iconRefresh).Width(14).Height(14).TextColor(pal.Accent)
		ui.Text(c, "omaterm updated: your sessions still run in "+a.daemonStale+"'s session daemon.").
			FontSize(12).TextColor(pal.TextSoft).Grow(1).MinWidth(0)
		later := ui.ButtonBase(c).Padding(4, 10).Radius(6).Height(26).Children(func() {
			ui.Text(c, "Later").FontSize(12).TextColor(pal.TextMuted)
		})
		if later.Clicked() {
			a.daemonStale = ""
		}
		if ui.PrimaryButton(c, "Restart Sessions").Height(26).FontSize(12).Clicked() {
			a.daemonStale = ""
			a.restartDaemon()
		}
	})
}

// restartDaemon stops the session daemon and starts this version's: the
// sessions start again from their scrollback, agents resuming.
func (a *appState) restartDaemon() bool {
	r, ok := a.panes.(*daemon.Remote)
	if !ok {
		return false
	}
	a.saveState()
	go r.Restart()
	return true
}
