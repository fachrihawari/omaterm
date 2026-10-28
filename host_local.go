package main

// The app's side of the control socket: what `omaterm <command>` and
// agents' hooks call. Calls that change sessions run on the UI thread,
// which owns the tabs; reading and waiting on a pane do not, so a long
// wait never holds the window.

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"omaterm/internal/agent"
	"omaterm/internal/ctl"
	"omaterm/internal/gitsnap"
	"omaterm/internal/mail"
	"omaterm/internal/model"
	"omaterm/internal/proto"
	"omaterm/internal/session"
	"omaterm/internal/shellcmd"
	"omaterm/service"
)

// localHost serves the control socket from the app.
type localHost struct {
	a       *appState
	started time.Time

	mu      sync.Mutex
	subs    map[int]func(proto.Message)
	nextSub int
	pending bool
	// mailbox is the mailbox of the app's own panes (mailFor).
	mailbox  *mail.Mailbox
	mailReg  *session.Registry
	mailStop context.CancelFunc
}

var _ ctl.Host = (*localHost)(nil)

func newLocalHost(a *appState) *localHost {
	return &localHost{a: a, started: time.Now(), subs: map[int]func(proto.Message){}}
}

// onUI runs fn on the UI thread and waits for it, five seconds at most.
// A call that waited longer is answered "busy" and never runs: run later,
// a create or close the caller was told failed happened anyway, and an
// agent that tried again made a second session.
func (h *localHost) onUI(fn func() (any, error)) (any, error) {
	return onUIWithin(h.a, 5*time.Second, h.a.win != nil, fn)
}

// onUIWithin is onUI waiting d; with no window (posted false), fn runs
// here.
func onUIWithin(a *appState, d time.Duration, posted bool, fn func() (any, error)) (any, error) {
	if !posted {
		return fn()
	}
	type result struct {
		v   any
		err error
	}
	const pending, running, abandoned = 0, 1, 2
	var state atomic.Int32
	ch := make(chan result, 1)
	a.post(func() {
		if !state.CompareAndSwap(pending, running) {
			return // answered "busy" already
		}
		v, err := fn()
		ch <- result{v, err}
	})
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(d):
		if state.CompareAndSwap(pending, abandoned) {
			return nil, proto.Errorf(proto.CodeBusy, "omaterm is busy")
		}
		r := <-ch // started just now: its answer is the one
		return r.v, r.err
	}
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v); err != nil {
			return v, proto.Errorf(proto.CodeUsage, "bad params: "+err.Error())
		}
	}
	return v, nil
}

