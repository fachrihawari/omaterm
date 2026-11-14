package daemon

// Web access reaches the remote hosts the app names, too: its pages list
// their sessions, attach to their panes, and type in them, as the window
// does. A remote host is a connection of the daemon's own, but a passive
// one: it never subscribes, so the daemon there still knows no app follows
// it, and the app's own calls are not answered twice.

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"time"

	"omaterm/internal/notice"
	"omaterm/internal/proto"
	"omaterm/internal/session"
	"omaterm/internal/sshconn"
	"omaterm/internal/web"
)

// WebHost is a computer web access serves, as the app names it.
type WebHost struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	SSH  string `json:"ssh"`
}

// setWebHosts replaces the hosts web access reaches.
func (d *Daemon) setWebHosts(list []WebHost) {
	d.mu.Lock()
	old := d.webHosts
	d.webHosts = map[string]*Remote{}
	d.webHostGen++
	gen := d.webHostGen
	d.mu.Unlock()
	for _, r := range old {
		r.Close()
	}
	version := VersionOf(d.opts.Version)
	for _, h := range list {
		if h.ID == "" || h.SSH == "" {
			continue
		}
		go d.runWebHost(h, version, gen)
	}
	d.changed()
}

// runWebHost keeps a passive connection to a host, which web access uses
// for its panes.
func (d *Daemon) runWebHost(h WebHost, version string, gen int) {
	wait := time.Second
	for {
		d.mu.Lock()
		alive := d.webHostGen == gen
		d.mu.Unlock()
		if !alive {
			return
		}
		env := d.webSSHEnv()
		t := Transport{Name: h.Name, Dial: func() (net.Conn, error) {
			return sshconn.DialVersionEnv(h.SSH, version, env)
		}}
		r, err := ConnectRemotePassive(t)
		if err != nil {
			log.Printf("web host %s: %v", h.Name, err)
			select {
			case <-time.After(wait):
			}
			if wait < 30*time.Second {
				wait *= 2
				if wait > 30*time.Second {
					wait = 30 * time.Second
				}
			}
			continue
		}
		r.SetHooks(session.Hooks{
			OnStatus: func(info session.Info, prev session.Status) {
				d.changed()
				d.pushRemote(h.Name, info, prev)
			},
			OnExit: func(string, int) { d.changed() },
			OnNotice: func(id, title, body string) {
				d.pushRemoteProgram(h.Name, id, title, body)
			},
		})
		d.mu.Lock()
		if d.webHostGen != gen {
			d.mu.Unlock()
			r.Close()
			return
		}
		d.webHosts[h.ID] = r
		d.mu.Unlock()
		d.changed()
		wait = time.Second
		for !r.Closed() {
			time.Sleep(time.Second)
		}
		d.mu.Lock()
		if d.webHosts[h.ID] == r {
			delete(d.webHosts, h.ID)
		}
		alive = d.webHostGen == gen
		d.mu.Unlock()
		r.Close()
		d.changed()
		if !alive {
			return
		}
	}
}

// webRemotes are web access's host connections.
func (d *Daemon) webRemotes() []*Remote {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]*Remote, 0, len(d.webHosts))
	for _, r := range d.webHosts {
		out = append(out, r)
	}
	return out
}

// remoteForPane is the host a pane runs on, nil for a local one.
func (d *Daemon) remoteForPane(id string) *Remote {
	for _, r := range d.webRemotes() {
		if r.Get(id) != nil {
			return r
		}
	}
	return nil
}

// forwardWebPane passes a pane's call to the host that runs it.
func (d *Daemon) forwardWebPane(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	for _, r := range d.webRemotes() {
		out, err := r.Forward(ctx, method, raw)
		if err == nil {
			return out, nil
		}
		if proto.CodeOf(err) != proto.CodeNotFound {
			return nil, err
		}
	}
	return nil, proto.Errorf(proto.CodeNotFound, "no such pane")
}

// mailPaneID is the pane a mailbox call belongs to, "" for one that names
// a message alone.
func mailPaneID(method string, raw json.RawMessage) string {
	switch method {
	case "mail.ask":
		var p struct {
			To string `json:"to"`
		}
		if json.Unmarshal(raw, &p) == nil {
			return p.To
		}
	case "mail.list":
		var p struct {
			Pane string `json:"pane"`
		}
		if json.Unmarshal(raw, &p) == nil {
			return p.Pane
		}
	}
	return ""
}

// paneCallID is the pane a call names, for routing it to a remote host.
func paneCallID(method string, raw json.RawMessage) string {
	switch method {
	case "read", "send", "key", "wait", "pane.type", "pane.key", "pane.close", "seen":
		var p struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &p) == nil {
			return p.ID
		}
	}
	return ""
}

// webSSHEnv is what the app's environment adds to ssh for these hosts:
// the agent a key with a passphrase answers with.
func (d *Daemon) webSSHEnv() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if v := d.env["SSH_AUTH_SOCK"]; v != "" {
		return []string{"SSH_AUTH_SOCK=" + v}
	}
	return nil
}

// pushRemote tells the phones of a remote pane's change.
func (d *Daemon) pushRemote(host string, info session.Info, prev session.Status) {
	d.notifyStatus(info, prev)
	if !d.pushing() {
		return
	}
	prevInfo := info
	prevInfo.Status = prev
	kind := notice.For(prevInfo, info, false, d.notifyPrefs().Prefs)
	state := map[notice.Kind]string{notice.Waiting: notice.WaitingWords(info.Kind), notice.Failed: "Failed", notice.Done: "Done"}[kind]
	if state == "" {
		return
	}
	d.push(web.Notice{Session: info.Session, Pane: info.ID, Title: host, State: state, Words: info.Message, Urgent: kind != notice.Done})
}

// pushRemoteProgram tells the phones of a program's own notification in
// a remote pane.
func (d *Daemon) pushRemoteProgram(host, pane, title, body string) {
	if !d.pushing() || !d.notifyPrefs().Programs {
		return
	}
	words := body
	if title != "" && body != "" {
		words = title + ": " + body
	} else if title != "" {
		words = title
	}
	d.push(web.Notice{Session: "", Pane: pane, Title: host, State: "Sent a notification", Words: words, Program: true})
}
