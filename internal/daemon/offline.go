package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"omaterm/internal/agent"
	"omaterm/internal/gitsnap"
	"omaterm/internal/model"
	"omaterm/internal/ospath"
	"omaterm/internal/paths"
	"omaterm/internal/proto"
	"omaterm/internal/session"
	"omaterm/internal/shellcmd"
	"omaterm/internal/worktree"
)

// While no app follows it, the daemon answers the omaterm command itself:
// the sessions in state.json, which it runs. The app owns state.json
// while it runs; closed, it reads what the daemon wrote at its next
// launch.

// appAttached tells whether an app follows the daemon.
func (d *Daemon) appAttached() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.subs) > 0
}

func (d *Daemon) stateFile() string {
	if d.opts.StateFile != "" {
		return d.opts.StateFile
	}
	return paths.StateFile()
}

func (d *Daemon) loadState() model.State {
	st, err := model.Load(d.stateFile())
	if err != nil {
		st = model.State{Version: model.Version}
	}
	return st
}

func (d *Daemon) saveState(st model.State) error {
	st.Version = model.Version
	return model.Save(d.stateFile(), st)
}

// change loads state.json, lets fn change it and saves it, one change at
// a time.
func (d *Daemon) change(fn func(st *model.State) (any, error)) (any, error) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	// A file that is there but can't be read is never written over: it
	// holds every session.
	if _, err := model.Load(d.stateFile()); model.Unreadable(err) {
		return nil, proto.Errorf(proto.CodeUsage, "omaterm's sessions file can't be read ("+err.Error()+"): open omaterm to see")
	}
	st := d.loadState()
	v, err := fn(&st)
	if err != nil {
		return nil, err
	}
	return v, d.saveState(st)
}

var errAppClosed = func(what string) error {
	return proto.Errorf(proto.CodeAppClosed, "the omaterm window is closed: open it to "+what)
}