// Handle runs a method.
func (h *localHost) Handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	a := h.a
	if mail.IsMethod(method) {
		return h.mail(ctx, method, raw)
	}
	if method == "worktree.merge" {
		p, err := decode[proto.MergeParams](raw)
		if err != nil {
			return nil, err
		}
		return h.mergeWorktree(ctx, p)
	}
	switch method {
	case "version":
		return map[string]any{"app": appVersion(), "proto": proto.Version}, nil
	case "state":
		return h.onUI(func() (any, error) { return a.protoState(h.started), nil })
	case "create", "split":
		p, err := decode[proto.CreateParams](raw)
		if err != nil {
			return nil, err
		}
		split := method == "split" || p.Split != ""
		if p.Worktree != nil {
			if split {
				return nil, proto.Errorf(proto.CodeUsage, "a split works in its session's folder: --worktree is for new")
			}
			return h.createWorktree(ctx, p)
		}
		return h.onUI(func() (any, error) {
			if split {
				return a.cliSplit(p)
			}
			return a.cliCreate(p)
		})
	case "close":
		p, err := decode[proto.IDParams](raw)
		if err != nil {
			return nil, err
		}
		return h.onUI(func() (any, error) { return true, a.cliCloseSession(p.ID) })
	case "pane.close":
		p, err := decode[proto.IDParams](raw)
		if err != nil {
			return nil, err
		}
		return h.onUI(func() (any, error) { return true, a.cliClosePane(p.ID) })
	case "restart":
		p, err := decode[proto.IDParams](raw)
		if err != nil {
			return nil, err
		}
		return h.onUI(func() (any, error) { return true, a.cliRestart(p.ID) })
	case "rename":
		p, err := decode[proto.RenameParams](raw)
		if err != nil {
			return nil, err
		}
		return h.onUI(func() (any, error) { return true, a.cliRename(p) })
	case "pane.wake":
		p, err := decode[proto.IDParams](raw)
		if err != nil {
			return nil, err
		}
		return h.onUI(func() (any, error) { return true, a.wakePane(p.ID) })
	case "focus", "select":
		p, err := decode[proto.IDParams](raw)
		if err != nil {
			return nil, err
		}
		return h.onUI(func() (any, error) { return true, a.cliFocus(p.ID) })
	case "send":
		p, err := decode[proto.SendParams](raw)
		if err != nil {
			return nil, err
		}
		pane, err := h.pane(p.ID)
		if err != nil {
			return nil, err
		}
		return true, pane.Write([]byte(p.Data))
	case "read":
		p, err := decode[proto.ReadParams](raw)
		if err != nil {
			return nil, err
		}
		pane, err := h.pane(p.ID)
		if err != nil {
			return nil, err
		}
		return proto.ReadResult{Text: pane.Text(p.Scrollback, p.Lines)}, nil
	case "wait":
		p, err := decode[proto.WaitParams](raw)
		if err != nil {
			return nil, err
		}
		pane, err := h.pane(p.ID)
		if err != nil {
			return nil, err
		}
		return waitPane(ctx, pane, p)
	case "notify":
		p, err := decode[proto.NotifyParams](raw)
		if err != nil {
			return nil, err
		}
		pane, err := h.pane(p.ID)
		if err != nil {
			return nil, err
		}
		pane.Flag(p.Title, p.Body)
		return true, nil
	case "pane.agentSession":
		p, err := decode[proto.AgentSessionParams](raw)
		if err != nil {
			return nil, err
		}
		return a.agentSession(p)
	case "project.create":
		p, err := decode[proto.ProjectParams](raw)
		if err != nil {
			return nil, err
		}
		return h.onUI(func() (any, error) {
			i := a.addProject(p.Dir, p.Name)
			if i < 0 {
				return nil, proto.Errorf(proto.CodeUsage, errStateUnreadable.Error())
			}
			pr := a.projects[i]
			return proto.Project{ID: pr.ID, Name: pr.Name, Dir: pr.Path}, nil
		})
	case "actions":
		var list []map[string]string
		for _, c := range commandRegistry {
			if remoteAction(c.ID) {
				list = append(list, map[string]string{"ID": c.ID, "Title": c.Category + ": " + c.Title})
			}
		}
		return list, nil
	case "do":
		p, err := decode[proto.DoParams](raw)
		if err != nil {
			return nil, err
		}
		if !a.remoteControl.Load() {
			return nil, proto.Errorf(proto.CodeRemoteControl, "Agents can't control the app: turn on “Let Agents Control the App” in Settings › Agents")
		}
		if !remoteAction(p.Action) {
			return nil, proto.Errorf(proto.CodeNotFound, "no action "+p.Action+": omaterm actions lists them")
		}
		return h.onUI(func() (any, error) {
			// Commands run with the window's context: the next frame runs it.
			a.queuedActions = append(a.queuedActions, p.Action)
			if a.win != nil {
				a.win.Update(func() {})
			}
			return true, nil
		})
	}
	if v, err, ok := h.handleMore(ctx, method, raw); ok {
		return v, err
	}
	return nil, proto.Errorf(proto.CodeUsage, "no method "+method)
}

// pane returns a running pane by id.
func (h *localHost) pane(id string) (session.Handle, error) {
	p := h.a.offPanes().Get(id)
	if p == nil {
		return nil, proto.Errorf(proto.CodeNotFound, "no pane "+id)
	}
	return p, nil
}

// waitPane waits for a pane as WaitParams say (session.Await).
func waitPane(ctx context.Context, p session.Handle, w proto.WaitParams) (proto.Pane, error) {
	if w.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(w.TimeoutMS)*time.Millisecond)
		defer cancel()
	}
	info, err := session.Await(ctx, p, w.Status, w.Exit)
	return info, timeoutErr(err)
}

