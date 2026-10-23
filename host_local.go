package main

// The app's side of the control socket: what `omaterm <command>` and
// agents' hooks call. Calls that change sessions run on the UI thread,
// which owns the tabs; reading and waiting on a pane do not, so a long
// wait never holds the window.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"omaterm/internal/agent"
	"omaterm/internal/ctl"
	"omaterm/internal/model"
	"omaterm/internal/proto"
	"omaterm/internal/pty"
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
}

var _ ctl.Host = (*localHost)(nil)

func newLocalHost(a *appState) *localHost {
	return &localHost{a: a, started: time.Now(), subs: map[int]func(proto.Message){}}
}

// onUI runs fn on the UI thread and waits for it, five seconds at most.
func (h *localHost) onUI(fn func() (any, error)) (any, error) {
	if h.a.win == nil {
		return fn()
	}
	type result struct {
		v   any
		err error
	}
	ch := make(chan result, 1)
	h.a.post(func() {
		v, err := fn()
		ch <- result{v, err}
	})
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(5 * time.Second):
		return nil, proto.Errorf(proto.CodeBusy, "omaterm is busy")
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
		return h.onUI(func() (any, error) {
			if method == "split" || p.Split != "" {
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
		if !a.settings.RemoteControl {
			return nil, proto.Errorf(proto.CodeRemoteControl, "Remote Control is off: turn it on in the palette (Toggle Remote Control)")
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
func (h *localHost) pane(id string) (*session.Pane, error) {
	p := h.a.panes.Get(id)
	if p == nil {
		return nil, proto.Errorf(proto.CodeNotFound, "no pane "+id)
	}
	return p, nil
}

// waitPane waits for a pane as WaitParams say: until its command ends
// (Exit), its status is one of Status, or it stops working. A pane that
// has not started to work gets a moment to, as an agent just prompted.
func waitPane(ctx context.Context, p *session.Pane, w proto.WaitParams) (proto.Pane, error) {
	if w.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(w.TimeoutMS)*time.Millisecond)
		defer cancel()
	}
	switch {
	case w.Exit:
		info, err := p.Wait(ctx, func(i session.Info) bool { return i.CommandExit != nil || i.Status == session.Exited })
		return info, timeoutErr(err)
	case len(w.Status) > 0:
		info, err := p.Wait(ctx, func(i session.Info) bool {
			for _, s := range w.Status {
				if string(i.Status) == s {
					return true
				}
			}
			return false
		})
		return info, timeoutErr(err)
	}
	if info := p.Info(); info.Status != session.Working {
		grace, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		p.Wait(grace, func(i session.Info) bool { return i.Status == session.Working })
		cancel()
	}
	info, err := p.Wait(ctx, func(i session.Info) bool { return i.Status != session.Working })
	return info, timeoutErr(err)
}

func timeoutErr(err error) error {
	if err != nil {
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
// quit, or touch files or projects.
func remoteAction(id string) bool {
	switch {
	case id == "app.quit", id == "app.openProject", strings.HasPrefix(id, "git."), id == "file.save":
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
			s := proto.Session{ID: t.ID, Project: p.ID, Zoomed: t.Zoomed, Layout: toLayout(t.Split), AgentNamed: t.AgentNamed}
			for _, id := range t.Split.termIDs() {
				info, ok := a.status[id]
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
// index.
func (a *appState) addProject(dir, name string) int {
	for i, p := range a.projects {
		if p.Path == dir {
			return i
		}
	}
	if name == "" {
		name = filepath.Base(dir)
	}
	a.projects = append(a.projects, project{ID: newID(), Name: name, Path: dir})
	a.saveSoon()
	return len(a.projects) - 1
}

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
	tabs := a.workspaceTabs(proj)
	paneID := newID()
	tab := topTab{ID: newID(), Kind: tabTerminal, Label: name, Dot: dotGreen, Split: &splitNode{TermID: paneID}}
	*tabs = append(*tabs, tab)
	dir := p.Dir
	if dir == "" {
		dir = proj.Path
	}
	a.paneDirs[paneID] = dir
	if _, err := a.startPane(paneID, tab.ID, dir, p.Command, false); err != nil {
		a.paneFailed[paneID] = true
		return nil, err
	}
	if p.Focus {
		a.focusSession(pi, tab.ID)
	}
	a.saveSoon()
	return proto.Created{Session: tab.ID, Pane: paneID}, nil
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

// projectTabsPtr is a project's tabs to change, nil for a project not
// shown this run (whose sessions wait in a.saved).
func (a *appState) projectTabsPtr(p *project) *[]topTab {
	if p.ID == a.wsOwner {
		return &a.tabs
	}
	if w := a.ws[p.ID]; w != nil {
		return &w.tabs
	}
	return nil
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
	if _, err := a.startPane(paneID, tab.ID, workDir, p.Command, false); err != nil {
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
		a.panes.Remove(pid)
	}
	*tabs = append((*tabs)[:ti], (*tabs)[ti+1:]...)
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
	a.panes.Remove(id)
	a.saveSoon()
	return nil
}

// cliRestart starts a pane's shell again, in its directory.
func (a *appState) cliRestart(id string) error {
	_, tabs, ti, ok := a.findPane(id)
	if !ok {
		return proto.Errorf(proto.CodeNotFound, "no pane "+id)
	}
	dir := a.status[id].Dir
	if dir == "" {
		dir = a.paneDirs[id]
	}
	a.panes.Remove(id)
	delete(a.paneFailed, id)
	_, err := a.startPane(id, (*tabs)[ti].ID, dir, nil, false)
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
	shells := map[int]*session.Pane{}
	a.panes.Each(func(pane *session.Pane) {
		if pid := pane.Info().PID; pid > 0 {
			shells[pid] = pane
		}
	})
	var found *session.Pane
	for pid, n := p.PID, 0; pid > 1 && n < 64; pid, n = pty.ProcParent(pid), n+1 {
		if pane := shells[pid]; pane != nil {
			found = pane
			break
		}
	}
	away := false
	if found == nil {
		// The hook ran away from its pane, as OpenCode's service runs it:
		// the pane running that agent without a conversation yet, the one
		// in the same directory first.
		var cands []*session.Pane
		a.panes.Each(func(pane *session.Pane) {
			i := pane.Info()
			if i.Agent == p.Agent && i.AgentSession == nil {
				cands = append(cands, pane)
			}
		})
		for _, c := range cands {
			if found == nil || p.Dir != "" && c.Info().WorkDir() == p.Dir {
				found = c
			}
		}
		away = found != nil
	}
	if found == nil {
		return nil, proto.Errorf(proto.CodeNotFound, "no pane runs that agent")
	}
	found.SetAgentSession(&agent.Session{Agent: p.Agent, ID: p.Session})
	info := found.Info()
	a.post(a.saveSoon)
	return proto.AgentSessionResult{Pane: info.ID, Session: info.Session, Away: away}, nil
}

var _ = model.Version

// handleMore runs the methods of later phases: browser and git.
func (h *localHost) handleMore(ctx context.Context, method string, raw json.RawMessage) (any, error, bool) {
	return nil, nil, false
}