// handleOffline answers the omaterm command's methods.
func (d *Daemon) handleOffline(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if d.appAttached() {
		switch method {
		case "state", "create", "split", "close", "restart", "rename", "focus", "select", "wait",
			"pane.agentSession", "project.create", "git.changes", "git.diff", "browser", "do", "actions", "pane.wake",
			"worktree.merge":
			return d.forward(ctx, method, raw)
		}
	}
	switch method {
	case "state":
		d.stateMu.Lock()
		st := d.loadState()
		d.stateMu.Unlock()
		return d.protoState(st), nil
	case "create", "split":
		var p proto.CreateParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, proto.Errorf(proto.CodeUsage, err.Error())
		}
		split := method == "split" || p.Split != ""
		if p.Worktree != nil {
			if split {
				return nil, proto.Errorf(proto.CodeUsage, "a split works in its session's folder: --worktree is for new")
			}
			return d.createWorktree(ctx, p)
		}
		if split {
			return d.change(func(st *model.State) (any, error) { return d.split(st, p) })
		}
		return d.change(func(st *model.State) (any, error) { return d.create(st, p) })
	case "close":
		var p proto.IDParams
		json.Unmarshal(raw, &p)
		return d.change(func(st *model.State) (any, error) { return true, d.closeSession(st, p.ID) })
	case "worktree.merge":
		var p proto.MergeParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, proto.Errorf(proto.CodeUsage, err.Error())
		}
		return d.mergeWorktree(ctx, p)
	case "pane.wake":
		// Web access shows any pane: one of state.json not running yet
		// starts, as at a restore.
		var p proto.IDParams
		json.Unmarshal(raw, &p)
		if d.reg.Get(p.ID) != nil {
			return true, nil
		}
		d.stateMu.Lock()
		st := d.loadState()
		d.stateMu.Unlock()
		s, pn := findPane(&st, p.ID)
		if pn == nil {
			return nil, proto.Errorf(proto.CodeNotFound, "no pane "+p.ID)
		}
		if pn.URL != "" {
			return nil, proto.Errorf(proto.CodeNotFound, "pane "+p.ID+" is a browser, not a terminal")
		}
		_, resume := d.restorePrefs()
		if err := d.restorePane(&st, s, *pn, resume); err != nil && !errors.Is(err, session.ErrExists) {
			return nil, err // a wake racing another, or a restore, found it starting: it runs
		}
		return true, nil
	case "restart":
		var p proto.IDParams
		json.Unmarshal(raw, &p)
		return d.change(func(st *model.State) (any, error) {
			s, pn := findPane(st, p.ID)
			if pn == nil {
				return nil, proto.Errorf(proto.CodeNotFound, "no pane "+p.ID)
			}
			if pn.URL != "" {
				return nil, errAppClosed("reload a page")
			}
			dir, err := s.Worktree.PaneDir(pn.Dir)
			if err != nil {
				return nil, err
			}
			// The command it started with runs again, as the app does,
			// with the environment it was given.
			var command, env []string
			if p := d.reg.Get(pn.ID); p != nil {
				command, env = p.Info().Command, p.Env()
			}
			d.endPane(pn.ID)
			return true, d.startPane(projectOf(st, s.Project), s.ID, pn.ID, dir, command, env)
		})
	case "rename":
		var p proto.RenameParams
		json.Unmarshal(raw, &p)
		return d.change(func(st *model.State) (any, error) {
			i := sessionIndex(st, p.ID)
			if i < 0 {
				return nil, proto.Errorf(proto.CodeNotFound, "no session "+p.ID)
			}
			st.Sessions[i].Name, st.Sessions[i].AgentNamed = p.Name, p.By != ""
			return true, nil
		})
	case "focus", "select":
		return nil, errAppClosed("show a session")
	case "browser":
		return nil, errAppClosed("use a browser pane")
	case "do", "actions":
		return nil, errAppClosed("run its commands")
	case "wait":
		var p proto.WaitParams
		json.Unmarshal(raw, &p)
		pn := d.reg.Get(p.ID)
		if pn == nil {
			return nil, proto.Errorf(proto.CodeNotFound, "no pane "+p.ID)
		}
		if p.TimeoutMS > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
			defer cancel()
		}
		info, err := session.Await(ctx, session.LocalHost{Registry: d.reg}.Get(p.ID), p.Status, p.Exit)
		switch {
		case errors.Is(err, session.ErrClosed):
			return nil, proto.Errorf(proto.CodeNotFound, "pane closed")
		case err != nil:
			return nil, proto.Errorf(proto.CodeTimeout, "timed out")
		}
		return info, nil
	case "pane.agentSession":
		var p proto.AgentSessionParams
		json.Unmarshal(raw, &p)
		if !agent.SessionIDRe.MatchString(p.Session) {
			return nil, proto.Errorf(proto.CodeUsage, "bad session id")
		}
		found, away := session.FindAgentPane(session.LocalHost{Registry: d.reg}, p.PID, p.Agent, p.Session, p.Dir)
		if found == nil {
			return nil, proto.Errorf(proto.CodeNotFound, "no pane runs that agent")
		}
		as := &agent.Session{Agent: p.Agent, ID: p.Session}
		info := found.Info()
		// Told again each turn (Antigravity's hook), the same conversation
		// changes nothing.
		if cur := info.AgentSession; cur == nil || *cur != *as {
			found.SetAgentSession(as)
			d.change(func(st *model.State) (any, error) {
				if _, pn := findPane(st, info.ID); pn != nil {
					pn.Agent = as
				}
				return nil, nil
			})
		}
		return proto.AgentSessionResult{Pane: info.ID, Session: info.Session, Away: away}, nil
	case "project.create":
		var p proto.ProjectParams
		json.Unmarshal(raw, &p)
		return d.change(func(st *model.State) (any, error) {
			i, err := addProject(st, p.Dir, p.Name)
			if err != nil {
				return nil, err
			}
			pr := st.Projects[i]
			return proto.Project{ID: pr.ID, Name: pr.Name, Dir: pr.Dir}, nil
		})
	case "git.changes", "git.diff":
		var p proto.GitParams
		json.Unmarshal(raw, &p)
		pn := d.reg.Get(p.Pane)
		if pn == nil {
			return nil, proto.Errorf(proto.CodeNotFound, "no pane "+p.Pane)
		}
		branch := ""
		d.stateMu.Lock()
		st := d.loadState()
		d.stateMu.Unlock()
		if s, _ := findPane(&st, p.Pane); s != nil && s.Worktree != nil {
			branch = s.Worktree.Base
		}
		return gitsnap.Answer(ctx, method, p, pn.Info(), branch)
	}
	return nil, proto.Errorf(proto.CodeUsage, "no method "+method)
}