func timeoutErr(err error) error {
	switch {
	case errors.Is(err, session.ErrClosed):
		return proto.Errorf(proto.CodeNotFound, "pane closed")
	case err != nil:
		return proto.Errorf(proto.CodeTimeout, "timed out")
	}
	return nil
}

// Subscribe adds a subscriber of the state.
func (h *localHost) Subscribe(fn func(proto.Message)) func() {
	h.mu.Lock()
	h.nextSub++
	id := h.nextSub
	h.subs[id] = fn
	h.mu.Unlock()
	h.changed()
	return func() {
		h.mu.Lock()
		delete(h.subs, id)
		h.mu.Unlock()
	}
}

// changed sends the state to the subscribers, once a burst of changes
// settles (25 ms).
func (h *localHost) changed() {
	h.mu.Lock()
	if h.pending || len(h.subs) == 0 {
		h.mu.Unlock()
		return
	}
	h.pending = true
	h.mu.Unlock()
	time.AfterFunc(25*time.Millisecond, func() {
		v, err := h.onUI(func() (any, error) { return h.a.protoState(h.started), nil })
		h.mu.Lock()
		h.pending = false
		subs := make([]func(proto.Message), 0, len(h.subs))
		for _, fn := range h.subs {
			subs = append(subs, fn)
		}
		h.mu.Unlock()
		if err != nil {
			return
		}
		st := v.(proto.State)
		for _, fn := range subs {
			fn(proto.Message{Event: "state", State: &st})
		}
	})
}

// remoteAction tells the commands `omaterm do` may run: not the ones that
// quit, or touch files or projects; nor those that end every session
// (daemon.restart), write outside omaterm (agents.setup into the agents'
// settings, cli.install onto the PATH), or let a device in (web.pair). An
// agent, or a prompt injected into one, could run them unasked.
func remoteAction(id string) bool {
	switch {
	case id == "app.quit", id == "app.openProject", strings.HasPrefix(id, "git."), id == "file.save",
		id == "daemon.restart", id == "agents.setup", id == "cli.install", id == "web.pair":
		return false
	}
	for _, c := range commandRegistry {
		if c.ID == id {
			return true
		}
	}
	return false
}

// --- what the methods do, on the UI thread --------------------------------

// protoState is the state as the socket tells it.
func (a *appState) protoState(started time.Time) proto.State {
	st := proto.State{Viewed: a.viewedSession(), Started: started.UnixMilli()}
	for _, p := range a.projects {
		st.Projects = append(st.Projects, proto.Project{ID: p.ID, Name: p.Name, Dir: p.Path})
		focus := ""
		if p.ID == a.wsOwner {
			focus = a.activePaneTermID
		} else if w := a.ws[p.ID]; w != nil {
			focus = w.activePane
		}
		for _, t := range a.projectTabs(p.ID) {
			if t.Kind != tabTerminal || t.Split == nil {
				continue
			}
			s := proto.Session{ID: t.ID, Project: p.ID, Zoomed: t.Zoomed, Layout: toLayout(t.Split), AgentNamed: t.AgentNamed,
				Worktree: t.Worktree, Parent: t.Parent}
			for _, id := range t.Split.termIDs() {
				info, ok := a.status[id]
				if a.isBrowser(id) {
					info, ok = a.browserInfo(id, t.ID), true
				}
				if !ok {
					if pane := a.panes.Get(id); pane != nil {
						info = pane.Info()
					} else {
						info = session.Info{ID: id, Session: t.ID, Status: session.Idle, Progress: -1}
					}
				}
				s.Panes = append(s.Panes, info)
				if id == focus {
					s.Focus = id
				}
			}
			if s.Focus == "" && len(s.Panes) > 0 {
				s.Focus = s.Panes[0].ID
			}
			s.Lead, _ = session.Lead(s.Panes)
			s.Name = a.sessionName(t, s.Lead)
			st.Sessions = append(st.Sessions, s)
		}
	}
	proto.DropGoneParents(st.Sessions)
	return st
}

