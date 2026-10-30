package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/egoist/mygo"

	"omaterm/internal/procs"
	"omaterm/internal/proto"
	"omaterm/internal/session"
	"omaterm/service"
)

// fileStamp is what tells a file changed: the modification time and
// size it had, and whether it was there.
type fileStamp struct {
	modified int64
	size     int64
	exists   bool
}

// daemonRunner runs the panes of the daemon's processes in its registry,
// with what every pane of the daemon gets: the project, the theme, the
// terminal prefs and the daemon's environment.
type daemonRunner struct{ d *Daemon }

func (r daemonRunner) Start(o session.StartOptions) (session.Handle, error) {
	r.d.procMu.Lock()
	project := r.d.procProjects[o.Session]
	r.d.procMu.Unlock()
	o.Project, o.ProjectName, o.ProjectDir = project.ID, project.Name, project.Dir
	o.Env = append(r.d.paneEnv(), o.Env...)
	o.Theme = r.d.currentTheme()
	r.d.termPrefs().apply(&o)
	p, err := r.d.reg.Start(o)
	if err != nil {
		return nil, err
	}
	r.d.changed()
	return p, nil
}

func (r daemonRunner) Get(id string) session.Handle {
	p := r.d.reg.Get(id)
	if p == nil {
		return nil
	}
	return p
}

func (r daemonRunner) Remove(id string) { r.d.endPane(id) }

// loadProcesses reads every project's process definitions, the omaterm.yml
// ones first and the local ones from state.json after, and gives them to
// the supervisor; it remembers the files' stamps for processFilesChanged.
func (d *Daemon) loadProcesses() {
	st := d.loadState()
	mtimes := map[string]fileStamp{d.stateFile(): stamp(d.stateFile())}
	for _, project := range st.Projects {
		file := filepath.Join(project.Dir, procs.FileName)
		mtimes[file] = stamp(file)
	}
	d.procMu.Lock()
	d.procMtimes = mtimes
	d.procMu.Unlock()
	for _, project := range st.Projects {
		defs, err := procs.LoadFile(project.Dir)
		if err != nil {
			log.Printf("process definitions in %s: %v", project.Dir, err)
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
			d.procMu.Lock()
			d.procProjects[procs.SessionID(project.ID, def.Name)] = project
			d.procMu.Unlock()
		}
		d.procs.Load(project.ID, project.Dir, defs)
	}
}

// processFilesChanged tells whether state.json or a project's omaterm.yml
// changed since loadProcesses read them.
func (d *Daemon) processFilesChanged() bool {
	d.procMu.Lock()
	paths := make(map[string]fileStamp, len(d.procMtimes))
	for path, old := range d.procMtimes {
		paths[path] = old
	}
	d.procMu.Unlock()
	for path, old := range paths {
		if stamp(path) != old {
			return true
		}
	}
	return false
}

// stamp is the file's stamp now; a missing file has the zero stamp.
func stamp(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{modified: info.ModTime().UnixNano(), size: info.Size(), exists: true}
}

// processCall serves the process.* methods: a project named by id or name,
// and for all but list and sync the process to act on, answered with the
// process as it is after.
func (d *Daemon) processCall(method string, raw json.RawMessage) (any, error) {
	var p proto.ProcessParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, proto.Errorf(proto.CodeUsage, err.Error())
		}
	}
	if p.Project != "" {
		st := d.loadState()
		found := false
		for _, project := range st.Projects {
			if project.ID == p.Project || project.Name == p.Project {
				p.Project = project.ID
				found = true
				break
			}
		}
		if !found {
			return nil, proto.Errorf(proto.CodeNotFound, "no project "+p.Project)
		}
	}
	if method == "process.sync" {
		d.loadProcesses()
		if p.Project == "" {
			return true, nil
		}
		return d.processList(p.Project), nil
	}
	if p.Project == "" {
		return nil, proto.Errorf(proto.CodeUsage, "a project is required")
	}
	if method == "process.list" {
		return d.processList(p.Project), nil
	}
	var err error
	switch method {
	case "process.start":
		err = d.procs.Start(p.Project, p.Name)
	case "process.stop":
		err = d.procs.Stop(p.Project, p.Name)
	case "process.restart":
		err = d.procs.Restart(p.Project, p.Name)
	case "process.trust":
		err = d.procs.Trust(p.Project, p.Name)
	}
	if err != nil {
		code := proto.CodeUsage
		if errors.Is(err, procs.ErrNotFound) {
			code = proto.CodeNotFound
		}
		return nil, proto.Errorf(code, err.Error())
	}
	for _, proc := range d.procs.List(p.Project) {
		if proc.Name == p.Name {
			return proto.Process(proc), nil
		}
	}
	return nil, proto.Errorf(proto.CodeNotFound, "process "+p.Name)
}

// processList is a project's processes as the protocol tells them.
func (d *Daemon) processList(project string) []proto.Process {
	procs := d.procs.List(project)
	out := make([]proto.Process, len(procs))
	for i, p := range procs {
		out[i] = proto.Process(p)
	}
	return out
}

// processEvent tells attached clients of a process event and, with no app
// to show it, notifies the desktop of a failure or a paused restart.
func (d *Daemon) processEvent(event procs.Event) {
	d.broadcast(proto.Message{Event: "process", Result: mustJSON(event)})
	if d.appAttached() || !d.notifyPrefs().Error {
		return
	}
	switch event.Kind {
	case procs.EventFailed:
		d.notifier()("Process failed: "+event.Name, "exit "+strconv.Itoa(event.Code), procs.SessionID(event.Project, event.Name))
	case procs.EventPaused:
		d.notifier()("Restart paused: "+event.Name, "", procs.SessionID(event.Project, event.Name))
	}
}

var _ procs.Runner = daemonRunner{}

// watchFolder watches a project's folder for a process that restarts when
// its files change, and calls changed with the paths of each batch of
// changes until ctx ends. It lives here because the watcher is mygo's, and
// the folder's rules are the app's: internal/procs imports neither.
func watchFolder(ctx context.Context, root string, changed func(paths []string)) error {
	w, err := mygo.WatchFiles(ctx, root, mygo.FileWatchOptions{
		Recursive: true,
		Debounce:  300 * time.Millisecond,
		Ignore:    service.IgnoreFunc(root),
	})
	if err != nil {
		return err
	}
	defer w.Close()
	for {
		events, err := w.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		paths := make([]string, 0, len(events))
		for _, e := range events {
			if e.Op != mygo.FileRemove && e.Path != "." {
				paths = append(paths, e.Path)
			}
		}
		if len(paths) > 0 {
			changed(paths)
		}
	}
}