// protoState is state.json as the socket tells it, with the daemon's
// panes' Info.
func (d *Daemon) protoState(st model.State) proto.State {
	infos := d.reg.Infos()
	out := proto.State{Started: d.started.UnixMilli()}
	for _, p := range st.Projects {
		out.Projects = append(out.Projects, proto.Project{ID: p.ID, Name: p.Name, Dir: p.Dir})
	}
	for _, s := range st.Sessions {
		ps := proto.Session{ID: s.ID, Project: s.Project, AgentNamed: s.AgentNamed, Focus: s.Focus, Zoomed: s.Zoomed, Layout: s.Layout,
			Worktree: s.Worktree, Parent: s.Parent}
		for _, pn := range s.Panes {
			info, ok := infos[pn.ID]
			if !ok {
				info = session.Info{ID: pn.ID, Session: s.ID, Project: s.Project, Dir: pn.Dir, URL: pn.URL,
					Title: pn.Title, Status: session.Idle, Progress: -1}
			}
			ps.Panes = append(ps.Panes, info)
		}
		if ps.Focus == "" && len(ps.Panes) > 0 {
			ps.Focus = ps.Panes[0].ID
		}
		ps.Lead, _ = session.Lead(ps.Panes)
		ps.Name = sessionName(s, ps.Lead)
		out.Sessions = append(out.Sessions, ps)
	}
	for _, project := range st.Projects {
		for _, process := range d.procs.Sessions(project.ID) {
			var info session.Info
			if pane := d.reg.Get(process.Pane); pane != nil {
				info = pane.Info()
			} else {
				info = session.Info{ID: process.Pane, Session: process.Session, Project: project.ID,
					Role: "process", Status: session.Exited, Progress: -1}
			}
			proc := process
			out.Sessions = append(out.Sessions, proto.Session{ID: process.Session, Project: project.ID,
				Name: process.Name, Process: process.Name, Proc: &proc, Panes: []proto.Pane{info}, Lead: info})
		}
	}
	proto.DropGoneParents(out.Sessions)
	return out
}

// sessionName is what the app calls a session: its name, else what its
// lead pane runs.
func sessionName(s model.Session, lead session.Info) string {
	if s.Name != "" && s.Name != "Terminal" && s.Name != shellcmd.Name(shellcmd.Login()) {
		return s.Name
	}
	switch {
	case lead.Agent != "":
		return agent.DisplayName(lead.Agent)
	case lead.Process != "":
		return lead.Process
	case lead.WorkDir() != "":
		return filepath.Base(lead.WorkDir())
	case s.Name != "":
		return s.Name
	}
	return "Terminal"
}

// create makes a session, in the project p names, else the one holding
// its folder (or a new one for it), else the active one.
func (d *Daemon) create(st *model.State, p proto.CreateParams) (any, error) {
	pi, err := findProject(st, p)
	if err != nil {
		return nil, err
	}
	proj := st.Projects[pi]
	dir := p.Dir
	if dir == "" {
		dir = proj.Dir
	}
	name := p.Name
	if name == "" {
		name = shellcmd.Name(shellcmd.Login())
	}
	parent := ""
	if s, _ := findPane(st, p.By); s != nil && p.By != "" {
		parent = s.ID
	}
	sid, pid := model.NewID(), model.NewID()
	pane := model.Pane{ID: pid, Dir: dir, Created: time.Now().UnixMilli()}
	if p.URL != "" {
		pane.URL = d.browserURL(p.URL) // the app loads it once open
	} else if err := d.startPane(proj, sid, pid, dir, p.Command, p.Env); err != nil {
		return nil, err
	}
	// Last under the session that started it, as the app shows it.
	at := model.TreeInsert(len(st.Sessions), func(i int) string { return st.Sessions[i].ID },
		func(i int) string { return st.Sessions[i].Parent }, parent)
	st.Sessions = slices.Insert(st.Sessions, at, model.Session{ID: sid, Project: proj.ID, Name: name, Parent: parent,
		Created: time.Now().UnixMilli(), Focus: pid, Layout: &model.Layout{Pane: pid}, Panes: []model.Pane{pane}})
	return proto.Created{Session: sid, Pane: pid}, nil
}

