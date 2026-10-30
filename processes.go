package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/daemon"
	"omaterm/internal/paths"
	"omaterm/internal/procs"
	"omaterm/internal/proto"
	"omaterm/internal/session"
	"omaterm/service"
)

// processCall serves the process.* methods on the app's control socket,
// on the UI thread: the omaterm command reaches the app while its window
// is open, whichever host runs the processes. A project is named by its
// id or its name.
func (a *appState) processCall(method string, raw json.RawMessage) (any, error) {
	if a.procHost == nil {
		return nil, proto.Errorf(proto.CodeUsage, "no process host")
	}
	var p proto.ProcessParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, proto.Errorf(proto.CodeUsage, err.Error())
		}
	}
	pi := -1
	for i, project := range a.projects {
		if project.ID == p.Project || project.Name == p.Project {
			pi = i
			break
		}
	}
	if method == "process.sync" {
		a.procHost.Sync()
		a.refreshProcList()
		if pi < 0 {
			return true, nil
		}
		return a.processList(pi), nil
	}
	if pi < 0 {
		return nil, proto.Errorf(proto.CodeNotFound, "no project "+p.Project)
	}
	projectID := a.projects[pi].ID
	if method == "process.list" {
		// The app's own host rereads a changed omaterm.yml for the list.
		if local, ok := a.procHost.(*localProcs); ok {
			local.SyncIfChanged()
		}
		a.refreshProcList()
		return a.processList(pi), nil
	}
	var err error
	switch method {
	case "process.start":
		err = a.procHost.Start(projectID, p.Name)
	case "process.stop":
		err = a.procHost.Stop(projectID, p.Name)
	case "process.restart":
		err = a.procHost.Restart(projectID, p.Name)
	case "process.trust":
		err = a.procHost.Trust(projectID, p.Name)
	}
	a.refreshProcList()
	if err != nil {
		var pe *proto.Error
		if errors.As(err, &pe) {
			return nil, err
		}
		code := proto.CodeUsage
		if errors.Is(err, procs.ErrNotFound) {
			code = proto.CodeNotFound
		}
		return nil, proto.Errorf(code, err.Error())
	}
	for _, proc := range a.procList[projectID] {
		if proc.Name == p.Name {
			return proto.Process(proc), nil
		}
	}
	return nil, proto.Errorf(proto.CodeNotFound, "process "+p.Name)
}

// processList is a project's processes as the protocol tells them.
func (a *appState) processList(pi int) []proto.Process {
	list := a.procList[a.projects[pi].ID]
	out := make([]proto.Process, len(list))
	for i, p := range list {
		out[i] = proto.Process(p)
	}
	return out
}

// procHost runs a project's processes: the session daemon's supervisor
// over the socket, or the app's own when there is no daemon.
type procHost interface {
	// List is the project's processes; an error means the host couldn't
	// say, not that there are none.
	List(projectID string) ([]procs.Proc, error)
	Start(projectID, name string) error
	Stop(projectID, name string) error
	Restart(projectID, name string) error
	Trust(projectID, name string) error
	Sync()
	Close()
}

type remoteProcs struct{ remote *daemon.Remote }

func (p *remoteProcs) List(projectID string) ([]procs.Proc, error) {
	return p.remote.ProcessList(projectID)
}
func (p *remoteProcs) Start(projectID, name string) error {
	return p.remote.ProcessStart(projectID, name)
}
func (p *remoteProcs) Stop(projectID, name string) error {
	return p.remote.ProcessStop(projectID, name)
}
func (p *remoteProcs) Restart(projectID, name string) error {
	return p.remote.ProcessRestart(projectID, name)
}
func (p *remoteProcs) Trust(projectID, name string) error {
	return p.remote.ProcessTrust(projectID, name)
}
func (p *remoteProcs) Sync()  { _ = p.remote.ProcessSync("") }
func (p *remoteProcs) Close() {}

type localProcs struct {
	a      *appState
	s      *procs.Supervisor
	r      *localProcRunner
	mtimes map[string]time.Time
}

// localProcRunner runs the app's own processes in its pane registry. The
// supervisor calls it from goroutines of its own, so it reads nothing of
// the app's there: the terminal options and the environment a pane gets
// are a snapshot the UI thread refreshes, and what a pane's end changes
// in the app is posted to that thread.
type localProcRunner struct {
	a        *appState
	mu       sync.Mutex
	projects map[string]project
	base     session.StartOptions
	env      []string
}

func (r *localProcRunner) setProject(projectID, name string, p project) {
	r.mu.Lock()
	r.projects[procs.SessionID(projectID, name)] = p
	r.mu.Unlock()
}

