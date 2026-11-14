package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/daemon"
	"omaterm/internal/proto"
	"omaterm/internal/sshconn"
)

// hostInfo is a remote host's connection as Settings and the sidebar show
// it. The UI thread owns it: the connection goroutine posts its changes.
type hostInfo struct {
	Status  string // connecting, online, reconnecting, offline
	Err     string
	Version string
}

// remoteHostForm is the Add a host form, as typed.
type remoteHostForm struct {
	name, ssh string
}

// startHosts connects the hosts settings.json names. Called once the
// window exists, so a slow or unreachable host never holds the app up.
func (a *appState) startHosts() { a.reloadHosts() }

// reloadHosts lets go of every host and connects the configured ones
// again: a host added, changed or removed in Settings.
func (a *appState) reloadHosts() {
	for id := range a.hostStops {
		a.stopHost(id)
	}
	a.hostInfos = map[string]*hostInfo{}
	for _, h := range a.settings.Hosts {
		a.connectHost(h)
	}
}

func (a *appState) hostByID(id string) (RemoteHost, bool) {
	if h := a.hostByIDPtr(id); h != nil {
		return *h, true
	}
	return RemoteHost{}, false
}

// hostByIDPtr is the host as settings hold it, to edit in place.
func (a *appState) hostByIDPtr(id string) *RemoteHost {
	for i := range a.settings.Hosts {
		if a.settings.Hosts[i].ID == id {
			return &a.settings.Hosts[i]
		}
	}
	return nil
}

func (a *appState) hostName(id string) string {
	if h, ok := a.hostByID(id); ok {
		return h.Name
	}
	return "missing host"
}

// projectIsRemote tells whether the shown project lives on another
// computer: its files, git and processes are not this app's to read.
func (a *appState) projectIsRemote() bool {
	p := a.project()
	return p != nil && p.Host != ""
}

// remoteNote says once why a local feature has nothing to show for a
// remote project.
func (a *appState) remoteNote(c *ui.Context) {
	ui.Column(c).FillWidth().Padding(14).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconServer).Size(14, 14).TextColor(pal.TextFaint)
			ui.Text(c, "On "+a.hostName(a.project().Host)).FontSize(12.5).FontWeight(600).TextColor(pal.Text)
		})
		ui.Text(c, "Files, Git, processes and Dev URLs stay on this computer for now. This project's terminal sessions run on the host.").
			FontSize(11.5).TextColor(pal.TextMuted).Wrap()
	})
}

func (a *appState) connectHost(h RemoteHost) {
	if h.ID == "" || strings.TrimSpace(h.SSH) == "" {
		return
	}
	if _, ok := a.hostStops[h.ID]; ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.hostStops[h.ID] = cancel
	a.hostInfos[h.ID] = &hostInfo{Status: "connecting"}
	go a.runHost(ctx, h)
}

// stopHost ends a host's connection: its panes keep running on it.
func (a *appState) stopHost(id string) {
	if stop := a.hostStops[id]; stop != nil {
		stop()
		delete(a.hostStops, id)
	}
	if a.hosts != nil {
		a.hosts.removeRemote(id)
	}
	delete(a.hostInfos, id)
}

func (a *appState) reconnectHost(h RemoteHost) {
	a.stopHost(h.ID)
	a.connectHost(h)
}

func (a *appState) addHost() {
	ssh := strings.TrimSpace(a.hostForm.ssh)
	if ssh == "" {
		return
	}
	name := strings.TrimSpace(a.hostForm.name)
	if name == "" {
		name = ssh
	}
	h := RemoteHost{ID: newID(), Name: name, SSH: ssh}
	a.settings.Hosts = append(a.settings.Hosts, h)
	a.hostForm = remoteHostForm{}
	a.saveSettings()
	a.connectHost(h)
}

func (a *appState) removeHost(id string) {
	for i := len(a.projects) - 1; i >= 0; i-- {
		if a.projects[i].Host == id {
			a.removeProject(i)
		}
	}
	hosts := a.settings.Hosts[:0]
	for _, h := range a.settings.Hosts {
		if h.ID != id {
			hosts = append(hosts, h)
		}
	}
	a.settings.Hosts = hosts
	a.saveSettings()
	a.stopHost(id)
	a.settingsGo("hosts")
}