// closeSession ends session id of st, and its panes.
func (d *Daemon) closeSession(st *model.State, id string) error {
	i := sessionIndex(st, id)
	if i < 0 {
		return proto.Errorf(proto.CodeNotFound, "no session "+id)
	}
	for _, pn := range st.Sessions[i].Panes {
		d.endPane(pn.ID)
	}
	st.Sessions = append(st.Sessions[:i], st.Sessions[i+1:]...)
	return nil
}

// mergeWorktree merges a worker's worktree into its lead's branch, for
// omaterm merge with the window closed: as the app does, git's work
// outside the sessions file's lock.
func (d *Daemon) mergeWorktree(ctx context.Context, p proto.MergeParams) (any, error) {
	d.stateMu.Lock()
	st := d.loadState()
	d.stateMu.Unlock()
	lead, _ := findPane(&st, p.By)
	if lead == nil {
		return nil, proto.Errorf(proto.CodeUsage, "run omaterm merge in your pane: it merges into your branch")
	}
	i := sessionIndex(&st, p.Target)
	if i < 0 {
		return nil, proto.Errorf(proto.CodeNotFound, "no session "+p.Target)
	}
	t := st.Sessions[i]
	name := sessionName(t, session.Info{})
	if err := model.LeadMerge(lead.Worktree, t.Worktree, name); err != nil {
		return nil, proto.Errorf(proto.CodeUsage, err.Error())
	}
	w := *t.Worktree
	for _, info := range d.reg.Infos() {
		if info.Status == session.Working && info.Agent != "" && within(info.WorkDir(), w.Dir) {
			return nil, proto.Errorf(proto.CodeUsage, name+" is working: wait until it stops (omaterm wait "+name+"), then merge")
		}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), worktreeTimeout)
	defer cancel()
	wt := worktree.Of(w.Dir, w.Branch, w.Base)
	commits, err := worktree.MergeWork(ctx, wt, p.Message, !p.Commit, p.Keep)
	if err != nil {
		return nil, worktree.ForLead(err, name, w.Base)
	}
	merged := proto.Merged{Branch: w.Branch, Into: w.Base, Commits: commits}
	if !p.Keep {
		if _, err := d.change(func(st *model.State) (any, error) { return nil, d.closeSession(st, t.ID) }); err != nil {
			return nil, fmt.Errorf("merged, but its session stays: %w", err)
		}
		if err := worktree.Remove(ctx, wt); err != nil {
			return nil, fmt.Errorf("merged, but its worktree stays: %w", err)
		}
		merged.Removed = true
	}
	return merged, nil
}

// worktreeTimeout bounds a worktree's git work: a checkout of a large
// repository, with its hooks, or a merge.
const worktreeTimeout = 5 * time.Minute

// createWorktree makes a session in a worktree of its own: the worktree
// is made outside the sessions file's lock, as git may take a while.
func (d *Daemon) createWorktree(ctx context.Context, p proto.CreateParams) (any, error) {
	v, err := d.change(func(st *model.State) (any, error) {
		pi, err := findProject(st, p)
		if err != nil {
			return nil, err
		}
		return st.Projects[pi], nil
	})
	if err != nil {
		return nil, err
	}
	proj := v.(model.Project)
	from := p.Dir
	if from == "" {
		from = proj.Dir
	}
	// The command going away doesn't stop git midway through a checkout.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), worktreeTimeout)
	defer cancel()
	wt, err := worktree.Add(ctx, from, p.Worktree.Branch, p.Worktree.Base, !p.Worktree.NoEnv)
	if err != nil {
		return nil, err
	}
	p.Project, p.Dir, p.Worktree = proj.ID, wt.Start(), nil
	if p.Name == "" {
		p.Name = wt.Branch
	}
	return d.change(func(st *model.State) (any, error) {
		for _, s := range st.Sessions {
			if s.Worktree != nil && s.Worktree.Dir == wt.Dir {
				return nil, fmt.Errorf("%s has a session already", wt.Branch)
			}
		}
		v, err := d.create(st, p)
		if err == nil {
			i := sessionIndex(st, v.(proto.Created).Session)
			st.Sessions[i].Worktree = &model.Worktree{Dir: wt.Dir, Branch: wt.Branch, Base: wt.Base}
		}
		return v, err
	})
}

