package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"omaterm/internal/daemon"
	"omaterm/internal/session"
)

// multiHost runs the panes of this computer and of remote hosts as one
// session.Host: the app reads one Infos map, follows one Changed channel
// and sets one set of hooks, wherever the panes run.
type multiHost struct {
	mu      sync.Mutex
	local   session.Host
	remotes map[string]session.Host
	order   []string
	hooks   session.Hooks
	any     chan struct{}
	ctx     context.Context
	// children are the hosts Run is following, with the context that
	// stops a child replaced by setLocal or setRemote.
	children map[string]childRun
}

type childRun struct {
	host   session.Host
	cancel context.CancelFunc
}

func newMultiHost(local session.Host) *multiHost {
	return &multiHost{
		local:    local,
		remotes:  map[string]session.Host{},
		any:      make(chan struct{}, 1),
		children: map[string]childRun{},
	}
}

// setLocal makes h the host of this computer's panes: the app's own
// registry without a daemon, or the daemon's Remote.
func (m *multiHost) setLocal(h session.Host) {
	m.mu.Lock()
	old := m.local
	m.local = h
	m.mu.Unlock()
	if old != nil && old != h {
		m.stopChild("local")
		if r, ok := old.(*daemon.Remote); ok {
			r.Close()
		}
	}
	m.startChild("local", h)
}

// setRemote adds or replaces a remote host's panes. id names the host in
// settings; panes routs to it by Session.StartOptions.Host.
func (m *multiHost) setRemote(id string, h session.Host) {
	m.mu.Lock()
	old, had := m.remotes[id]
	if !had {
		m.order = append(m.order, id)
	}
	m.remotes[id] = h
	m.mu.Unlock()
	if had {
		m.stopChild("remote:" + id)
	}
	if old != nil && old != h {
		if r, ok := old.(*daemon.Remote); ok {
			r.Close()
		}
	}
	m.startChild("remote:"+id, h)
}

// removeRemote lets go of a host: its panes keep running on it.
func (m *multiHost) removeRemote(id string) {
	m.mu.Lock()
	h := m.remotes[id]
	delete(m.remotes, id)
	for i, got := range m.order {
		if got == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	m.stopChild("remote:" + id)
	if r, ok := h.(*daemon.Remote); ok {
		r.Close()
	}
}

// remote returns a remote host's panes, nil when it isn't connected.
func (m *multiHost) remote(id string) session.Host {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.remotes[id]
}

// localRemote is the local daemon's panes, nil when panes run here.
func (m *multiHost) localRemote() *daemon.Remote {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, _ := m.local.(*daemon.Remote)
	return r
}

// localHost is this computer's panes, wherever they run.
func (m *multiHost) localHost() session.Host {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.local
}

// remoteRemotes are the remote daemons whose panes this app holds.
func (m *multiHost) remoteRemotes() []*daemon.Remote {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*daemon.Remote, 0, len(m.remotes))
	for _, id := range m.order {
		if r, ok := m.remotes[id].(*daemon.Remote); ok {
			out = append(out, r)
		}
	}
	return out
}

// hostOfPane tells which host runs a pane: "" for this computer, a host
// ID for another, and "?" when no host runs it.
func (m *multiHost) hostOfPane(id string) string {
	m.mu.Lock()
	local := m.local
	remotes := make(map[string]session.Host, len(m.remotes))
	for k, v := range m.remotes {
		remotes[k] = v
	}
	m.mu.Unlock()
	if local != nil && local.Get(id) != nil {
		return ""
	}
	for host, h := range remotes {
		if h.Get(id) != nil {
			return host
		}
	}
	return "?"
}

func (m *multiHost) startChild(key string, h session.Host) {
	if h == nil {
		return
	}
	m.mu.Lock()
	if c, ok := m.children[key]; ok && c.host == h {
		m.mu.Unlock()
		return
	}
	if c, ok := m.children[key]; ok {
		c.cancel()
		delete(m.children, key)
	}
	ctx := m.ctx
	hooks := m.hooks
	m.mu.Unlock()
	h.SetHooks(hooks)
	if ctx == nil {
		return
	}
	childCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.children[key] = childRun{host: h, cancel: cancel}
	m.mu.Unlock()
	go h.Run(childCtx)
	go m.follow(childCtx, h)
}

