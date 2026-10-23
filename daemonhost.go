package main

import (
	"log"
	"os"
	"runtime"

	"github.com/egoist/mygo/plugins/terminal"

	"omaterm/internal/daemon"
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
		a.queuedToasts = append(a.queuedToasts,
			"Sessions run in omaterm "+v+"'s daemon: Restart Session Daemon runs this version's")
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