// findProject finds the project a new session goes in: the one p names,
// else the one holding its folder (or a new one for it), else the active
// one.
func findProject(st *model.State, p proto.CreateParams) (int, error) {
	pi := -1
	switch {
	case p.Project != "":
		for i, pr := range st.Projects {
			if pr.ID == p.Project || strings.EqualFold(pr.Name, p.Project) {
				pi = i
			}
		}
		if pi < 0 {
			return -1, proto.Errorf(proto.CodeNotFound, "no project "+p.Project)
		}
	case p.Dir != "":
		best := -1
		for i, pr := range st.Projects {
			if within(p.Dir, pr.Dir) && (best < 0 || len(pr.Dir) > len(st.Projects[best].Dir)) {
				best = i
			}
		}
		pi = best
		if pi < 0 {
			var err error
			if pi, err = addProject(st, p.Dir, ""); err != nil {
				return -1, err
			}
		}
	default:
		for i, pr := range st.Projects {
			if pr.ID == st.ActiveProject {
				pi = i
			}
		}
		if pi < 0 && len(st.Projects) > 0 {
			pi = 0
		}
		if pi < 0 {
			return -1, proto.Errorf(proto.CodeUsage, "no project: give a directory")
		}
	}
	return pi, nil
}

// split makes a pane beside p.Split, a pane or a session (its focused
// pane), taking p.Ratio of the room (half by default).
func (d *Daemon) split(st *model.State, p proto.CreateParams) (any, error) {
	s, target := findPane(st, p.Split)
	if target == nil {
		if i := sessionIndex(st, p.Split); i >= 0 {
			s = &st.Sessions[i]
			if s.Focus != "" {
				_, target = findPane(st, s.Focus)
			}
			if target == nil && len(s.Panes) > 0 {
				target = &s.Panes[0]
			}
		}
	}
	if target == nil {
		return nil, proto.Errorf(proto.CodeNotFound, "no pane or session "+p.Split)
	}
	ratio := p.Ratio
	if ratio <= 0 || ratio >= 1 {
		ratio = 0.5
	}
	dir := p.Dir
	if dir == "" {
		dir = target.Dir
		if pn := d.reg.Get(target.ID); pn != nil && pn.Info().WorkDir() != "" {
			dir = pn.Info().WorkDir()
		}
	}
	if p.URL == "" {
		var err error
		if dir, err = s.Worktree.PaneDir(dir); err != nil {
			return nil, err
		}
	}
	targetID, pid := target.ID, model.NewID()
	pane := model.Pane{ID: pid, Dir: dir, Created: time.Now().UnixMilli()}
	if p.URL != "" {
		pane.URL = d.browserURL(p.URL)
	} else if err := d.startPane(projectOf(st, s.Project), s.ID, pid, dir, p.Command, p.Env); err != nil {
		return nil, err
	}
	s.Layout = splitLayout(s.Layout, targetID, pid, p.Direction, ratio)
	s.Panes = append(s.Panes, pane)
	if p.Focus {
		s.Focus = pid
	}
	return proto.Created{Session: s.ID, Pane: pid}, nil
}

// splitLayout puts pane beside target in l: right (the default), down,
// left or up, taking ratio of the room.
func splitLayout(l *model.Layout, target, pane, dir string, ratio float64) *model.Layout {
	if l == nil {
		return &model.Layout{Pane: pane}
	}
	if l.Pane == target {
		row := dir != "down" && dir != "up"
		before := dir == "left" || dir == "up"
		n, old := &model.Layout{Pane: pane}, &model.Layout{Pane: target}
		if before {
			return &model.Layout{Row: row, Ratio: ratio, A: n, B: old}
		}
		return &model.Layout{Row: row, Ratio: 1 - ratio, A: old, B: n}
	}
	if l.Pane == "" {
		l.A = splitLayout(l.A, target, pane, dir, ratio)
		l.B = splitLayout(l.B, target, pane, dir, ratio)
	}
	return l
}

