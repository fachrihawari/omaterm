package main

// A process that fails, or whose restarts were paused, tells the user:
// the same notification a session's failure sends, for a process the
// daemon runs with no session of its own in view (internal/procs).

import (
	"strconv"

	"omaterm/internal/procs"
)

// processNotice sends a process's notification: the app's own, which a
// test replaces to see what the user would have been told.
var processNotice = func(a *appState, sessionID, title, body string) {
	a.notify(sessionID, title, body)
}

// processEvent notifies the user of a process that failed or whose
// restarts were paused, as the daemon does while the app is closed: the
// daemon notifies only when no app is attached, so the two never both do.
//
// It runs on the UI thread, from the daemon's event hook or the local
// supervisor's, and says nothing of a start or a stop: the user asked for
// those.
func (a *appState) processEvent(event procs.Event) {
	if !a.settings.Notify.Error {
		return
	}
	var title, body string
	switch event.Kind {
	case procs.EventFailed:
		title = "Process failed: " + event.Name
		body = "exit " + strconv.Itoa(event.Code)
	case procs.EventPaused:
		title = "Restart paused: " + event.Name
		body = "restarted too often"
	default:
		return
	}
	sessionID := procs.SessionID(event.Project, event.Name)
	// One every 5 seconds a session, as a pane's own notifications are.
	if !a.programLimit.Allow(sessionID) {
		return
	}
	processNotice(a, sessionID, title, body)
}