// projectFor finds the project a new session goes in: the one named, else
// the one holding dir (the deepest), else a new one for dir's repository,
// else the one shown.
func (a *appState) projectFor(ref, dir string) int {
	if ref != "" {
		for i, p := range a.projects {
			if p.ID == ref || strings.EqualFold(p.Name, ref) {
				return i
			}
		}
	}
	if dir != "" {
		best, bestLen := -1, -1
		for i, p := range a.projects {
			if within(dir, p.Path) && len(p.Path) > bestLen {
				best, bestLen = i, len(p.Path)
			}
		}
		if best >= 0 {
			return best
		}
		root := service.RepoRoot(dir)
		if root == "" {
			root = dir
		}
		if !service.IsWorkspaceTooBroad(root) {
			return a.addProject(root, ref)
		}
	}
	if a.active >= 0 {
		return a.active
	}
	return 0
}

func within(dir, root string) bool {
	return dir == root || strings.HasPrefix(dir, strings.TrimSuffix(root, "/")+"/")
}

// addProject adds a folder as a project, unless it is one; it returns its
// index, -1 when projects can't be added: while the sessions file can't be
// read, nothing is saved, and the sessions of a project added then were
// ended as unknown at the next launch.
func (a *appState) addProject(dir, name string) int {
	for i, p := range a.projects {
		if p.Path == dir {
			return i
		}
	}
	if a.stateLoadFailed != "" {
		a.toast(nil, errStateUnreadable.Error())
		return -1
	}
	if name == "" {
		name = filepath.Base(dir)
	}
	a.projects = append(a.projects, project{ID: newID(), Name: name, Path: dir})
	a.saveSoon()
	return len(a.projects) - 1
}

// errStateUnreadable is why no project can be added while the sessions
// file can't be read.
var errStateUnreadable = errors.New("omaterm can't save sessions while its sessions file can't be read: fix it, or remove it, and restart omaterm")

// cliCreate makes a session, and starts its pane at once: its command
// runs whether its project shows or not.
func (a *appState) cliCreate(p proto.CreateParams) (any, error) {
	if len(a.projects) == 0 && p.Dir == "" {
		return nil, proto.Errorf(proto.CodeUsage, "no project: give a directory")
	}
	pi := a.projectFor(p.Project, p.Dir)
	if pi < 0 || pi >= len(a.projects) {
		return nil, proto.Errorf(proto.CodeNotFound, "no project "+p.Project)
	}
	proj := &a.projects[pi]
	if proj.ID == "" {
		proj.ID = newID()
	}
	name := p.Name
	if name == "" {
		name = shellcmd.Name(shellcmd.Login())
	}
	parent := ""
	if _, ptabs, pti, ok := a.findPane(p.By); ok && p.By != "" {
		parent = (*ptabs)[pti].ID
	}
	tabs := a.workspaceTabs(proj)
	paneID := newID()
	tab := topTab{ID: newID(), Kind: tabTerminal, Label: name, Dot: dotGreen, Split: &splitNode{TermID: paneID},
		Created: nowFunc().UnixMilli(), Parent: parent}
	a.insertTab(proj, tabs, tab)
	dir := p.Dir
	if dir == "" {
		dir = proj.Path
	}
	a.paneDirs[paneID] = dir
	if p.URL != "" {
		a.paneURLs[paneID] = proto.BrowserURL(p.URL)
	} else if _, err := a.startPane(paneID, tab.ID, dir, p.Command, false, p.Env...); err != nil {
		a.paneFailed[paneID] = true
		return nil, err
	}
	if p.Focus {
		a.focusSession(pi, tab.ID)
	}
	a.saveSoon()
	return proto.Created{Session: tab.ID, Pane: paneID}, nil
}

