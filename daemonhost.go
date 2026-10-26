package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/client"
	"omaterm/internal/crash"
	"omaterm/internal/daemon"
	"omaterm/internal/paths"
	"omaterm/internal/proto"
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
	r.TermOptions = a.termStartOptions
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
	// The omaterm command's calls that reach the daemon while the window
	// is open come here, unless the control socket is off.
	r.OnCall = func(method string, params json.RawMessage) (any, error) {
		if !a.settings.ControlSocket || a.host == nil {
			return nil, proto.Errorf(proto.CodeAppClosed, "omaterm's control socket is off (Settings: controlSocket)")
		}
		return a.host.Handle(context.Background(), method, params)
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
	// Another build, even of this version: its sessions run older code.
	if r.Version() != daemon.Build(appVersion()) {
		a.daemonStale = daemon.VersionOf(r.Version())
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
		msg := "omaterm updated: your sessions still run in the previous build's session daemon (" + a.daemonStale + ")."
		// Two lines at most, in a narrow window; the tooltip has it all.
		ui.Text(c, msg).Tooltip(msg).
			FontSize(12).TextColor(pal.TextSoft).MaxLines(2).Ellipsis("…").Grow(1).MinWidth(0)
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

// crashBanner tells of a crash a past run left: the report is kept in a
// log, which Show Report opens.
func (a *appState) crashBanner(c *ui.Context) {
	if len(a.crashes) == 0 {
		return
	}
	what := "omaterm quit unexpectedly last time."
	if len(a.crashes) == 1 && a.crashes[0].Name == "daemon" {
		what = "The session daemon quit unexpectedly last time."
	}
	ui.Row(c.Key("crash-banner")).Margin(0, 0, a.paneGap(), 0).Padding(8, 12).Gap(10).AlignItems(ui.Center).
		Radius(8).Background(pal.Raised).Border(1, pal.Border).Children(func() {
		ui.Icon(c, iconAlert).Width(14).Height(14).TextColor(pal.Bad)
		msg := what + " Its report can help fix it."
		ui.Text(c, msg).Tooltip(msg).
			FontSize(12).TextColor(pal.TextSoft).MaxLines(2).Ellipsis("…").Grow(1).MinWidth(0)
		dismiss := ui.ButtonBase(c).Padding(4, 10).Radius(6).Height(26).Children(func() {
			ui.Text(c, "Dismiss").FontSize(12).TextColor(pal.TextMuted)
		})
		if dismiss.Clicked() {
			a.dismissCrashes()
		}
		if ui.PrimaryButton(c, "Show Report").Height(26).FontSize(12).Clicked() {
			if a.win != nil {
				for _, r := range a.crashes {
					mygo.Shell.OpenPath(r.Path)
				}
			}
			a.dismissCrashes()
		}
	})
}

// dismissCrashes notes that the user was told of the crashes.
func (a *appState) dismissCrashes() {
	a.crashes = nil
	if a.win != nil {
		crash.MarkSeen(paths.StateDir())
	}
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

// hiddenDetach is how long a daemon pane goes unshown before the app lets
// go of its terminal: each shown pane is a second emulator, the
// daemon's being the first.
const hiddenDetach = 10 * time.Minute

// detachHidden lets go of the terminals of the daemon panes not shown for
// hiddenDetach; showing one attaches it again. UI thread.
func (a *appState) detachHidden(now time.Time) {
	r, ok := a.panes.(*daemon.Remote)
	if !ok {
		return
	}
	shown := map[string]bool{}
	if n := a.activeSplit(); n != nil && a.activeView == tabTerminal {
		for _, id := range n.termIDs() {
			shown[id] = true
		}
	}
	r.Each(func(p session.Handle) {
		id := p.ID()
		if shown[id] || p.TermIfOpen() == nil || now.Sub(a.paneShown[id]) < hiddenDetach {
			return
		}
		r.Detach(id)
		delete(a.paneShown, id)
	})
}
