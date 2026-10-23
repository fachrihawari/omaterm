package session

import (
	"context"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"omaterm/internal/agent"
)

// Handle is a pane as the app uses it: one this process runs (*Pane), or
// one a session daemon runs, which the app attaches to.
type Handle interface {
	ID() string
	Info() Info
	Changed() <-chan struct{}
	// Term is the terminal a view shows.
	Term() *terminal.Terminal
	Write(b []byte) error
	Text(scrollback bool, lines int) string
	History(force bool) []byte
	Wait(ctx context.Context, until func(Info) bool) (Info, error)
	Flag(title, body string)
	SetAgentSession(s *agent.Session)
	SetTurn(t *Turn)
}

// Hooks are what a Host tells the app: a pane's shell exited, a program
// sent a desktop notification, a pane's status changed. They run on a
// goroutine of their own.
type Hooks struct {
	OnExit   func(id string, code int)
	OnNotice func(id, title, body string)
	OnStatus func(info Info, prev Status)
}

// Host runs the app's panes: here (LocalHost), or in a session daemon.
type Host interface {
	Start(o StartOptions) (Handle, error)
	Get(id string) Handle
	Each(fn func(Handle))
	Infos() map[string]Info
	Len() int
	// Remove ends a pane; CloseAll ends this app's hold of every pane:
	// the panes themselves here, the attachments only of a daemon's,
	// whose shells go on.
	Remove(id string)
	CloseAll()
	SetViewed(sessionID string)
	Changed() <-chan struct{}
	Run(ctx context.Context)
	Tick(now time.Time)
	SetHooks(h Hooks)
	// Owned tells whether the panes end with the app (no daemon).
	Owned() bool
}

// LocalHost runs the panes in this process.
type LocalHost struct{ *Registry }

// NewLocalHost returns a host of panes run here.
func NewLocalHost() LocalHost { return LocalHost{NewRegistry()} }

func (h LocalHost) Start(o StartOptions) (Handle, error) {
	p, err := h.Registry.Start(o)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (h LocalHost) Get(id string) Handle {
	if p := h.Registry.Get(id); p != nil {
		return p
	}
	return nil
}

func (h LocalHost) Each(fn func(Handle)) { h.Registry.Each(func(p *Pane) { fn(p) }) }

func (h LocalHost) SetHooks(k Hooks) {
	h.Registry.OnExit, h.Registry.OnNotice, h.Registry.OnStatus = k.OnExit, k.OnNotice, k.OnStatus
}

func (h LocalHost) Owned() bool { return true }