// snapshot takes, on the UI thread, what a process pane is started with.
func (r *localProcRunner) snapshot() {
	base := r.a.termStartOptions("")
	env := []string{"PATH=" + paths.BinDir() + string(os.PathListSeparator) + os.Getenv("PATH")}
	if r.a.ctl != nil {
		env = append(env, "OMATERM_SOCKET="+r.a.ctl.Path())
	}
	r.mu.Lock()
	r.base, r.env = base, env
	r.mu.Unlock()
}

func (r *localProcRunner) Start(o session.StartOptions) (session.Handle, error) {
	r.mu.Lock()
	p := r.projects[o.Session]
	opt, env := r.base, append([]string(nil), r.env...)
	r.mu.Unlock()
	a, id := r.a, o.ID
	opt.OpenLink = func(link string) bool { return a.openTermLink(id, link) }
	opt.ID, opt.Session, opt.Project, opt.ProjectName, opt.ProjectDir = o.ID, o.Session, p.ID, p.Name, p.Path
	opt.Role, opt.Dir, opt.Command, opt.Exec = "process", o.Dir, o.Command, true
	opt.Env, opt.History = append(env, o.Env...), o.History
	opt.Cols, opt.Rows = o.Cols, o.Rows
	return r.a.offPanes().Start(opt)
}

func (r *localProcRunner) Get(id string) session.Handle { return r.a.offPanes().Get(id) }

// Remove ends the pane now, in the registry, which takes that from any
// goroutine; what the app keeps about the pane goes on the UI thread.
func (r *localProcRunner) Remove(id string) {
	r.a.offPanes().Remove(id)
	r.a.post(func() { r.a.closePaneRuntime(id) })
}

func (p *localProcs) List(projectID string) ([]procs.Proc, error) { return p.s.List(projectID), nil }
func (p *localProcs) Start(projectID, name string) error          { return p.s.Start(projectID, name) }
func (p *localProcs) Stop(projectID, name string) error           { return p.s.Stop(projectID, name) }
func (p *localProcs) Restart(projectID, name string) error        { return p.s.Restart(projectID, name) }
func (p *localProcs) Trust(projectID, name string) error          { return p.s.Trust(projectID, name) }
func (p *localProcs) Sync()                                       { p.load() }
func (p *localProcs) Close()                                      { p.s.Close() }

func (p *localProcs) SyncIfChanged() {
	next := map[string]time.Time{}
	for _, project := range p.a.projects {
		path := filepath.Join(project.Path, procs.FileName)
		if info, err := os.Stat(path); err == nil {
			next[path] = info.ModTime()
		}
	}
	if len(next) != len(p.mtimes) {
		p.mtimes = next
		p.load()
		return
	}
	for path, modified := range next {
		if p.mtimes[path] != modified {
			p.mtimes = next
			p.load()
			return
		}
	}
}

func (p *localProcs) load() {
	mtimes := map[string]time.Time{}
	for _, project := range p.a.projects {
		path := filepath.Join(project.Path, procs.FileName)
		if info, err := os.Stat(path); err == nil {
			mtimes[path] = info.ModTime()
		}
		defs, err := procs.LoadFile(project.Path)
		if err != nil {
			log.Printf("process definitions in %s: %v", project.Path, err)
			defs = nil
		}
		fileNames := make(map[string]bool, len(defs))
		for _, def := range defs {
			fileNames[def.Name] = true
		}
		for _, local := range project.Processes {
			if fileNames[local.Name] {
				continue
			}
			local.Local = true
			defs = append(defs, local)
		}
		for _, def := range defs {
			p.r.setProject(project.ID, def.Name, project)
		}
		p.s.Load(project.ID, project.Path, defs)
	}
	p.mtimes = mtimes
	p.a.refreshProcList()
}

func (a *appState) useLocalProcHost() {
	if a.procHost != nil {
		a.procHost.Close()
	}
	r := &localProcRunner{a: a, projects: map[string]project{}}
	s := procs.New(r, procs.OpenTrust(procs.TrustPath()))
	l := &localProcs{a: a, s: s, r: r, mtimes: map[string]time.Time{}}
	s.OnChange = func() { a.post(a.refreshProcList) }
	// No daemon, so the app is what tells the user a process failed.
	s.OnEvent = func(event procs.Event) { a.post(func() { a.processEvent(event) }) }
	// Files are watched as the daemon watches them, with the same globs.
	s.Watch, s.Match, s.Ignore = daemon.WatchFolder, service.MatchGlob, service.IgnoreFunc
	a.procHost = l
	a.procList = map[string][]procs.Proc{}
	r.snapshot()
	l.load()
}

