package daemon

import (
	"strconv"
	"time"

	"omaterm/internal/notice"
	"omaterm/internal/procs"
	"omaterm/internal/session"
	"omaterm/internal/web"
)

// The phones that asked to be notified (web access's push) are told of a
// session that needs the user, failed or finished, and of a process that
// failed: with the app open too, which on a laptop left at home it
// usually is. While someone uses the desktop, nothing goes: the desktop
// tells them itself.

// deskIdle is how long after the desktop's last input its user is taken
// to have left it.
const deskIdle = 2 * time.Minute

// deskInput notes input from the desktop app, not a web page: someone is
// at the computer.
func (d *Daemon) deskInput() { d.deskAt.Store(time.Now().UnixMilli()) }

// atDesk tells whether the desktop saw input lately. The session it shows
// stays viewed while its window has the focus, someone there or not.
func (d *Daemon) atDesk() bool {
	return time.Since(time.UnixMilli(d.deskAt.Load())) < deskIdle
}

// pushing tells whether a notice would go anywhere: web access serves,
// and no one is at the desktop.
func (d *Daemon) pushing() bool {
	if d.atDesk() {
		return false
	}
	if d.opts.Push != nil {
		return true
	}
	d.webMu.Lock()
	defer d.webMu.Unlock()
	return d.web != nil
}

// push sends a notice to the phones, through web access.
func (d *Daemon) push(n web.Notice) {
	n.Waiting = d.waitingSessions()
	if d.opts.Push != nil {
		d.opts.Push(n)
		return
	}
	d.webMu.Lock()
	s := d.web
	d.webMu.Unlock()
	if s != nil {
		s.Push(n)
	}
}

// waitingSessions counts the sessions that wait for the user.
func (d *Daemon) waitingSessions() int {
	waiting := map[string]bool{}
	for _, info := range d.reg.Infos() {
		if info.Status == session.Waiting {
			waiting[info.Session] = true
		}
	}
	return len(waiting)
}

// pushStatus tells the phones of a pane's change, as notice.For decides
// and the settings' notify allow: the desktop's own notifications follow
// the same.
func (d *Daemon) pushStatus(info session.Info, prevStatus session.Status) {
	if !d.pushing() {
		return
	}
	if p := d.reg.Get(info.ID); p != nil {
		if now := p.Info(); now.Status == info.Status {
			info = now
		}
	}
	if d.reg.Viewed() == info.Session {
		// On the screen of a desktop no one uses: its work is unseen.
		info.Unread = true
	}
	prev := info
	prev.Status = prevStatus
	kind := notice.For(prev, info, false, d.notifyPrefs().Prefs)
	state := map[notice.Kind]string{notice.Waiting: notice.WaitingWords(info.Kind), notice.Failed: "Failed", notice.Done: "Done"}[kind]
	if state == "" {
		return
	}
	d.push(web.Notice{Session: info.Session, Pane: info.ID, Title: d.sessionLabel(info.Session), State: state,
		Words: info.Message, Urgent: kind != notice.Done})
}

// pushProgram passes on a program's own notification (OSC 9, 777) when
// the settings let programs notify; only a device that asked for them
// takes it. Any program in a pane can send one, so it comes under its
// session's name, and never in omaterm's place.
func (d *Daemon) pushProgram(paneID, title, body string) {
	if !d.pushing() || !d.notifyPrefs().Programs {
		return
	}
	p := d.reg.Get(paneID)
	if p == nil {
		return
	}
	sid := p.Info().Session
	words := body
	if title != "" && body != "" {
		words = title + ": " + body
	} else if title != "" {
		words = title
	}
	d.push(web.Notice{Session: sid, Pane: paneID, Title: d.sessionLabel(sid), State: "Sent a notification", Words: words, Program: true})
}

// pushProcess tells the phones of a process that failed, or that failed
// so often its restarts paused. Its pane is gone with it: a tap opens the
// list.
func (d *Daemon) pushProcess(event procs.Event) {
	var state string
	switch event.Kind {
	case procs.EventFailed:
		state = "Failed (exit " + strconv.Itoa(event.Code) + ")"
	case procs.EventPaused:
		state = "Failed, restarts paused"
	default:
		return
	}
	if !d.pushing() || !d.notifyPrefs().Error {
		return
	}
	sid := procs.SessionID(event.Project, event.Name)
	title := d.sessionLabel(sid)
	if title == "omaterm" {
		title = event.Name
	}
	d.push(web.Notice{Session: sid, Title: title, State: state, Urgent: true})
}
