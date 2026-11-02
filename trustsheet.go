package main

import (
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/procs"
)

// trustAsk is the trust sheet of an untrusted process, while it is open: it
// shows what the process runs before the user trusts it.
type trustAsk struct {
	open      bool
	projectID string
	proc      procs.Proc
}

// askTrust opens the trust sheet of a process of the project, in place of
// trusting it silently.
func (a *appState) askTrust(projectID string, proc procs.Proc) {
	a.trustAsk = &trustAsk{open: true, projectID: projectID, proc: proc}
}

// trustAndStart trusts the process the sheet shows, then starts it. The sheet
// closes either way, and says what went wrong if either step failed.
func (a *appState) trustAndStart(st *trustAsk) {
	st.open = false
	err := a.procHost.Trust(st.projectID, st.proc.Name)
	if err == nil {
		err = a.procHost.Start(st.projectID, st.proc.Name)
	}
	a.refreshProcList()
	if err != nil {
		a.toast(nil, err.Error())
	}
}

// trustDialog is the trust sheet, while open.
func (a *appState) trustDialog(c *ui.Context) {
	st := a.trustAsk
	if st == nil {
		return
	}
	if !st.open {
		a.trustAsk = nil
		return
	}
	confirmed := false
	ui.DialogBase(c, &st.open, func(backdrop, panel ui.Element) {
		a.dialogFrame(c, backdrop, panel)
		dialogTitle(c, "Trust "+st.proc.Name+"?", false)
		ui.Text(c, "omaterm.yml defines this process; a command from a file in the repository runs only once you have read it.").
			FontSize(12.5).TextColor(pal.TextMuted).Wrap()
		for _, row := range trustDetails(st.proc.Def) {
			dialogField(c, row[0], func() {
				text := ui.Text(c, row[1]).FontSize(12.5).TextColor(pal.Text).Wrap()
				if row[0] == "Command" {
					text.Font(monoFont).FontSize(12)
				}
			})
		}
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			if a.dialogButton(c, "Cancel", false, false) {
				st.open = false
			}
			if a.dialogButton(c, "Trust and Start", true, false) {
				confirmed = true
			}
		})
	})
	if confirmed {
		a.trustAndStart(st)
	}
}

// trustDetails are a process's settings as the trust sheet labels them, in
// the order it shows them.
func trustDetails(d procs.Def) [][2]string {
	dir := d.Dir
	if dir == "" {
		dir = "the project"
	}
	port := "none"
	switch {
	case d.Port > 0:
		port = strconv.Itoa(d.Port)
	case d.URL != "":
		port = "a free one, picked as it starts (PORT)"
	}
	restart := d.Restart
	if restart == "" {
		restart = "never"
	}
	watch := "none"
	if len(d.Watch) > 0 {
		watch = strings.Join(d.Watch, ", ")
	}
	rows := [][2]string{
		{"Command", d.Command},
		{"Directory", dir},
		{"Env", envLines(d.Env)},
		{"Port", port},
		{"Restart", restart},
		{"Watch", watch},
	}
	if d.URL != "" {
		rows = append(rows, [2]string{"URL", "https://" + procs.Host(d.URL)})
	}
	return rows
}

// envLines is an environment as KEY=value lines, sorted by key, or "none".
func envLines(env map[string]string) string {
	if len(env) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + env[k]
	}
	return strings.Join(lines, "\n")
}
