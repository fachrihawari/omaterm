package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"

	"omaterm/internal/agent"
	"omaterm/internal/pty"
)

// Handle is a pane as the app uses it: one this process runs (*Pane), or
// one a session daemon runs, which the app attaches to.
type Handle interface {
	ID() string
	Info() Info
	Changed() <-chan struct{}
	// Term is the terminal a view shows.
	Term() *terminal.Terminal
	// TermIfOpen is Term when the app has one, nil else: it never opens
	// one (for a daemon pane, an attach connection).
	TermIfOpen() *terminal.Terminal
	Write(b []byte) error
	// Send types for the omaterm command (see Pane.Send).
	Send(ctx context.Context, data string, paste bool) error
	Text(scrollback bool, lines int) string
	History(force bool) []byte
	Wait(ctx context.Context, until func(Info) bool) (Info, error)
	Flag(title, body string)
	Report(body string) error
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

// Await waits for a pane: until its command ends (exit), its status is
// one of statuses, or else until it stops working. A pane that has not
// started to work gets a moment to, as an agent just prompted. The error
// is ctx's.
func Await(ctx context.Context, p Handle, statuses []string, exit bool) (Info, error) {
	switch {
	case exit:
		return p.Wait(ctx, func(i Info) bool { return i.CommandExit != nil || i.Status == Exited })
	case len(statuses) > 0:
		return p.Wait(ctx, func(i Info) bool {
			for _, s := range statuses {
				if string(i.Status) == s {
					return true
				}
			}
			return false
		})
	}
	if info := p.Info(); info.Status != Working {
		grace, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		p.Wait(grace, func(i Info) bool { return i.Status == Working })
		cancel()
	}
	return p.Wait(ctx, func(i Info) bool { return i.Status != Working })
}

// FindAgentPane finds the pane an agent's hook ran in: the pane whose
// shell is an ancestor of pid. Else the hook ran away from its pane, and
// so do the agent's commands (away), as for OpenCode's service, or Claude
// Code's background sessions, which a pane only attaches to:
//   - the pane a process naming the conversation runs in, as `claude
//     attach <id>` does;
//   - else the one pane in dir that runs the agent without a conversation
//     yet. In another folder, or one of several, it is a guess: none.
func FindAgentPane(h Host, pid int, agentName, sessionID, dir string) (found Handle, away bool) {
	shells := map[int]Handle{}
	h.Each(func(p Handle) {
		if pid := p.Info().PID; pid > 0 {
			shells[pid] = p
		}
	})
	paneOf := func(pid int) Handle {
		for n := 0; pid > 1 && n < 64; pid, n = pty.ProcParent(pid), n+1 {
			if p := shells[pid]; p != nil {
				return p
			}
		}
		return nil
	}
	if p := paneOf(pid); p != nil {
		return p, false
	}
	if sessionID != "" {
		named := map[Handle]bool{}
		for _, proc := range pty.Procs() {
			if namesSession(pty.ProcArgs(proc), sessionID) {
				if p := paneOf(proc); p != nil {
					named[p] = true
				}
			}
		}
		if len(named) == 1 {
			for p := range named {
				return p, true
			}
		}
	}
	var cands []Handle
	h.Each(func(p Handle) {
		i := p.Info()
		if i.Agent == agentName && i.AgentSession == nil && dir != "" && i.WorkDir() == dir {
			cands = append(cands, p)
		}
	})
	if len(cands) != 1 {
		return nil, false
	}
	return cands[0], true
}

// namesSession reports whether a command line names conversation id: the
// whole id, or a prefix of it of 8 or more, as `claude attach 8f0d0898`.
func namesSession(args []string, id string) bool {
	for _, a := range args[min(1, len(args)):] {
		if len(a) >= 8 && strings.HasPrefix(id, a) {
			return true
		}
	}
	return false
}

// RestoredHistory is a pane's saved scrollback, at path, under a dim line
// saying when its session ended; nil when none was saved.
// SaveHistory writes a pane's scrollback to path, through a file of a
// name of its own beside it: the app and a daemon writing one pane's
// history at once shared one ".tmp" name, and mixed their writes.
func SaveHistory(path string, h []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(h)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

func RestoredHistory(path string) []byte {
	h, err := os.ReadFile(path)
	if err != nil || len(h) == 0 {
		return nil
	}
	when := time.Now()
	if st, err := os.Stat(path); err == nil {
		when = st.ModTime()
	}
	return append(h, []byte("\x1b[2m── restored · session ended "+when.Format("Mon Jan 2 15:04")+" ──\x1b[0m\r\n")...)
}