func (a *appState) setProcHost(host procHost) {
	if a.procHost != nil {
		a.procHost.Close()
	}
	a.procHost = host
	a.procList = map[string][]procs.Proc{}
	if a.procHost != nil {
		a.procHost.Sync()
		a.refreshProcList()
	}
}

func (a *appState) refreshProcList() {
	if a.procList == nil {
		a.procList = map[string][]procs.Proc{}
	}
	for _, project := range a.projects {
		if a.procHost == nil {
			a.procList[project.ID] = nil
			continue
		}
		list, err := a.procHost.List(project.ID)
		if err != nil {
			// The host couldn't say: what it said last stands, rather than
			// an empty list that would close the tabs and hide the group.
			continue
		}
		locals := make(map[string]bool, len(project.Processes))
		for _, def := range project.Processes {
			locals[def.Name] = true
		}
		files := map[string]bool{}
		checkFiles := false
		for _, proc := range list {
			checkFiles = checkFiles || locals[proc.Name]
		}
		if checkFiles {
			defs, err := procs.LoadFile(project.Path)
			if err == nil {
				for _, def := range defs {
					files[def.Name] = true
				}
			}
		}
		for i := range list {
			list[i].Local = locals[list[i].Name] && !files[list[i].Name]
		}
		a.procList[project.ID] = list
	}
	for pi := range a.projects {
		tabs := a.projectTabsPtr(&a.projects[pi])
		if tabs == nil {
			continue
		}
		byName := map[string]procs.Proc{}
		for _, proc := range a.procList[a.projects[pi].ID] {
			byName[proc.Name] = proc
		}
		for i := range *tabs {
			t := &(*tabs)[i]
			if t.Kind != tabProcess {
				continue
			}
			proc, ok := byName[t.Process]
			if !ok || proc.Pane == "" || proc.Status == procs.Stopped || a.panes.Get(proc.Pane) == nil {
				t.Split = nil
				continue
			}
			if t.Split == nil || !t.Split.contains(proc.Pane) {
				t.Split = &splitNode{TermID: proc.Pane}
				if pi == a.active && a.activeTab >= 0 && a.activeTab < len(a.tabs) && &a.tabs[a.activeTab] == t {
					a.activePaneTermID = proc.Pane
				}
			}
		}
	}
}

// startProcListRefresh keeps the process list fresh once a second while
// the window is open: the app's own host is asked to reread a changed
// omaterm.yml then, and the daemon's for the list.
func (a *appState) startProcListRefresh() {
	a.stopProcListRefresh()
	a.refreshProcList()
	if a.win == nil {
		return
	}
	gen := a.procGen
	a.procTimer = time.AfterFunc(time.Second, func() { a.win.Update(func() { a.procTick(gen) }) })
}

// procTick is one refresh, on the UI thread, and the next one scheduled.
func (a *appState) procTick(gen int) {
	if a.procGen != gen {
		return
	}
	if local, ok := a.procHost.(*localProcs); ok {
		local.r.snapshot()
		local.SyncIfChanged()
	}
	a.refreshProcList()
	a.procTimer = time.AfterFunc(time.Second, func() { a.win.Update(func() { a.procTick(gen) }) })
}

func (a *appState) stopProcListRefresh() {
	a.procGen++
	if a.procTimer != nil {
		a.procTimer.Stop()
		a.procTimer = nil
	}
}

func (a *appState) startProcess(pi int, name string) {
	if a.procHost == nil || pi < 0 || pi >= len(a.projects) {
		return
	}
	projectID := a.projects[pi].ID
	var proc *procs.Proc
	for _, candidate := range a.procList[projectID] {
		if candidate.Name == name {
			copy := candidate
			proc = &copy
			break
		}
	}
	if proc != nil && !proc.Trusted {
		a.askTrust(projectID, *proc)
		return
	}
	if err := a.procHost.Start(projectID, name); err != nil {
		a.toast(nil, err.Error())
		return
	}
	a.refreshProcList()
}

func (a *appState) stopProcess(pi int, name string) {
	if a.procHost == nil || pi < 0 || pi >= len(a.projects) {
		return
	}
	if err := a.procHost.Stop(a.projects[pi].ID, name); err != nil {
		a.toast(nil, err.Error())
		return
	}
	a.refreshProcList()
}

func (a *appState) restartProcess(pi int, name string) {
	if a.procHost == nil || pi < 0 || pi >= len(a.projects) {
		return
	}
	if err := a.procHost.Restart(a.projects[pi].ID, name); err != nil {
		a.toast(nil, err.Error())
		return
	}
	a.refreshProcList()
}

