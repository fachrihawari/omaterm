package main

import (
	"context"
	"time"

	"omaterm/internal/session"
)

// startPanes runs the panes' tick and follows their changes into the
// views: a change is coalesced for 25 ms, then copied to a.status on the
// UI thread.
func (a *appState) startPanes() {
	ctx, cancel := context.WithCancel(context.Background())
	a.panesStop = cancel
	go a.panes.Run(ctx)
	go a.git.run(ctx, func() { a.post(func() {}) })
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.panes.Changed():
				time.Sleep(25 * time.Millisecond)
				a.post(a.syncStatus)
			}
		}
	}()
}

// trackViewed tells the panes which session the user looks at: the one
// shown while the window has the focus. Its panes' changes are seen as
// they come, and its unread marks go.
func (a *appState) trackViewed() {
	viewed := a.viewedSession()
	if a.win != nil && !a.win.IsFocused() {
		viewed = ""
	}
	if viewed != a.lastViewed {
		a.lastViewed = viewed
		a.panes.SetViewed(viewed)
	}
}

// hookPanes connects the registry's events to the app.
func (a *appState) hookPanes() {
	a.panes.OnExit = func(id string, code int) {
		a.post(func() { a.paneExited(id) })
	}
	a.panes.OnNotice = func(id, title, body string) {
		a.post(func() { a.programNotice(id, title, body) })
	}
}

// syncStatus copies every pane's Info for the views to read, and acts on
// what changed. UI thread.
func (a *appState) syncStatus() {
	next := a.panes.Infos()
	prev := a.status
	a.status = next
	a.statusChanged(prev, next)
	if a.host != nil {
		a.host.changed()
	}
	dirs := map[string]bool{}
	for _, i := range next {
		if d := i.WorkDir(); d != "" {
			dirs[d] = dirs[d] || i.Status == session.Working
		}
	}
	a.git.setDirs(dirs)
	if a.win != nil {
		if t := a.windowTitle(); t != a.title {
			a.title = t
			a.win.SetTitle(t)
		}
	}
	for id, n := range next {
		if prev[id].Dir != n.Dir {
			a.saveSoon()
			break
		}
	}
}

// statusChanged reacts to panes' changes: notifications (notify.go).
func (a *appState) statusChanged(prev, next map[string]session.Info) {
	for id, n := range next {
		a.maybeNotify(prev[id], n)
	}
}

// paneInfo is a pane's last synced Info.
func (a *appState) paneInfo(id string) (session.Info, bool) {
	i, ok := a.status[id]
	return i, ok
}

// sessionInfo is the Info that speaks for a terminal tab: its lead pane's.
func (a *appState) sessionInfo(tab topTab) (session.Info, bool) {
	if tab.Split == nil {
		return session.Info{}, false
	}
	var infos []session.Info
	for _, id := range tab.Split.termIDs() {
		if i, ok := a.status[id]; ok {
			infos = append(infos, i)
		}
	}
	return session.Lead(infos)
}