func (m *multiHost) stopChild(key string) {
	m.mu.Lock()
	c, ok := m.children[key]
	delete(m.children, key)
	m.mu.Unlock()
	if ok {
		c.cancel()
	}
}

func (m *multiHost) follow(ctx context.Context, h session.Host) {
	ch := h.Changed()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			select {
			case m.any <- struct{}{}:
			default:
			}
		}
	}
}

// all returns the hosts to act on, local first.
func (m *multiHost) all() []session.Host {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]session.Host, 0, len(m.remotes)+1)
	if m.local != nil {
		out = append(out, m.local)
	}
	for _, id := range m.order {
		if h := m.remotes[id]; h != nil {
			out = append(out, h)
		}
	}
	return out
}

func (m *multiHost) Start(o session.StartOptions) (session.Handle, error) {
	m.mu.Lock()
	h := m.local
	if o.Host != "" {
		h = m.remotes[o.Host]
	}
	m.mu.Unlock()
	if h == nil {
		if o.Host != "" {
			return nil, fmt.Errorf("host %s is not connected", o.Host)
		}
		return nil, fmt.Errorf("no panes host")
	}
	return h.Start(o)
}

func (m *multiHost) Get(id string) session.Handle {
	for _, h := range m.all() {
		if p := h.Get(id); p != nil {
			return p
		}
	}
	return nil
}

func (m *multiHost) Each(fn func(session.Handle)) {
	for _, h := range m.all() {
		h.Each(fn)
	}
}

func (m *multiHost) Infos() map[string]session.Info {
	out := map[string]session.Info{}
	for _, h := range m.all() {
		for id, i := range h.Infos() {
			out[id] = i
		}
	}
	return out
}

func (m *multiHost) Len() int {
	n := 0
	for _, h := range m.all() {
		n += h.Len()
	}
	return n
}

func (m *multiHost) Remove(id string) {
	for _, h := range m.all() {
		if h.Get(id) != nil {
			h.Remove(id)
			return
		}
	}
}

func (m *multiHost) CloseAll() {
	for _, h := range m.all() {
		h.CloseAll()
	}
}

func (m *multiHost) SetViewed(id string) {
	for _, h := range m.all() {
		h.SetViewed(id)
	}
}

func (m *multiHost) Changed() <-chan struct{} { return m.any }

func (m *multiHost) Run(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	local := m.local
	remotes := make(map[string]session.Host, len(m.remotes))
	for k, v := range m.remotes {
		remotes[k] = v
	}
	m.mu.Unlock()
	m.startChild("local", local)
	for id, h := range remotes {
		m.startChild("remote:"+id, h)
	}
	<-ctx.Done()
	m.mu.Lock()
	if m.ctx == ctx {
		m.ctx = nil
	}
	m.mu.Unlock()
}

func (m *multiHost) Tick(now time.Time) {
	for _, h := range m.all() {
		h.Tick(now)
	}
}

func (m *multiHost) SetHooks(h session.Hooks) {
	m.mu.Lock()
	m.hooks = h
	hosts := m.allLocked()
	m.mu.Unlock()
	for _, host := range hosts {
		host.SetHooks(h)
	}
}

// allLocked is all with m.mu held.
func (m *multiHost) allLocked() []session.Host {
	out := make([]session.Host, 0, len(m.remotes)+1)
	if m.local != nil {
		out = append(out, m.local)
	}
	for _, id := range m.order {
		if h := m.remotes[id]; h != nil {
			out = append(out, h)
		}
	}
	return out
}

func (m *multiHost) Owned() bool {
	m.mu.Lock()
	local := m.local
	remotes := make([]session.Host, 0, len(m.remotes))
	for _, h := range m.remotes {
		remotes = append(remotes, h)
	}
	m.mu.Unlock()
	if local == nil || !local.Owned() {
		return false
	}
	for _, h := range remotes {
		if !h.Owned() {
			return false
		}
	}
	return true
}

var _ session.Host = (*multiHost)(nil)