// insertTab adds a session to project p's tabs: last, or last under the
// session that started it, so that the tab bar and the keys go through
// them as the sidebar shows them.
func (a *appState) insertTab(p *project, tabs *[]topTab, tab topTab) {
	at := len(*tabs)
	if tab.Parent != "" {
		at = model.TreeInsert(len(*tabs), func(i int) string { return (*tabs)[i].ID },
			func(i int) string { return (*tabs)[i].Parent }, tab.Parent)
	}
	*tabs = slices.Insert(*tabs, at, tab)
	switch w := a.ws[p.ID]; {
	case p.ID == a.wsOwner:
		if at <= a.activeTab && a.activeTab < len(*tabs)-1 {
			a.activeTab++
		}
	case w != nil && &w.tabs == tabs:
		if at <= w.activeTab && w.activeTab < len(*tabs)-1 {
			w.activeTab++
		}
	}
}

// findPane finds the project and tab holding a pane, among all projects'.
func (a *appState) findPane(id string) (pi int, tabs *[]topTab, ti int, ok bool) {
	for pi := range a.projects {
		tabs := a.projectTabsPtr(&a.projects[pi])
		if tabs == nil {
			continue
		}
		for ti, t := range *tabs {
			if t.Split != nil && t.Split.contains(id) {
				return pi, tabs, ti, true
			}
		}
	}
	return 0, nil, 0, false
}

// wakePane starts a pane of any project that the window has not shown
// since it opened, as web access asks before showing it. UI thread.
func (a *appState) wakePane(id string) error {
	pi, tabs, ti, ok := a.findPane(id)
	if !ok {
		return proto.Errorf(proto.CodeNotFound, "no pane "+id)
	}
	if a.isBrowser(id) {
		return proto.Errorf(proto.CodeNotFound, "pane "+id+" is a browser, not a terminal")
	}
	if a.panes.Get(id) != nil {
		return nil
	}
	delete(a.paneFailed, id)
	_, err := a.startRestored(id, (*tabs)[ti].ID, a.projects[pi].Path)
	return err
}

// findSession finds the project and tab of a session.
func (a *appState) findSession(id string) (pi int, tabs *[]topTab, ti int, ok bool) {
	for pi := range a.projects {
		tabs := a.projectTabsPtr(&a.projects[pi])
		if tabs == nil {
			continue
		}
		for ti, t := range *tabs {
			if t.ID == id {
				return pi, tabs, ti, true
			}
		}
	}
	return 0, nil, 0, false
}

// projectTabsPtr is a project's tabs to change, its saved sessions'
// for a project not shown this run; nil for one with none.
func (a *appState) projectTabsPtr(p *project) *[]topTab {
	if p.ID == a.wsOwner {
		return &a.tabs
	}
	if a.ws[p.ID] == nil && len(a.saved[p.ID]) == 0 {
		return nil
	}
	return a.workspaceTabs(p)
}

// cliSplit puts a new pane beside one, and starts it.
func (a *appState) cliSplit(p proto.CreateParams) (any, error) {
	pi, tabs, ti, ok := a.findPane(p.Split)
	if !ok {
		if _, stabs, sti, sok := a.findSession(p.Split); sok {
			tabs, ti, ok = stabs, sti, true
			if ids := (*stabs)[sti].Split.termIDs(); len(ids) > 0 {
				p.Split = ids[0]
			}
		}
	}
	if !ok {
		return nil, proto.Errorf(proto.CodeNotFound, "no pane "+p.Split)
	}
	_ = pi
	tab := &(*tabs)[ti]
	dir := splitVertical
	before := false
	switch p.Direction {
	case "down":
		dir = splitHorizontal
	case "left":
		before = true
	case "up":
		dir, before = splitHorizontal, true
	}
	ratio := p.Ratio
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	paneID := newID()
	tab.Split = splitAt(tab.Split, p.Split, paneID, dir, before, ratio)
	tab.Zoomed = ""
	from := a.status[p.Split]
	workDir := p.Dir
	if workDir == "" {
		workDir = from.WorkDir()
	}
	if workDir == "" {
		workDir = a.paneDirs[p.Split]
	}
	a.paneDirs[paneID] = workDir
	if p.URL != "" {
		a.paneURLs[paneID] = proto.BrowserURL(p.URL)
	} else if workDir, err := tab.Worktree.PaneDir(workDir); err != nil {
		a.paneFailed[paneID] = true
		return nil, err
	} else if _, err := a.startPane(paneID, tab.ID, workDir, p.Command, false, p.Env...); err != nil {
		a.paneFailed[paneID] = true
		return nil, err
	}
	if p.Focus {
		a.focusSession(pi, tab.ID)
		a.focusPane(paneID)
	}
	a.saveSoon()
	return proto.Created{Session: tab.ID, Pane: paneID}, nil
}