func (a *appState) startAllProcesses(pi int) {
	if a.procHost == nil || pi < 0 || pi >= len(a.projects) {
		return
	}
	projectID := a.projects[pi].ID
	started, untrusted := 0, 0
	for _, proc := range a.procList[projectID] {
		if !proc.Trusted {
			untrusted++
			continue
		}
		if proc.Status == procs.Starting || proc.Status == procs.Running {
			continue
		}
		if err := a.procHost.Start(projectID, proc.Name); err != nil {
			a.toast(nil, err.Error())
			continue
		}
		started++
	}
	a.refreshProcList()
	if untrusted > 0 {
		a.toast(nil, fmt.Sprintf("%d started, %d not trusted: start it to review it", started, untrusted))
	} else {
		a.toast(nil, fmt.Sprintf("%d started", started))
	}
}

func (a *appState) stopAllProcesses(pi int) {
	if a.procHost == nil || pi < 0 || pi >= len(a.projects) {
		return
	}
	projectID := a.projects[pi].ID
	stopped := 0
	for _, proc := range a.procList[projectID] {
		if proc.Status != procs.Starting && proc.Status != procs.Running {
			continue
		}
		if err := a.procHost.Stop(projectID, proc.Name); err != nil {
			a.toast(nil, err.Error())
			continue
		}
		stopped++
	}
	a.refreshProcList()
	a.toast(nil, fmt.Sprintf("%d stopped", stopped))
}

func (a *appState) moveProcess(pi int, proc procs.Proc) {
	if pi < 0 || pi >= len(a.projects) {
		return
	}
	shared := proc.Local
	if err := a.saveProcessDef(pi, proc.Def, shared, proc.Name); err != nil {
		a.toast(nil, err.Error())
		return
	}
	if err := a.trustAndSyncProcess(a.projects[pi].ID, proc.Name); err != nil {
		a.toast(nil, err.Error())
		return
	}
	if shared {
		a.toast(nil, "Moved "+proc.Name+" to omaterm.yml")
	} else {
		a.toast(nil, proc.Name+" is now local")
	}
}

func (a *appState) askRemoveProcess(pi int, proc procs.Proc) {
	store := "omaterm.yml"
	if proc.Local {
		store = "this project's local processes"
	}
	message := fmt.Sprintf("Remove %s? Its definition goes from %s; a running one is stopped.", proc.Name, store)
	name := proc.Name
	a.askDanger("Remove Process", message, "Remove", func() { a.removeProcess(pi, name) })
}

func (a *appState) removeProcess(pi int, name string) {
	if pi < 0 || pi >= len(a.projects) {
		return
	}
	project := &a.projects[pi]
	var proc *procs.Proc
	for _, candidate := range a.procList[project.ID] {
		if candidate.Name == name {
			copy := candidate
			proc = &copy
			break
		}
	}
	if proc == nil {
		return
	}
	if (proc.Status == procs.Starting || proc.Status == procs.Running) && a.procHost != nil {
		if err := a.procHost.Stop(project.ID, name); err != nil {
			a.toast(nil, err.Error())
			return
		}
	}
	if proc.Local {
		locals := project.Processes[:0]
		for _, def := range project.Processes {
			if def.Name != name {
				locals = append(locals, def)
			}
		}
		project.Processes = locals
		a.saveState()
	} else {
		defs, err := procs.LoadFile(project.Path)
		if err != nil {
			a.toast(nil, err.Error())
			return
		}
		kept := defs[:0]
		for _, def := range defs {
			if def.Name != name {
				kept = append(kept, def)
			}
		}
		if err := procs.SaveFile(project.Path, kept); err != nil {
			a.toast(nil, err.Error())
			return
		}
	}
	if a.procHost != nil {
		a.procHost.Sync()
	}
	a.closeTabID(proc.Session)
	a.refreshProcList()
	a.toast(nil, "Removed "+name)
}

