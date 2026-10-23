// Package notice decides when a session's change is worth a desktop
// notification. The app follows it while its window runs, and the session
// daemon while the window is closed.
package notice

import (
	"sync"
	"time"

	"omaterm/internal/session"
)

// Kind is why a session notifies.
type Kind int

const (
	None Kind = iota
	Waiting
	Failed
	Done
)

// Prefs say which kinds notify (settings.json's notify).
type Prefs struct {
	Waiting bool `json:"waiting"`
	Done    bool `json:"done"`
	Error   bool `json:"error"`
}

// For tells whether a pane's change is worth a notification: it started
// waiting for the user, failed, or finished work unseen, and the user is
// not looking at it (here).
func For(prev, next session.Info, here bool, p Prefs) Kind {
	if here || prev.ID == "" {
		return None
	}
	switch {
	case next.Status == session.Waiting && prev.Status != session.Waiting && p.Waiting:
		return Waiting
	case next.Status == session.Failed && prev.Status != session.Failed && p.Error:
		return Failed
	case next.Status == session.Idle && prev.Status == session.Working && next.Unread && p.Done:
		return Done
	}
	return None
}

// Body is a notification's text: the program's message, else what
// happened.
func Body(k Kind, message string) string {
	if message != "" {
		return message
	}
	return map[Kind]string{Waiting: "Needs your input", Failed: "Failed", Done: "Finished"}[k]
}

// Limiter lets a session notify at most once every five seconds.
type Limiter struct {
	mu   sync.Mutex
	last map[string]time.Time
	// Now is the clock (tests); time.Now when nil.
	Now func() time.Time
}

// Allow tells whether a session may notify now, counting it if so.
func (l *Limiter) Allow(sessionID string) bool {
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.last[sessionID]; ok && now.Sub(t) < 5*time.Second {
		return false
	}
	if l.last == nil {
		l.last = map[string]time.Time{}
	}
	l.last[sessionID] = now
	return true
}