// runHost connects a host, retrying while it is not there. Once connected,
// the Remote follows the daemon itself, reconnecting after a network
// drop.
func (a *appState) runHost(ctx context.Context, h RemoteHost) {
	wait := time.Second
	build := daemon.Build(appVersion())
	version := daemon.VersionOf(build)
	for {
		if err := a.prepareHost(h, build); err != nil {
			if ctx.Err() != nil {
				return
			}
			msg := err.Error()
			a.post(func() {
				if info := a.hostInfos[h.ID]; info != nil {
					info.Status, info.Err = "offline", msg
				}
			})
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait *= 2
			if wait > 30*time.Second {
				wait = 30 * time.Second
			}
			continue
		}
		wait = time.Second
		t := daemon.Transport{
			Name: h.Name,
			Dial: func() (net.Conn, error) { return sshconn.DialVersion(h.SSH, version) },
		}
		r, err := daemon.ConnectRemote(t)
		if err == nil {
			remote := daemon.VersionOf(r.Version())
			if remote != version {
				r.Close()
				err = fmt.Errorf("omaterm %s is on %s, this app is %s: install the same version there", remote, h.Name, version)
			}
		}
		if err == nil {
			a.setupHost(ctx, h, r)
			return
		}
		if ctx.Err() != nil {
			return
		}
		msg := err.Error()
		a.post(func() {
			if info := a.hostInfos[h.ID]; info != nil {
				info.Status, info.Err = "offline", msg
			}
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait *= 2
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
	}
}

func (a *appState) setupHost(ctx context.Context, h RemoteHost, r *daemon.Remote) {
	r.TermOptions = a.termStartOptions
	r.OnCall = func(method string, params json.RawMessage) (any, error) {
		return a.handleRemoteCall(h.ID, method, params)
	}
	r.OnConnection = func(reconnecting bool) {
		a.post(func() {
			if info := a.hostInfos[h.ID]; info != nil {
				if reconnecting {
					info.Status = "reconnecting"
				} else {
					info.Status, info.Err = "online", ""
					for id, host := range a.paneWaiting {
						if host == h.ID {
							delete(a.paneWaiting, id)
						}
					}
				}
			}
			a.syncStatus()
		})
	}
	r.OnLost = func(ids []string) {
		a.post(func() {
			for _, id := range ids {
				a.restoring[id] = true
			}
			a.queuedToasts = append(a.queuedToasts, h.Name+" restarted: its sessions are restored")
			a.syncStatus()
		})
	}
	a.post(func() {
		if ctx.Err() != nil || a.hostStops[h.ID] == nil {
			r.Close()
			return
		}
		a.hosts.setRemote(h.ID, r)
		if info := a.hostInfos[h.ID]; info != nil {
			info.Status, info.Err, info.Version = "online", "", r.Version()
		}
		// Panes that waited for this host start now.
		for id, host := range a.paneWaiting {
			if host == h.ID {
				delete(a.paneWaiting, id)
			}
		}
		a.pruneUnknownPanes(r)
		a.syncStatus()
	})
}

// handleRemoteCall answers a call of a remote host's omaterm command,
// under that host's limits: the sessions there, not this computer's.
func (a *appState) handleRemoteCall(hostID, method string, params json.RawMessage) (any, error) {
	host := a.hostReady.Load()
	if !a.controlSocket.Load() || host == nil {
		return nil, proto.Errorf(proto.CodeAppClosed, "omaterm's control socket is off (Settings: controlSocket)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute+time.Second)
	defer cancel()
	return host.HandleHost(ctx, hostID, method, params)
}

// --- Settings › Hosts ---

func (a *appState) settingsHosts(c *ui.Context) {
	a.settingsGroup(c, "hosts", "Hosts", "Sessions on another computer run there, over SSH. Install omaterm on it first, and use a key ssh can use without asking.", func() {
		if len(a.settings.Hosts) == 0 {
			a.settingsRow(c, "No hosts yet", settingsRowOpts{sub: "Add one below, then open a project on it."}, nil)
		}
		for _, h := range a.settings.Hosts {
			h := h
			info := a.hostInfos[h.ID]
			sub := h.SSH
			if info != nil && info.Err != "" {
				sub = info.Err
			}
			if a.settingsRow(c, h.Name, settingsRowOpts{id: "host-" + h.ID, sub: sub, link: "host:" + h.ID}, func() {
				hostStatePill(c, info)
			}) {
				a.settingsGo("host:" + h.ID)
			}
		}
	})
	a.settingsGroup(c, "host-add", "Add a host", "devbox, or you@192.168.1.10. ~/.ssh/config aliases work too.", func() {
		a.settingsTextField(c, "hostName", &a.hostForm.name, "devbox", 180, false)
		a.settingsTextField(c, "hostSSH", &a.hostForm.ssh, "user@host", 240, true)
	})
	settingsActions(c, func() {
		if a.settingsButton(c, "Add Host", true, false, strings.TrimSpace(a.hostForm.ssh) == "") {
			a.addHost()
		}
	})
}

func (a *appState) settingsHost(c *ui.Context) {
	_, id, _ := strings.Cut(a.settingsCurrent(), ":")
	hp := a.hostByIDPtr(id)
	if hp == nil {
		// Removed while its page was open, or from the history after.
		a.settingsGo("hosts")
		return
	}
	info := a.hostInfos[id]
	a.settingsGroup(c, "host-conn", "Connection", "", func() {
		a.settingsRow(c, "Status", settingsRowOpts{id: "host-status"}, func() { hostStatePill(c, info) })
		ver := "—"
		if info != nil && info.Version != "" {
			ver = daemon.VersionOf(info.Version)
		}
		a.settingsRow(c, "omaterm there", settingsRowOpts{sub: ver, mono: true}, nil)
		if info != nil && info.Err != "" {
			a.settingsRow(c, "Last error", settingsRowOpts{sub: info.Err}, nil)
		}
	})
	a.settingsGroup(c, "host-edit", "Host", "A new SSH target reconnects as you leave the field.", func() {
		a.settingsRow(c, "Name", settingsRowOpts{}, func() {
			if a.settingsTextField(c, "hostName-"+id, &hp.Name, "devbox", 180, false) {
				a.saveSettings()
			}
		})
		a.settingsRow(c, "SSH", settingsRowOpts{}, func() {
			if a.settingsTextField(c, "hostSSH-"+id, &hp.SSH, "user@host", 240, true) {
				a.saveSettings()
				if strings.TrimSpace(hp.SSH) == "" {
					a.stopHost(id)
				} else {
					a.reconnectHost(*hp)
				}
			}
		})
	})
	a.settingsGroup(c, "host-agents", "Agents", "Run omaterm skill setup on the host to teach its agents omaterm's command and conversation hook.", func() {
		a.settingsRow(c, "Agent hooks", settingsRowOpts{sub: "Set up on the host, not from here"}, nil)
	})
	installing := info != nil && info.Status == "installing"
	settingsActions(c, func() {
		if a.settingsButton(c, "Reconnect", false, false, installing) {
			a.reconnectHost(*hp)
		}
		if a.settingsButton(c, "Remove Host…", false, true, installing) {
			a.settingsUI.sheet = &settingsSheet{
				title:   "Remove " + hp.Name + "?",
				text:    "Its projects and sessions leave omaterm. Programs running in them on the host keep running.",
				confirm: "Remove",
				danger:  true,
				run:     func(c *ui.Context) { a.removeHost(id) },
			}
		}
	})
}

// remoteBanner tells of the active project's host while it is not
// connected, with the way to retry it or look at it.
func (a *appState) remoteBanner(c *ui.Context) {
	p := a.project()
	if p == nil || p.Host == "" {
		return
	}
	h, ok := a.hostByID(p.Host)
	if !ok {
		a.banner(c, "remote-missing-"+p.Host, iconServer, pal.Bad, true,
			"This project's host is no longer configured.", func() {
				if a.bannerButton(c, "Hosts Settings", true) {
					a.openSettings("hosts")
				}
			})
		return
	}
	info := a.hostInfos[h.ID]
	if info != nil && info.Status == "online" {
		return
	}
	if info != nil && info.Status == "installing" {
		a.banner(c, "remote-"+h.ID, iconServer, pal.Accent, false, "Installing omaterm on "+h.Name+"…", nil)
		return
	}
	msg := "Connecting to " + h.Name + "…"
	danger := false
	if info != nil && info.Status == "offline" {
		msg = h.Name + " is offline: " + info.Err
		danger = true
	} else if info != nil && info.Status == "reconnecting" {
		msg = "Reconnecting to " + h.Name + "…"
	}
	a.banner(c, "remote-"+h.ID, iconServer, pal.Warn, danger, msg, func() {
		if a.bannerButton(c, "Host Settings", false) {
			a.openSettings("host:" + h.ID)
		}
		if a.bannerButton(c, "Retry", true) {
			a.reconnectHost(h)
		}
	})
}

func hostStatePill(c *ui.Context, info *hostInfo) {
	if info == nil {
		settingsPill(c, "Connecting", pillPlain)
		return
	}
	switch info.Status {
	case "online":
		settingsPill(c, "Connected", pillOk)
	case "offline":
		settingsPill(c, "Offline", pillWarn)
	case "reconnecting":
		settingsPill(c, "Reconnecting", pillWarn)
	case "installing":
		settingsPill(c, "Installing", pillPlain)
	default:
		settingsPill(c, "Connecting", pillPlain)
	}
}
