package main

import (
	"path"
	"strings"

	"github.com/egoist/mygo/ui"
)

// remoteProjectAsk is the New Project on a Host dialog, while it is open.
type remoteProjectAsk struct {
	open bool
	host string // the host's ID
	dir  string // its folder, as the host knows it
	name string
	err  string
}

// openRemoteProjectDialog asks which host a new project is on, and where
// it is there.
func (a *appState) openRemoteProjectDialog() {
	if len(a.settings.Hosts) == 0 {
		a.openSettings("hosts")
		return
	}
	host := a.settings.Hosts[0].ID
	for _, h := range a.settings.Hosts {
		if info := a.hostInfos[h.ID]; info != nil && info.Status == "online" {
			host = h.ID
			break
		}
	}
	a.remoteProject = &remoteProjectAsk{open: true, host: host}
}

// remoteProjectDialog is the New Project on a Host dialog, while open.
func (a *appState) remoteProjectDialog(c *ui.Context) {
	st := a.remoteProject
	if st == nil {
		return
	}
	if !st.open {
		a.remoteProject = nil
		return
	}
	submitted := false
	ui.DialogBase(c, &st.open, func(backdrop, panel ui.Element) {
		a.dialogFrame(c, backdrop, panel)
		dialogTitle(c, "New Project on a Host", false)
		ui.Text(c, "Its sessions run on the host, over SSH. Give the folder as the host knows it, as /home/you/work/app.").
			FontSize(12.5).TextColor(pal.TextMuted).Wrap()
		dialogField(c, "Host", func() {
			opts := make([][2]string, len(a.settings.Hosts))
			for i, h := range a.settings.Hosts {
				opts[i] = [2]string{h.ID, h.Name}
				if info := a.hostInfos[h.ID]; info != nil && info.Status == "offline" {
					opts[i][1] += " (offline)"
				}
			}
			settingsPopup(c, "rp-host", &st.host, opts)
		})
		dialogField(c, "Folder", func() {
			if processTextInput(c, "rp-dir", &st.dir, "/home/you/project", "Folder on the host", true) {
				submitted = true
			}
		})
		dialogField(c, "Name", func() {
			if processTextInput(c, "rp-name", &st.name, "optional", "Project name", false) {
				submitted = true
			}
		})
		if info := a.hostInfos[st.host]; info != nil && info.Status == "offline" {
			dialogNote(c, a.hostName(st.host)+" is offline: the project is added now, and its sessions start when it connects.", false)
		}
		if st.err != "" {
			dialogNote(c, st.err, true)
		}
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			if a.dialogButton(c, "Cancel", false, false) {
				st.open = false
			}
			if a.dialogButton(c, "Add Project", true, false) {
				submitted = true
			}
		})
	})
	if !submitted {
		return
	}
	dir := strings.TrimSpace(st.dir)
	if !path.IsAbs(dir) {
		st.err = "Give the folder as an absolute path on the host, as /home/you/app."
		return
	}
	if _, ok := a.hostByID(st.host); !ok {
		st.err = "Choose a host."
		return
	}
	i := a.addProjectOn(st.host, dir, strings.TrimSpace(st.name))
	if i < 0 {
		st.err = "That project can't be added."
		return
	}
	st.open = false
	a.remoteProject = nil
	a.switchProject(i)
	// A folder opened for the first time starts with a session, as Open
	// Project… makes it.
	if len(a.tabs) == 0 {
		a.newTerminalTab()
	}
}
