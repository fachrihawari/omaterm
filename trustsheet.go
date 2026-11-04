package main

import (
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/procs"
)

// trustAsk is the trust sheet of untrusted processes, while it is open: it
// shows what each runs before the user trusts them, one or, from Start
// All, every one not trusted yet.
type trustAsk struct {
	open      bool
	projectID string
	procs     []procs.Proc
}

// askTrust opens the trust sheet of processes of the project, in place of
// trusting them silently.
func (a *appState) askTrust(projectID string, list ...procs.Proc) {
	a.trustAsk = &trustAsk{open: true, projectID: projectID, procs: list}
}

// trustAndStart trusts the processes the sheet shows, then starts them. The
// sheet closes either way, and says what went wrong if a step failed.
func (a *appState) trustAndStart(st *trustAsk) {
	st.open = false
	var errs []string
	for _, proc := range st.procs {
		err := a.procHost.Trust(st.projectID, proc.Name)
		if err == nil {
			err = a.procHost.Start(st.projectID, proc.Name)
		}
		if err != nil {
			errs = append(errs, sentence(err.Error()))
		}
	}
	a.refreshProcList()
	if len(errs) > 0 {
		a.toast(nil, strings.Join(errs, "\n"))
	}
}

// trustDialog is the trust sheet, while open.
func (a *appState) trustDialog(c *ui.Context) {
	st := a.trustAsk
	if st == nil {
		return
	}
	if !st.open || len(st.procs) == 0 {
		a.trustAsk = nil
		return
	}
	many := len(st.procs) > 1
	confirmed := false
	ui.DialogBase(c, &st.open, func(backdrop, panel ui.Element) {
		a.dialogFrame(c, backdrop, panel)
		title, intro, button := "Trust "+st.procs[0].Name+"?",
			"omaterm.yml defines this process; a command from a file in the repository runs only once you have read it.",
			"Trust and Start"
		if many {
			title = "Trust " + itoa(len(st.procs)) + " processes?"
			intro = "omaterm.yml defines these processes; a command from a file in the repository runs only once you have read it."
			button = "Trust and Start All"
		}
		dialogTitle(c, title, false)
		ui.Text(c, intro).FontSize(12.5).TextColor(pal.TextMuted).Wrap()
		_, height := c.Size()
		ui.Scroll(c.Key("trust-list")).MaxHeight(max(80, height-dialogTop(height)-dialogMinBottom-160)).Children(func() {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				for i, proc := range st.procs {
					ui.Column(c.Key(i)).FillWidth().Gap(4).Children(func() {
						if many {
							ui.Text(c, proc.Name).FontSize(12.5).FontWeight(600).TextColor(pal.TextStrong).Margin(4, 0, 0, 0)
						}
						for _, row := range trustDetails(proc.Def) {
							dialogField(c, row[0], func() {
								// Every value starts at the column's edge, short
								// or long, as a wrapped command does.
								text := ui.Text(c, row[1]).FontSize(12.5).TextColor(pal.Text).Wrap().Grow(1).MinWidth(0)
								if row[0] == "Command" {
									text.Font(monoFont).FontSize(12)
								}
							})
						}
					})
				}
			})
		})
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			if a.dialogButton(c, "Cancel", false, false) {
				st.open = false
			}
			if a.dialogButton(c, button, true, false) {
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
	restart := restartLabel(d.Restart)
	watch := "none"
	if len(d.Watch) > 0 {
		watch = strings.Join(d.Watch, ", ")
	}
	rows := [][2]string{
		{"Command", d.Command},
		{"Directory", dir},
		{"Environment", envLines(d.Env)},
		{"Port", port},
		{"Restart", restart},
		{"Watch", watch},
	}
	if d.URL != "" {
		rows = append(rows, [2]string{"Dev URL", "https://" + procs.Host(d.URL)})
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
