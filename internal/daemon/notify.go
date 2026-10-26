package daemon

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"omaterm/internal/notice"
	"omaterm/internal/proto"
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
	kind := notice.For(prev, info, false, d.notifyPrefs().Prefs)
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
	if !d.notifyPrefs().Programs {
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

// notifyPrefs are settings.json's notify: which changes notify, whether
// they sound, and whether programs' own notifications show. All on by
// default.
type notifyPrefs struct {
	notice.Prefs
	Sound    bool `json:"sound"`
	Programs bool `json:"programs"`
}

func (d *Daemon) notifyPrefs() notifyPrefs {
	s := struct {
		Notify notifyPrefs `json:"notify"`
	}{Notify: notifyPrefs{Prefs: notice.Prefs{Waiting: true, Done: true, Error: true}, Sound: true, Programs: true}}
	d.readSettings(&s)
	return s.Notify
}

// termPrefs are settings.json's terminal settings a pane's own terminal
// takes as it starts: the app's shows what it has, so only the shell, the
// scrollback, and who answers clipboard reads matter here.
type termPrefs struct {
	Shell         string `json:"shell"`
	ScrollbackMB  int    `json:"scrollbackMB"`
	ClipboardRead string `json:"clipboardRead"`
}

func (d *Daemon) termPrefs() termPrefs {
	s := struct {
		Terminal termPrefs `json:"terminal"`
	}{}
	d.readSettings(&s)
	return s.Terminal
}

// readSettings reads the app's settings.json into v.
func (d *Daemon) readSettings(v any) {
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(d.stateFile()), "settings.json")); err == nil {
		json.Unmarshal(b, v)
	}
}

// browserURL is what a browser pane loads for what was typed, searching
// with the settings' search engine.
func (d *Daemon) browserURL(text string) string {
	s := struct {
		SearchEngine string `json:"searchEngine"`
	}{}
	d.readSettings(&s)
	proto.SetSearchEngine(s.SearchEngine)
	return proto.BrowserURL(text)
}

// apply gives a pane's start options the preferences. Clipboard reads
// are refused here, unless the settings say to ask: then they go to the
// app's terminal, which asks. Passed on with no app to ask, a read had no
// answer, and came back later as stray input.
func (t termPrefs) apply(o *session.StartOptions) {
	if o.Shell == "" {
		o.Shell = t.Shell
	}
	if t.ScrollbackMB > 0 {
		o.Scrollback = t.ScrollbackMB << 20
	}
	o.ClipboardRead = terminal.ClipboardDeny
	if t.ClipboardRead == "ask" {
		o.ClipboardRead = terminal.ClipboardForward
	}
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
		args := []string{"--app-name=omaterm", "--wait", "--action=default=Open"}
		if !d.notifyPrefs().Sound {
			args = append(args, "--hint=boolean:suppress-sound:true")
		}
		cmd := exec.CommandContext(ctx, bin, append(args, title, body)...)
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
