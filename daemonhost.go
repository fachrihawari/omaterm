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

	"omaterm/internal/cli"
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
		// The daemon is off, yet its service starts it at login: both
		// would run the sessions, and resume every agent twice.
		if !a.settings.Daemon && runtime.GOOS == "linux" && daemon.ServiceInstalled() {
			a.serviceConflict = true
		}
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
		host := a.hostReady.Load()
		if !a.controlSocket.Load() || host == nil {
			return nil, proto.Errorf(proto.CodeAppClosed, "omaterm's control socket is off (Settings: controlSocket)")
		}
		// No longer than the daemon waits for the answer: past that, a
		// wait went on here for ever, its caller long gone.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute+time.Second)
		defer cancel()
		return host.Handle(ctx, method, params)
	}
	r.OnConnection = func(reconnecting bool) {
		a.post(func() {
			back := a.daemonReconnecting && !reconnecting
			a.daemonReconnecting = reconnecting
			// While the window was away, the daemon may have made
			// sessions (web access, the omaterm command) in state.json.
			if back {
				a.adoptSavedSessions()
				if a.saveDeferred {
					a.saveDeferred = false
					a.saveState()
				}
			}
		})
	}
	a.setPanes(r)
	a.hookPanes()

	a.pruneUnknownPanes(r)
	// Another build, even of this version: its sessions run older code.
	if r.Version() != daemon.Build(appVersion()) {
		a.daemonStale = daemon.VersionOf(r.Version())
	}
}

// pruneUnknownPanes ends the daemon's panes no session holds: their
// session was closed while the app could not tell the daemon. Unless the
// sessions file couldn't be read, when no pane is known: they all stay.
func (a *appState) pruneUnknownPanes(r *daemon.Remote) {
	if a.stateLoadFailed != "" {
		return
	}
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
	default:
		// Its sessions keep running there, agents with them.
		a.daemonIgnored = true
	}
	a.daemonRefusedOpen = nil
}

// ignoredDaemonBanner tells that the sessions also run in the session
// daemon of another version, the user having run without it.
func (a *appState) ignoredDaemonBanner(c *ui.Context) {
	if !a.daemonIgnored {
		return
	}
	a.banner(c, "ignored-daemon-banner", iconAlert, pal.Warn, false,
		"Your sessions also run in another version's session daemon, so agents aren't resumed here. Restart omaterm to run them in this version's.", func() {
			// The dialog asks again at launch, where Restart Sessions
			// moves them over.
			if bannerButton(c, "Restart omaterm", true) {
				a.saveState()
				a.relaunch()
			}
		})
}

// daemonBanner offers to restart the sessions in this version's daemon
// once an update left them in the older one.
func (a *appState) daemonBanner(c *ui.Context) {
	if a.daemonReconnecting {
		a.banner(c, "daemon-reconnecting", iconRefresh, pal.Warn, false, "Reconnecting to the session daemon…", nil)
		return
	}
	if a.daemonStale == "" {
		return
	}
	a.banner(c, "daemon-banner", iconRefresh, pal.Accent, false,
		"omaterm updated: your sessions still run in the previous build's session daemon ("+a.daemonStale+").", func() {
			if bannerButton(c, "Not Now", false) {
				a.daemonStale = ""
			}
			if bannerButton(c, "Restart Sessions", true) {
				a.daemonStale = ""
				a.restartDaemon()
			}
		})
}

// banner is a notice above the panes: an icon, what it says, two lines at
// most with the rest in its tooltip, and its buttons. danger tints it red.
func (a *appState) banner(c *ui.Context, key string, icon *ui.SVG, iconColor ui.Color, danger bool, msg string, buttons func()) {
	row := ui.Row(c.Key(key)).Margin(0, 0, a.paneGap(), 0).Padding(8, 12).Gap(10).AlignItems(ui.Center).Radius(8)
	if danger {
		row.Background(pal.Bad.Alpha(0.10)).Border(1, pal.Bad.Alpha(0.35))
	} else {
		row.Background(pal.Raised).Border(1, pal.Border)
	}
	row.Children(func() {
		ui.Icon(c, icon).Width(14).Height(14).TextColor(iconColor)
		ui.Text(c, msg).Tooltip(msg).
			FontSize(12).TextColor(pal.TextSoft).MaxLines(2).Ellipsis("…").Grow(1).MinWidth(0)
		if buttons != nil {
			buttons()
		}
	})
}

// bannerButton is a banner's button: primary filled, else quiet. It
// reports a click.
func bannerButton(c *ui.Context, label string, primary bool) bool {
	if primary {
		return ui.PrimaryButton(c, label).Height(26).FontSize(12).Shrink(0).Clicked()
	}
	b := ui.ButtonBase(c).Padding(4, 10).Radius(6).Height(26).Shrink(0)
	if b.Hovered() {
		b.Background(pal.Hover)
	}
	return b.Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(pal.TextMuted)
	}).Clicked()
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
	a.banner(c, "crash-banner", iconAlert, pal.Bad, false, what+" Its report can help fix it.", func() {
		if bannerButton(c, "Dismiss", false) {
			a.dismissCrashes()
		}
		if bannerButton(c, "Show Report", true) {
			if a.win != nil {
				for _, r := range a.crashes {
					mygo.Shell.OpenPath(r.Path)
				}
			}
			a.dismissCrashes()
		}
	})
}

// serviceBanner tells that the session daemon is off in the settings while
// its service still starts it: one of the two has to go.
func (a *appState) serviceBanner(c *ui.Context) {
	if !a.serviceConflict {
		return
	}
	a.banner(c, "service-banner", iconAlert, pal.Warn, false,
		"The session daemon is off, but its service still starts it at login, so both run sessions. Agents aren't resumed here until one goes.", func() {
			if bannerButton(c, "Remove the Service", false) {
				a.serviceConflict = false
				go func() {
					err := cli.UninstallService()
					a.post(func() {
						if err != nil {
							a.queuedToasts = append(a.queuedToasts, "Could not remove the service: "+err.Error())
						}
					})
				}()
			}
			if bannerButton(c, "Turn the Daemon Back On", true) {
				a.settings.Daemon = true
				a.saveSettings()
				a.saveState()
				a.relaunch()
			}
		})
}

// stateBanner tells that state.json couldn't be read: omaterm runs with
// no projects and leaves the file, and every running session, alone.
func (a *appState) stateBanner(c *ui.Context) {
	if a.stateLoadFailed == "" {
		return
	}
	a.banner(c, "state-banner", iconAlert, pal.Bad, true,
		"omaterm couldn't read its sessions file, so it shows none and changes nothing: your sessions keep running. A copy is at "+
			cli.ShortPath(a.stateLoadFailed)+".", func() {
			if bannerButton(c, "Show File", true) && a.win != nil {
				mygo.Shell.ShowItemInFolder(a.stateLoadFailed)
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
