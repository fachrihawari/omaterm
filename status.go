package main

import (
	"context"
	"time"

	"omaterm/internal/agent"
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
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				a.saveHistories(false)
				a.post(func() { a.detachHidden(now) })
			}
		}
	}()
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
	a.panes.SetHooks(session.Hooks{
		OnExit:   func(id string, code int) { a.post(func() { a.paneExited(id) }) },
		OnStatus: a.onStatus,
		OnNotice: func(id, title, body string) { a.post(func() { a.programNotice(id, title, body) }) },
	})
}

// syncStatus copies every pane's Info for the views to read, and acts on
// what changed. UI thread.
func (a *appState) syncStatus() {
	next := a.panes.Infos()
	prev := a.status
	a.status = next
	a.statusChanged(prev, next)
	a.keepAgents(next)
	a.offerAgentSetup(next)
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
	// What a restart needs is saved as it changes, not only as the window
	// closes, which a logout or a crash may skip: the folder, and the
	// agent to resume and its conversation.
	for id, n := range next {
		p := prev[id]
		if p.Dir != n.Dir || p.Agent != n.Agent || (p.AgentSession == nil) != (n.AgentSession == nil) {
			a.saveSoon()
			break
		}
	}
}

// keepAgents keeps, apart from the status, which agent each running pane
// runs and its conversation: a session daemon that restarts takes the
// panes' status with it, and they start again from what is kept here.
func (a *appState) keepAgents(infos map[string]session.Info) {
	for id, i := range infos {
		switch {
		case i.AgentSession != nil:
			a.paneAgents[id] = i.AgentSession
		case i.Agent != "":
			if ag := a.paneAgents[id]; ag == nil || ag.Agent != i.Agent {
				a.paneAgents[id] = &agent.Session{Agent: i.Agent}
			}
		case !a.restoring[id] && i.Process == "":
			// Back at its shell's prompt: the agent quit.
			delete(a.paneAgents, id)
		}
	}
}

// statusChanged reacts to panes' changes: notifications (notify.go).
func (a *appState) statusChanged(prev, next map[string]session.Info) {
	for id, n := range next {
		a.maybeNotify(prev[id], n)
		if p, ok := prev[id]; ok && n.Bells > p.Bells {
			a.ring(n)
		}
	}
}

// ring answers a program's bell as the settings say: the pane flashes,
// or a notification comes while its session is out of view.
func (a *appState) ring(info session.Info) {
	switch a.settings.Terminal.Bell {
	case "visual":
		if a.bells == nil {
			a.bells = map[string]time.Time{}
		}
		a.bells[info.ID] = nowFunc()
		if a.win != nil {
			a.win.Invalidate()
		}
	case "notify":
		if a.win != nil && a.win.IsFocused() && a.viewedSession() == info.Session {
			return
		}
		if t, ok := a.notified[info.Session]; ok && nowFunc().Sub(t) < 5*time.Second {
			return
		}
		a.notified[info.Session] = nowFunc()
		a.notify(info.Session, a.sessionLabel(info.Session), "The bell rang")
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
