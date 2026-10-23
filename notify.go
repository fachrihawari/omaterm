package main

import (
	"strconv"
	"time"

	"github.com/egoist/mygo"

	"omaterm/internal/notice"
	"omaterm/internal/session"
)

// noticeKind is why a session notifies (internal/notice, which the
// session daemon follows too while the window is closed).
type noticeKind = notice.Kind

const (
	noticeNone    = notice.None
	noticeWaiting = notice.Waiting
	noticeFailed  = notice.Failed
	noticeDone    = notice.Done
)

// noticeFor tells whether a pane's change is worth a notification.
func noticeFor(prev, next session.Info, here bool, prefs NotifySettings) noticeKind {
	return notice.For(prev, next, here, notice.Prefs(prefs))
}

// maybeNotify notifies of a pane's change while the user looks elsewhere,
// at most once every five seconds a session.
func (a *appState) maybeNotify(prev, next session.Info) {
	here := a.win != nil && a.win.IsFocused() && a.viewedSession() == next.Session
	kind := noticeFor(prev, next, here, a.settings.Notify)
	if kind == noticeNone {
		return
	}
	if t, ok := a.notified[next.Session]; ok && nowFunc().Sub(t) < 5*time.Second {
		return
	}
	a.notified[next.Session] = nowFunc()
	a.notify(next.Session, a.sessionLabel(next.Session), notice.Body(kind, next.Message))
}

// programNotice passes on a desktop notification a program sent (OSC 9,
// 777), unless its session is in view.
func (a *appState) programNotice(id, title, body string) {
	info, ok := a.paneInfo(id)
	if !ok || a.win == nil || a.win.IsFocused() && a.viewedSession() == info.Session {
		return
	}
	if title == "" {
		title = a.sessionLabel(info.Session)
	}
	a.notify(info.Session, title, body)
}

// notify shows a desktop notification for a session; clicking it shows
// the session.
func (a *appState) notify(sessionID, title, body string) {
	if a.win == nil || !mygo.NotificationsSupported() {
		return
	}
	n := mygo.NewNotification(mygo.NotificationOptions{ID: "omaterm-" + sessionID, Title: title, Body: body})
	n.OnClick(func() {
		a.post(func() { a.showSession(sessionID) })
	})
	n.Show()
}

// viewedSession is the session the window shows, "" for none.
func (a *appState) viewedSession() string {
	if a.activeView != tabTerminal || a.activeTab < 0 || a.activeTab >= len(a.tabs) {
		return ""
	}
	return a.tabs[a.activeTab].ID
}

// sessionLabel is a session's name and its project's, for a notification.
func (a *appState) sessionLabel(sessionID string) string {
	for pi, p := range a.projects {
		for _, t := range a.projectTabs(p.ID) {
			if t.ID == sessionID {
				info, _ := a.sessionInfo(t)
				return a.sessionName(t, info) + " · " + a.projects[pi].Name
			}
		}
	}
	return "omaterm"
}

// showSession brings the window up on a session.
func (a *appState) showSession(sessionID string) {
	for pi, p := range a.projects {
		for _, t := range a.projectTabs(p.ID) {
			if t.ID == sessionID {
				a.focusSession(pi, sessionID)
			}
		}
	}
	if a.win != nil {
		a.win.Show()
		a.win.Focus()
	}
}

// jumpToAttention shows the next session that needs the user: one
// waiting, else failed, else done unseen.
func (a *appState) jumpToAttention(c interface{ Toast(string) }) {
	for _, st := range []iconState{iconWaiting, iconFailed, iconDone} {
		if a.jumpTo(st) {
			return
		}
	}
	if c != nil {
		c.Toast("Nothing needs you")
	}
}

// windowTitle counts the sessions that need the user, as the dock badge
// of other terminals does.
func (a *appState) windowTitle() string {
	waiting, _, _ := a.statusCounts()
	if waiting > 0 {
		return "(" + strconv.Itoa(waiting) + ") omaterm"
	}
	return "omaterm"
}

// cycleSession shows the next (d = 1) or previous (-1) session in the
// sidebar's order, across projects.
func (a *appState) cycleSession(d int) {
	type ref struct {
		pi int
		id string
	}
	var all []ref
	cur := 0
	for pi, p := range a.projects {
		for ti, t := range a.projectTabs(p.ID) {
			if t.Kind != tabTerminal {
				continue
			}
			if pi == a.active && ti == a.activeTab {
				cur = len(all)
			}
			all = append(all, ref{pi, t.ID})
		}
	}
	if len(all) < 2 {
		return
	}
	r := all[(cur+d+len(all))%len(all)]
	a.focusSession(r.pi, r.id)
}