// removeLayout takes pane out of l, its sibling taking the room.
func removeLayout(l *model.Layout, pane string) *model.Layout {
	switch {
	case l == nil || l.Pane == pane:
		return nil
	case l.Pane != "":
		return l
	}
	l.A, l.B = removeLayout(l.A, pane), removeLayout(l.B, pane)
	switch {
	case l.A == nil:
		return l.B
	case l.B == nil:
		return l.A
	}
	return l
}

// closePane ends a pane and takes it out of its session, the session
// going with its last pane.
func (d *Daemon) closePane(id string) (any, error) {
	return d.change(func(st *model.State) (any, error) {
		s, pn := findPane(st, id)
		d.endPane(id)
		if pn == nil {
			return true, nil
		}
		s.Layout = removeLayout(s.Layout, id)
		for i := range s.Panes {
			if s.Panes[i].ID == id {
				s.Panes = append(s.Panes[:i], s.Panes[i+1:]...)
				break
			}
		}
		if s.Focus == id {
			s.Focus = ""
		}
		if s.Layout == nil {
			i := sessionIndex(st, s.ID)
			st.Sessions = append(st.Sessions[:i], st.Sessions[i+1:]...)
		}
		return true, nil
	})
}

// startPane starts a pane's shell in dir, running command first when set,
// in a session of project proj.
func (d *Daemon) startPane(proj model.Project, sessionID, id, dir string, command, env []string) error {
	return d.startPaneWith(proj, sessionID, id, dir, command, env, nil)
}

// startPaneWith is startPane with the scrollback a restored pane shows.
func (d *Daemon) startPaneWith(proj model.Project, sessionID, id, dir string, command, env []string, history []byte) error {
	o := session.StartOptions{ID: id, Session: sessionID, Project: proj.ID, ProjectName: proj.Name, ProjectDir: proj.Dir,
		Dir: dir, Command: command, Env: append(d.paneEnv(), env...), Cols: 120, Rows: 40, Theme: d.currentTheme(), History: history}
	d.termPrefs().apply(&o)
	_, err := d.reg.Start(o)
	d.changed()
	return err
}

// paneEnv is what a pane adds to the daemon's environment: the omaterm
// command on its PATH, where to reach the app, and the app's display.
func (d *Daemon) paneEnv() []string {
	env := []string{
		"PATH=" + paths.BinDir() + string(os.PathListSeparator) + os.Getenv("PATH"),
		"OMATERM_SOCKET=" + paths.SocketPath(),
	}
	return append(env, d.envList()...)
}

// endPane ends a pane's shell and forgets its scrollback.
func (d *Daemon) endPane(id string) {
	d.closeAttachments(id)
	d.removePane(id)
	d.changed()
}

// projectOf is the project of id in st; an empty one for none.
func projectOf(st *model.State, id string) model.Project {
	for _, p := range st.Projects {
		if p.ID == id {
			return p
		}
	}
	return model.Project{}
}

func findPane(st *model.State, id string) (*model.Session, *model.Pane) {
	for i := range st.Sessions {
		s := &st.Sessions[i]
		for j := range s.Panes {
			if s.Panes[j].ID == id {
				return s, &s.Panes[j]
			}
		}
	}
	return nil, nil
}

func sessionIndex(st *model.State, id string) int {
	for i, s := range st.Sessions {
		if s.ID == id {
			return i
		}
	}
	return -1
}

// addProject adds a project for dir, or finds the one it is.
func addProject(st *model.State, dir, name string) (int, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return -1, proto.Errorf(proto.CodeUsage, err.Error())
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return -1, proto.Errorf(proto.CodeUsage, dir+" is not a directory")
	}
	for i, p := range st.Projects {
		if p.Dir == abs {
			return i, nil
		}
	}
	if name == "" {
		name = filepath.Base(abs)
	}
	st.Projects = append(st.Projects, model.Project{ID: model.NewID(), Name: name, Dir: abs})
	return len(st.Projects) - 1, nil
}

