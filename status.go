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
