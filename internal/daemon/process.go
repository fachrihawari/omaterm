package daemon

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"omaterm/internal/procs"
	"omaterm/internal/proto"
	"omaterm/internal/session"
)

type fileStamp struct {
	modified int64
	size     int64
	exists   bool
}

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

func stamp(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{modified: info.ModTime().UnixNano(), size: info.Size(), exists: true}
}

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

func (d *Daemon) processList(project string) []proto.Process {
	procs := d.procs.List(project)
	out := make([]proto.Process, len(procs))
	for i, p := range procs {
		out[i] = proto.Process(p)
	}
	return out
}

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