// splitAt splits the pane target, the new pane before or after it, the
// new one taking ratio of the room.
func splitAt(root *splitNode, target, newID string, dir splitDir, before bool, ratio float64) *splitNode {
	var walk func(n *splitNode) *splitNode
	walk = func(n *splitNode) *splitNode {
		if n == nil {
			return nil
		}
		if n.isLeaf() {
			if n.TermID != target {
				return n
			}
			old, nw := &splitNode{TermID: target}, &splitNode{TermID: newID}
			s := &splitNode{Dir: dir}
			if before {
				s.First, s.Second, s.Ratio = nw, old, float32(ratio*100)
			} else {
				s.First, s.Second, s.Ratio = old, nw, float32((1-ratio)*100)
			}
			return s
		}
		n.First, n.Second = walk(n.First), walk(n.Second)
		return n
	}
	return walk(root)
}

func (a *appState) cliCloseSession(id string) error {
	pi, tabs, ti, ok := a.findSession(id)
	if !ok {
		return proto.Errorf(proto.CodeNotFound, "no session "+id)
	}
	if a.projects[pi].ID == a.wsOwner {
		a.closeTab(ti)
		return nil
	}
	for _, pid := range (*tabs)[ti].Split.termIDs() {
		a.closePaneRuntime(pid)
	}
	*tabs = append((*tabs)[:ti], (*tabs)[ti+1:]...)
	// The project's kept workspace shows its tab again later: keep that
	// tab pointing at one that is there.
	if w := a.ws[a.projects[pi].ID]; w != nil {
		if ti < w.activeTab || w.activeTab >= len(w.tabs) {
			w.activeTab--
		}
		if w.activeTab < 0 && len(w.tabs) > 0 {
			w.activeTab = 0
		}
		if !w.hasPane(w.activePane) {
			w.activePane = ""
		}
	}
	a.saveSoon()
	return nil
}

func (a *appState) cliClosePane(id string) error {
	pi, tabs, ti, ok := a.findPane(id)
	if !ok {
		return proto.Errorf(proto.CodeNotFound, "no pane "+id)
	}
	if (*tabs)[ti].Split.isLeaf() {
		return a.cliCloseSession((*tabs)[ti].ID)
	}
	if a.projects[pi].ID == a.wsOwner {
		a.removePaneIn(ti, id)
		return nil
	}
	root, _ := removeLeaf((*tabs)[ti].Split, id)
	rebalance(root)
	(*tabs)[ti].Split = root
	a.closePaneRuntime(id)
	if w := a.ws[a.projects[pi].ID]; w != nil && w.activePane == id {
		w.activePane = ""
		if ids := root.termIDs(); len(ids) > 0 {
			w.activePane = ids[0]
		}
	}
	a.saveSoon()
	return nil
}

// cliRestart starts a pane's shell again, in its directory.
func (a *appState) cliRestart(id string) error {
	_, tabs, ti, ok := a.findPane(id)
	if !ok {
		return proto.Errorf(proto.CodeNotFound, "no pane "+id)
	}
	if a.isBrowser(id) {
		return proto.Errorf(proto.CodeNotFound, "pane "+id+" is a browser, not a terminal")
	}
	info := a.status[id]
	dir := info.Dir
	if dir == "" {
		dir = a.paneDirs[id]
	}
	dir, err := (*tabs)[ti].Worktree.PaneDir(dir)
	if err != nil {
		return err
	}
	a.panes.Remove(id)
	delete(a.paneFailed, id)
	// The command it started with runs again: a dev server restarts as
	// itself, not as a bare shell.
	_, err = a.startPane(id, (*tabs)[ti].ID, dir, info.Command, false)
	return err
}

