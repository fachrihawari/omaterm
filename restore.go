package main

// Sessions outlive the app as far as an app can make them: each pane's
// scrollback is kept (sessions/<pane>.vt, every 10 seconds and as the
// window closes), and when the app starts again a pane comes back with it
// above a fresh shell, and an agent's conversation resumes. The programs
// that ran are not run again, unasked.

import (
	"path/filepath"

	"omaterm/internal/agent"
	"omaterm/internal/paths"
	"omaterm/internal/session"
)

func historyPath(id string) string {
	return filepath.Join(paths.StateDir(), "sessions", id+".vt")
}

// saveHistories writes the scrollback of the panes that changed, or of
// all of them.
func (a *appState) saveHistories(force bool) {
	if a.win == nil {
		return
	}
	a.offPanes().Each(func(p session.Handle) {
		h := p.History(force)
		if h == nil {
			return
		}
		a.writeHistory(p.ID(), h)
	})
}

// writeHistory writes a pane's scrollback, unless the pane was closed
// while it was read: a file written now would stay behind, the pane's
// output on disk long after it was closed.
func (a *appState) writeHistory(id string, h []byte) {
	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	if a.offPanes().Get(id) == nil {
		return
	}
	session.SaveHistory(historyPath(id), h)
}

// restoreOptions are what a pane coming back from the last run starts
// with: its scrollback under a line that says when it ended, and the
// command resuming its agent's conversation.
func (a *appState) restoreOptions(id string) (history []byte, command []string) {
	if !a.restoring[id] {
		return nil, nil
	}
	delete(a.restoring, id)
	if !a.settings.RestoreSessions {
		return nil, nil
	}
	history = session.RestoredHistory(historyPath(id))
	if a.settings.ResumeAgents && !a.serviceConflict && !a.daemonIgnored {
		command = agent.ResumeCommand(a.paneAgents[id])
	}
	return history, command
}