func within(dir, root string) bool {
	return ospath.Contains(runtime.GOOS, root, dir)
}

// SharedEnv are the variables the app gives the daemon's new panes
// (env.update): where its display and session bus are, which a daemon
// started in an older login session would have stale.
var SharedEnv = []string{"WAYLAND_DISPLAY", "DISPLAY", "XAUTHORITY", "SSH_AUTH_SOCK", "DBUS_SESSION_BUS_ADDRESS",
	"XDG_CURRENT_DESKTOP", "XDG_SESSION_TYPE", "XDG_SESSION_DESKTOP", "XDG_SESSION_ID", "HYPRLAND_INSTANCE_SIGNATURE",
	"SWAYSOCK", "DESKTOP_SESSION"}

func envShared(k string) bool {
	for _, s := range SharedEnv {
		if s == k {
			return true
		}
	}
	return false
}

// restore starts the panes of state.json that do not run, each from its
// scrollback, its agent resuming its conversation, as the settings say.
func (d *Daemon) restore() {
	restore, resume := d.restorePrefs()
	if !restore {
		return
	}
	d.stateMu.Lock()
	st := d.loadState()
	d.stateMu.Unlock()
	n := 0
	for _, s := range st.Sessions {
		for _, pn := range s.Panes {
			if pn.URL != "" || d.reg.Get(pn.ID) != nil {
				continue
			}
			if d.restorePane(&st, &s, pn, resume) == nil {
				n++
			}
		}
	}
	if n > 0 {
		log.Printf("restored %d panes", n)
	}
}

// restorePrefs reads the settings a restore follows: whether to restore
// sessions at all, and to resume their agents.
func (d *Daemon) restorePrefs() (restore, resume bool) {
	prefs := struct {
		RestoreSessions *bool `json:"restoreSessions"`
		ResumeAgents    *bool `json:"resumeAgents"`
	}{}
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(d.stateFile()), "settings.json")); err == nil {
		json.Unmarshal(b, &prefs)
	}
	return prefs.RestoreSessions == nil || *prefs.RestoreSessions, prefs.ResumeAgents == nil || *prefs.ResumeAgents
}

// restorePane starts a pane of state.json from its scrollback, its agent
// resuming its conversation when resume.
func (d *Daemon) restorePane(st *model.State, s *model.Session, pn model.Pane, resume bool) error {
	dir, err := s.Worktree.PaneDir(pn.Dir)
	if err != nil {
		return err
	}
	var command []string
	if resume {
		command = agent.ResumeCommand(pn.Agent)
	}
	return d.startPaneWith(projectOf(st, s.Project), s.ID, pn.ID, dir, command, nil, session.RestoredHistory(d.historyPath(pn.ID)))
}

// ForwardedCall is a call of the omaterm command the daemon passes to the
// app following it (event "call"), which answers with call.answer.
type ForwardedCall struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// forward passes a call to the app, which owns the sessions while open,
// and returns its answer: the first app's to answer.
func (d *Daemon) forward(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	ch := make(chan proto.Message, 1)
	d.mu.Lock()
	d.nextCall++
	id := d.nextCall
	d.calls[id] = ch
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		delete(d.calls, id)
		d.mu.Unlock()
	}()
	d.broadcast(proto.Message{Event: "call", Result: mustJSON(ForwardedCall{ID: id, Method: method, Params: raw})})
	select {
	case m := <-ch:
		if m.Error != "" {
			return nil, proto.Errorf(m.Code, m.Error)
		}
		return m.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(forwardTimeout(method, raw)):
		return nil, proto.Errorf(proto.CodeTimeout, "the app did not answer")
	}
}

// forwardTimeout is how long a call passed to the app may take: a wait
// as long as the command waits, git's work on a worktree as long as it
// may take, anything else half a minute.
func forwardTimeout(method string, raw json.RawMessage) time.Duration {
	var p struct{ Worktree any }
	if method == "worktree.merge" || method == "create" && json.Unmarshal(raw, &p) == nil && p.Worktree != nil {
		return worktreeTimeout
	}
	if method == "wait" {
		return 10 * time.Minute
	}
	return 30 * time.Second
}
