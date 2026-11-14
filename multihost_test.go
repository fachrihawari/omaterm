package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"omaterm/internal/session"
)

// fakeHost is a session.Host for the composite's tests: it records starts
// and says how it was asked to behave.
type fakeHost struct {
	mu      sync.Mutex
	infos   map[string]session.Info
	starts  []session.StartOptions
	hooks   session.Hooks
	owned   bool
	changed chan struct{}
	ran     chan struct{}
	ranOnce sync.Once
}

func newFakeHost(owned bool) *fakeHost {
	return &fakeHost{infos: map[string]session.Info{}, owned: owned,
		changed: make(chan struct{}, 1), ran: make(chan struct{})}
}

func (h *fakeHost) Start(o session.StartOptions) (session.Handle, error) {
	h.mu.Lock()
	h.starts = append(h.starts, o)
	h.mu.Unlock()
	return nil, nil
}

func (h *fakeHost) Get(string) session.Handle { return nil }
func (h *fakeHost) Each(func(session.Handle)) {}
func (h *fakeHost) Remove(string)             {}
func (h *fakeHost) CloseAll()                 {}
func (h *fakeHost) SetViewed(string)          {}
func (h *fakeHost) Changed() <-chan struct{}  { return h.changed }
func (h *fakeHost) Tick(time.Time)            {}
func (h *fakeHost) Len() int                  { h.mu.Lock(); defer h.mu.Unlock(); return len(h.infos) }
func (h *fakeHost) Owned() bool               { return h.owned }
func (h *fakeHost) Infos() map[string]session.Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]session.Info, len(h.infos))
	for id, i := range h.infos {
		out[id] = i
	}
	return out
}
func (h *fakeHost) SetHooks(k session.Hooks) { h.mu.Lock(); h.hooks = k; h.mu.Unlock() }
func (h *fakeHost) Run(ctx context.Context) {
	h.ranOnce.Do(func() { close(h.ran) })
	<-ctx.Done()
}

func (h *fakeHost) startsOf() []session.StartOptions {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]session.StartOptions(nil), h.starts...)
}

func (h *fakeHost) notify() {
	select {
	case h.changed <- struct{}{}:
	default:
	}
}

func TestMultiHostRoutesPanesByHost(t *testing.T) {
	local := newFakeHost(true)
	m := newMultiHost(local)
	remote := newFakeHost(false)
	m.setRemote("devbox", remote)

	if _, err := m.Start(session.StartOptions{ID: "local1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(session.StartOptions{ID: "remote1", Host: "devbox"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(session.StartOptions{ID: "gone1", Host: "gone"}); err == nil {
		t.Fatal("a pane started on a host that isn't connected")
	}
	if got := local.startsOf(); len(got) != 1 || got[0].ID != "local1" {
		t.Fatalf("local starts: %+v", got)
	}
	if got := remote.startsOf(); len(got) != 1 || got[0].ID != "remote1" {
		t.Fatalf("remote starts: %+v", got)
	}
}

func TestMultiHostMergesInfosAndOwnership(t *testing.T) {
	local := newFakeHost(true)
	local.infos["a"] = session.Info{ID: "a"}
	m := newMultiHost(local)
	remote := newFakeHost(false)
	remote.infos["b"] = session.Info{ID: "b"}
	m.setRemote("devbox", remote)

	got := m.Infos()
	if len(got) != 2 || got["a"].ID != "a" || got["b"].ID != "b" {
		t.Fatalf("Infos: %+v", got)
	}
	if m.Owned() {
		t.Fatal("panes with a daemon host are owned by the app")
	}
	m.removeRemote("devbox")
	if !m.Owned() {
		t.Fatal("removing the remote host left the composite unowned")
	}
}

func TestMultiHostFollowsChangesAndHooks(t *testing.T) {
	local := newFakeHost(true)
	m := newMultiHost(local)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	select {
	case <-local.ran:
	case <-time.After(3 * time.Second):
		t.Fatal("the local host is never run")
	}
	exit := func(string, int) {}
	m.SetHooks(session.Hooks{OnExit: exit})
	local.mu.Lock()
	got := local.hooks.OnExit
	local.mu.Unlock()
	if got == nil {
		t.Fatal("hooks didn't reach the local host")
	}

	remote := newFakeHost(false)
	m.setRemote("devbox", remote)
	select {
	case <-remote.ran:
	case <-time.After(3 * time.Second):
		t.Fatal("the remote host is never run")
	}
	remote.mu.Lock()
	got = remote.hooks.OnExit
	remote.mu.Unlock()
	if got == nil {
		t.Fatal("hooks didn't reach a host added later")
	}
	remote.notify()
	select {
	case <-m.Changed():
	case <-time.After(3 * time.Second):
		t.Fatal("a host's change didn't reach the composite")
	}
}

func TestRemoteHostMethod(t *testing.T) {
	for _, method := range []string{"version", "state", "create", "split", "close", "send", "key", "read", "wait", "notify", "report", "project.create"} {
		if !remoteHostMethod(method) {
			t.Errorf("%s is refused to a remote host's command", method)
		}
	}
	for _, method := range []string{"do", "actions", "browser", "git.diff", "worktree.merge", "process.start", "mail.await"} {
		if remoteHostMethod(method) {
			t.Errorf("%s is allowed to a remote host's command", method)
		}
	}
}

func TestRemoteCommandCannotUseLocalFeatures(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	h := newLocalHost(a)
	for _, method := range []string{"do", "actions", "browser", "git.diff", "worktree.merge", "process.start", "mail.await"} {
		if _, err := h.HandleHost(context.Background(), "devbox", method, nil); err == nil {
			t.Errorf("%s was allowed to a remote host's command", method)
		}
	}
}

func TestRemoteProjectPaths(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	i := a.addProjectOn("devbox", "/srv/app", "")
	if i < 0 {
		t.Fatal("a remote project couldn't be added")
	}
	if a.projects[i].Host != "devbox" || a.projects[i].Path != "/srv/app" {
		t.Fatalf("project: %+v", a.projects[i])
	}
	if got := a.projectFor("", "/srv/app/src", "devbox"); got != i {
		t.Fatalf("projectFor found %d, want %d", got, i)
	}
	if got := a.addProjectOn("devbox", "relative", ""); got != -1 {
		t.Fatal("a remote project with a relative path was added")
	}
	st := a.state()
	found := false
	for _, p := range st.Projects {
		if p.ID == a.projects[i].ID && p.Host == "devbox" {
			found = true
		}
	}
	if !found {
		t.Fatal("the project's host wasn't saved in state")
	}
}