func (a *appState) cliRename(p proto.RenameParams) error {
	_, tabs, ti, ok := a.findSession(p.ID)
	if !ok {
		return proto.Errorf(proto.CodeNotFound, "no session "+p.ID)
	}
	t := &(*tabs)[ti]
	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = shellcmd.Name(shellcmd.Login())
	}
	t.Label = name
	t.AgentNamed = p.By != "" && a.status[p.By].Agent != ""
	a.saveSoon()
	return nil
}

// cliFocus shows a pane: its project, its session, and the pane.
func (a *appState) cliFocus(id string) error {
	pi, tabs, ti, ok := a.findPane(id)
	if !ok {
		if spi, stabs, sti, sok := a.findSession(id); sok {
			pi, tabs, ti, ok = spi, stabs, sti, true
			id = ""
		}
	}
	if !ok {
		return proto.Errorf(proto.CodeNotFound, "no pane "+id)
	}
	a.focusSession(pi, (*tabs)[ti].ID)
	if id != "" {
		a.focusPane(id)
	}
	if a.win != nil {
		a.win.Show()
		a.win.Focus()
	}
	return nil
}

// agentSession records the conversation an agent runs in its pane, which
// the agent's SessionStart hook tells: the pane whose shell the hook's
// process descends from, else the one running that agent without one.
func (a *appState) agentSession(p proto.AgentSessionParams) (any, error) {
	if !agent.SessionIDRe.MatchString(p.Session) {
		return nil, proto.Errorf(proto.CodeUsage, "bad session id")
	}
	found, away := session.FindAgentPane(a.offPanes(), p.PID, p.Agent, p.Session, p.Dir)
	if found == nil {
		return nil, proto.Errorf(proto.CodeNotFound, "no pane runs that agent")
	}
	found.SetAgentSession(&agent.Session{Agent: p.Agent, ID: p.Session})
	info := found.Info()
	a.post(a.saveSoon)
	return proto.AgentSessionResult{Pane: info.ID, Session: info.Session, Away: away}, nil
}

var _ = model.Version

// handleMore runs the methods of the browser and git.
func (h *localHost) handleMore(ctx context.Context, method string, raw json.RawMessage) (any, error, bool) {
	switch method {
	case "browser":
		p, err := decode[proto.BrowserParams](raw)
		if err != nil {
			return nil, err, true
		}
		type answer struct {
			r   *proto.BrowserResult
			err error
		}
		ch := make(chan answer, 1)
		if _, err := h.onUI(func() (any, error) {
			h.a.browse(p, func(r *proto.BrowserResult, err error) { ch <- answer{r, err} })
			return nil, nil
		}); err != nil {
			return nil, err, true
		}
		wait := time.Duration(p.TimeoutMS)*time.Millisecond + 30*time.Second
		if p.TimeoutMS <= 0 {
			wait = browseTimeout + 30*time.Second
		}
		select {
		case r := <-ch:
			return r.r, r.err, true
		case <-time.After(wait):
			return nil, proto.Errorf(proto.CodeTimeout, "the page did not answer"), true
		case <-ctx.Done():
			return nil, ctx.Err(), true
		}
	}
	return h.handleGit(ctx, method, raw)
}

// handleGit runs the git methods: a pane's changed files (git.changes)
// and its patch (git.diff), against a base.
func (h *localHost) handleGit(ctx context.Context, method string, raw json.RawMessage) (any, error, bool) {
	if method != "git.changes" && method != "git.diff" {
		return nil, nil, false
	}
	p, err := decode[proto.GitParams](raw)
	if err != nil {
		return nil, err, true
	}
	pane, err := h.pane(p.Pane)
	if err != nil {
		return nil, err, true
	}
	branch := ""
	if p.Base == gitsnap.BaseBranch {
		v, _ := h.onUI(func() (any, error) {
			if _, tabs, ti, ok := h.a.findPane(p.Pane); ok && (*tabs)[ti].Worktree != nil {
				return (*tabs)[ti].Worktree.Base, nil
			}
			return "", nil
		})
		branch, _ = v.(string)
	}
	v, err := gitsnap.Answer(ctx, method, p, pane.Info(), branch)
	return v, err, true
}