func (a *appState) showProcess(pi int, name string) {
	if pi < 0 || pi >= len(a.projects) {
		return
	}
	if pi != a.active {
		a.switchProject(pi)
	}
	projectID := a.projects[pi].ID
	var proc *procs.Proc
	for _, candidate := range a.procList[projectID] {
		if candidate.Name == name {
			copy := candidate
			proc = &copy
			break
		}
	}
	if proc == nil {
		return
	}
	id := procs.SessionID(projectID, name)
	for i := range a.tabs {
		if a.tabs[i].Kind == tabProcess && a.tabs[i].ID == id {
			if proc.Pane != "" && proc.Status != procs.Stopped && a.panes.Get(proc.Pane) != nil {
				a.tabs[i].Split = &splitNode{TermID: proc.Pane}
			} else {
				a.tabs[i].Split = nil
			}
			a.activeTab, a.activeView = i, tabProcess
			if a.tabs[i].Split != nil {
				a.activePaneTermID = a.tabs[i].Split.TermID
			}
			return
		}
	}
	var split *splitNode
	if proc.Pane != "" && proc.Status != procs.Stopped && a.panes.Get(proc.Pane) != nil {
		split = &splitNode{TermID: proc.Pane}
	}
	a.tabs = append(a.tabs, topTab{ID: id, Kind: tabProcess, Label: proc.Name, Process: proc.Name, Split: split})
	a.activeTab, a.activeView = len(a.tabs)-1, tabProcess
	a.activePaneTermID = ""
	if split != nil {
		a.activePaneTermID = split.TermID
	}
}

func processStatusText(status procs.Status) string {
	switch status {
	case procs.Starting:
		return "Starting"
	case procs.Running:
		return "Running"
	case procs.Exited:
		return "Exited"
	case procs.Failed:
		return "Failed"
	case procs.Paused:
		return "Restart paused"
	case procs.Untrusted:
		return "Not trusted"
	default:
		return "Stopped"
	}
}

// procOfTab is the process a process tab shows, as last listed.
func (a *appState) procOfTab(tab topTab) (procs.Proc, bool) {
	if tab.Kind != tabProcess {
		return procs.Proc{}, false
	}
	if pi, ok := a.projectIndexForTab(tab.ID); ok {
		for _, p := range a.procList[a.projects[pi].ID] {
			if p.Name == tab.Process {
				return p, true
			}
		}
	}
	return procs.Proc{}, false
}

func (a *appState) processPlaceholder(c *ui.Context, tab topTab) ui.Element {
	proc, _ := a.procOfTab(tab)
	return ui.Column(c).Fill().Center().Gap(8).Children(func() {
		ui.Text(c, tab.Process).FontSize(18).TextColor(pal.Text).Bold()
		ui.Text(c, processStatusText(proc.Status)).FontSize(12).TextColor(pal.TextMuted)
		if proc.Status == procs.Stopped || proc.Status == procs.Untrusted || proc.Status == procs.Failed || proc.Status == procs.Exited || proc.Status == procs.Paused {
			b := ui.ButtonBase(c).Radius(6).Border(1, pal.BorderStrong).Padding(7, 14).Background(pal.Raised)
			if b.Hovered() {
				b.Background(pal.Hover)
			}
			b.Children(func() { ui.Text(c, "Start").FontSize(12).TextColor(pal.Text) })
			if b.Clicked() {
				if pi, ok := a.projectIndexForTab(tab.ID); ok {
					a.startProcess(pi, tab.Process)
				}
			}
		}
	})
}

// processOnPort names the shown project's running process that gives
// port, "" for none: the inspector's PORTS says whose a port is.
func (a *appState) processOnPort(port int) string {
	p := a.project()
	if p == nil || port <= 0 {
		return ""
	}
	for _, proc := range a.procList[p.ID] {
		if proc.Port == port && (proc.Status == procs.Starting || proc.Status == procs.Running) {
			return proc.Name
		}
	}
	return ""
}

func (a *appState) projectIndexForTab(id string) (int, bool) {
	for i, p := range a.projects {
		if id == procs.SessionID(p.ID, "") || strings.HasPrefix(id, "proc-"+p.ID+"-") {
			return i, true
		}
	}
	return 0, false
}

func (a *appState) processIcon(c *ui.Context, proc procs.Proc, size float32) {
	var state iconState
	switch proc.Status {
	case procs.Running:
		ui.Box(c).Size(size, size).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			p.FillPath(new(ui.Path).Circle(r.X+r.W/2, r.Y+r.H/2, min(r.W, r.H)*0.28), pal.Ok)
		})
		return
	case procs.Starting:
		state = iconWorking
	case procs.Failed, procs.Paused:
		state = iconFailed
	case procs.Exited:
		state = iconExited
	case procs.Untrusted:
		state = iconWaiting
	default:
		return
	}
	ai := session.Info{Progress: -1}
	switch state {
	case iconWorking:
		ai.Status = session.Working
	case iconFailed:
		ai.Status = session.Failed
	case iconExited:
		ai.Status = session.Exited
	case iconWaiting:
		ai.Status = session.Waiting
	}
	a.statusIcon(c, ai, size)
}

var _ procHost = (*localProcs)(nil)
var _ procHost = (*remoteProcs)(nil)
