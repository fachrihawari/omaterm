package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"omaterm/internal/notice"
	"omaterm/internal/session"
)

// While no app follows it, the daemon is the one to tell the user that a
// session needs them: the window, closed, cannot.

// Notifier shows a desktop notification for a session.
type Notifier func(title, body, sessionID string)

// notifyStatus notifies of a pane's change as the app would, while no app
// follows the daemon.
func (d *Daemon) notifyStatus(info session.Info, prevStatus session.Status) {
	if d.appAttached() {
		return
	}
	// The hook's copy may be taken before the program's message came
	// with the status: the pane's own Info has it.
	if p := d.reg.Get(info.ID); p != nil {
		if now := p.Info(); now.Status == info.Status {
			info = now
		}
	}
	prev := info
	prev.Status = prevStatus
	kind := notice.For(prev, info, false, d.notifyPrefs())
	if kind == notice.None || !d.limit.Allow(info.Session) {
		return
	}
	d.notifier()(d.sessionLabel(info.Session), notice.Body(kind, info.Message), info.Session)
}

// notifyProgram passes on a program's own notification (OSC 9, 777).
func (d *Daemon) notifyProgram(paneID, title, body string) {
	if d.appAttached() {
		return
	}
	p := d.reg.Get(paneID)
	if p == nil {
		return
	}
	sid := p.Info().Session
	if title == "" {
		title = d.sessionLabel(sid)
	}
	d.notifier()(title, body, sid)
}

func (d *Daemon) notifier() Notifier {
	if d.opts.Notifier != nil {
		return d.opts.Notifier
	}
	return d.notifySend
}

// notifyPrefs are settings.json's notify; all on by default.
func (d *Daemon) notifyPrefs() notice.Prefs {
	s := struct {
		Notify *notice.Prefs `json:"notify"`
	}{}
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(d.stateFile()), "settings.json")); err == nil {
		json.Unmarshal(b, &s)
	}
	if s.Notify == nil {
		return notice.Prefs{Waiting: true, Done: true, Error: true}
	}
	return *s.Notify
}

// sessionLabel is "session · project", as the app titles its
// notifications.
func (d *Daemon) sessionLabel(sessionID string) string {
	d.stateMu.Lock()
	st := d.loadState()
	d.stateMu.Unlock()
	ps := d.protoState(st)
	for _, s := range ps.Sessions {
		if s.ID != sessionID {
			continue
		}
		for _, p := range ps.Projects {
			if p.ID == s.Project {
				return s.Name + " · " + p.Name
			}
		}
		return s.Name
	}
	return "omaterm"
}

// notifySend shows the notification with notify-send, waiting for it to
// be clicked: that starts the app on the session. Without notify-send
// (not Linux) nothing shows.
func (d *Daemon) notifySend(title, body, sessionID string) {
	bin, err := exec.LookPath("notify-send")
	if err != nil {
		return
	}
	go func() {
		// Clicking the notification invokes its "default" action, which
		// notification servers without action buttons (Omarchy's) do
		// too. The wait ends with the notification, or after a while.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "--app-name=omaterm", "--wait", "--action=default=Open", title, body)
		cmd.Env = append(os.Environ(), d.envList()...) // the app's display and bus
		out, err := cmd.Output()
		if err != nil || strings.TrimSpace(string(out)) != "default" {
			return
		}
		exe, err := os.Executable()
		if err != nil {
			return
		}
		open := exec.Command(exe, "--session="+sessionID)
		open.Env = cmd.Env
		open.Dir = os.Getenv("HOME")
		if open.Start() == nil {
			go open.Wait()
		}
	}()
}
