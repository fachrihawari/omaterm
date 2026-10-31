package daemon

import (
	"fmt"
	"log"
	"net"
	"path/filepath"
	"strconv"

	"omaterm/internal/devurl"
	"omaterm/internal/procs"
)

// defaultProxyAddr is where the proxy listens unless Options says otherwise:
// the port a browser uses for https, so that the URL has no port in it.
// Binding it takes a privilege the user gives once, in Dev URLs' setup. It
// is a variable so that the tests, which start many daemons, take a free
// port instead.
var defaultProxyAddr = "127.0.0.1:443"

// ProxyStatus is what proxy.status tells: whether the proxy runs, where, why
// not, and the routes it has.
type ProxyStatus struct {
	Running bool           `json:"running"`
	Addr    string         `json:"addr"`
	Error   string         `json:"error"`
	Routes  []devurl.Route `json:"routes"`
}

// procKey names a process across projects.
type procKey struct{ project, name string }

// devView is what List adds to the processes: their links, and the notes of
// those whose URL is taken. It is replaced as a whole by rebuildRoutes.
type devView struct {
	links map[procKey]string
	notes map[procKey]string
}

func (d *Daemon) proxyListenAddr() string {
	if d.opts.ProxyAddr != "" {
		return d.opts.ProxyAddr
	}
	return defaultProxyAddr
}

// reloadProxy (re)binds the proxy: it closes the one that runs and listens
// again, and the routes and links are made anew for the outcome.
func (d *Daemon) reloadProxy() ProxyStatus {
	d.devMu.Lock()
	if d.dev != nil {
		d.dev.Close()
		d.dev = nil
	}
	d.devErr = ""
	addr := d.proxyListenAddr()
	s, err := devurl.Listen(addr, filepath.Join(d.opts.StateDir, "devurl"), d.devRouter)
	if err != nil {
		d.devErr = err.Error()
		log.Printf("dev urls: %v", err)
	} else {
		d.dev = s
		log.Printf("dev urls: https://%s", s.Addr())
	}
	d.rebuildRoutesLocked()
	st := d.proxyStatusLocked()
	d.devMu.Unlock()
	d.changed()
	return st
}

// proxyStatus tells how the proxy is.
func (d *Daemon) proxyStatus() ProxyStatus {
	d.devMu.Lock()
	defer d.devMu.Unlock()
	return d.proxyStatusLocked()
}

func (d *Daemon) proxyStatusLocked() ProxyStatus {
	st := ProxyStatus{Error: d.devErr, Routes: d.devRouter.Routes()}
	if d.dev != nil {
		st.Running, st.Addr = true, d.dev.Addr()
	} else {
		st.Addr = d.proxyListenAddr()
	}
	return st
}

func (d *Daemon) stopProxy() {
	d.devMu.Lock()
	defer d.devMu.Unlock()
	if d.dev != nil {
		d.dev.Close()
		d.dev = nil
	}
}

// rebuildRoutes makes the routes, and the links the processes show, from
// the processes as they are: the supervisor tells it on every change.
func (d *Daemon) rebuildRoutes() {
	d.devMu.Lock()
	defer d.devMu.Unlock()
	d.rebuildRoutesLocked()
}

// rebuildRoutesLocked is rebuildRoutes with devMu held, which also orders
// two rebuilds so that the later one, from the newer processes, is the last
// to apply.
func (d *Daemon) rebuildRoutesLocked() {
	var routes []devurl.Route
	var keys []procKey // keys[i] is the process of routes[i]
	for _, p := range d.procs.All() {
		if p.URL == "" {
			continue
		}
		routes = append(routes, devurl.Route{Host: procs.Host(p.URL), Project: d.projectName(p.Project), Process: p.Name,
			Port: p.Port, Running: p.Status == procs.Running, Status: string(p.Status)})
		keys = append(keys, procKey{p.Project, p.Name})
	}
	d.devRouter.Set(routes)
	// The router keeps the first route of a host: the others are told so.
	view := &devView{links: map[procKey]string{}, notes: map[procKey]string{}}
	owner := map[string]int{}
	for i, rt := range routes {
		if first, taken := owner[rt.Host]; taken {
			view.notes[keys[i]] = fmt.Sprintf("%s is taken by %s in project %s", rt.Host, routes[first].Process, routes[first].Project)
			continue
		}
		owner[rt.Host] = i
		if d.dev != nil {
			view.links[keys[i]] = d.link(rt.Host)
		}
	}
	d.devViewMu.Lock()
	d.devView = view
	d.devViewMu.Unlock()
}

// link is the address a host is served at: no port when the proxy listens
// on the one https uses.
func (d *Daemon) link(host string) string {
	if port := d.dev.Port(); port != 443 {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return "https://" + host
}

// projectName is what the user calls a project, which a page of the proxy
// and a note say; the id when it isn't known.
func (d *Daemon) projectName(id string) string {
	d.procMu.Lock()
	defer d.procMu.Unlock()
	for _, p := range d.procProjects {
		if p.ID == id {
			return p.Name
		}
	}
	return id
}

// decorateProc gives a process its link, or the note that its URL is
// taken. It reads the view only, so the supervisor can call it with its
// lock held.
func (d *Daemon) decorateProc(project string, p *procs.Proc) {
	d.devViewMu.Lock()
	view := d.devView
	d.devViewMu.Unlock()
	if view == nil || p.URL == "" {
		return
	}
	key := procKey{project, p.Name}
	p.Link = view.links[key]
	if note := view.notes[key]; note != "" && p.Message == "" {
		p.Message = note
	}
}
