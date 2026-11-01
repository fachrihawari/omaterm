package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/procs"
	"omaterm/service"
)

// processDialogState is the process definition the dialog is editing.
type processDialogState struct {
	open        bool
	project     string
	projectName string
	root        string
	oldName     string
	name        string
	command     string
	dir         string
	port        string
	url         string
	// autoURL is the URL the dialog filled in itself, which a later
	// suggestion may replace: one the user typed stays.
	autoURL     string
	autostart   bool
	restart     string
	watch       string
	env         string
	shared      bool
	suggestions []procs.Def
	err         string
}

// processDialogOpen tells whether the Add/Edit Process dialog is open.
func (a *appState) processDialogOpen() bool {
	return a.procDialog != nil && a.procDialog.open
}

// openProcessDialog opens a new or existing process definition.
func (a *appState) openProcessDialog(pi int, def *procs.Def) {
	if pi < 0 || pi >= len(a.projects) {
		return
	}
	p := a.projects[pi]
	shared := service.RepoRoot(p.Path) != ""
	if a.processShared != nil {
		shared = *a.processShared
	}
	st := &processDialogState{open: true, project: p.ID, projectName: p.Name, root: p.Path,
		shared: shared, restart: "Never"}
	if def == nil {
		a.procDialog = st
		var suggestions []procs.Def
		a.worktreeJob(func(ctx context.Context) error {
			suggestions = procs.Suggest(p.Path)
			return nil
		}, func(err error) {
			if err != nil {
				st.err = err.Error()
			} else {
				st.suggestions = suggestions
			}
		})
		return
	}
	st.oldName, st.name, st.command = def.Name, def.Name, def.Command
	st.dir, st.port, st.url = def.Dir, "", def.URL
	if def.Port > 0 {
		st.port = strconv.Itoa(def.Port)
	}
	st.autostart, st.restart, st.shared = def.Autostart, restartLabel(def.Restart), !def.Local
	st.watch, st.env = strings.Join(def.Watch, "\n"), formatProcessEnv(def.Env)
	a.procDialog = st
}

// processDialog draws the Add or Edit Process dialog.
func (a *appState) processDialog(c *ui.Context) {
	st := a.procDialog
	if st == nil {
		return
	}
	if !st.open {
		a.procDialog = nil
		return
	}
	save := false
	ui.DialogBase(c, &st.open, func(backdrop, panel ui.Element) {
		a.dialogFrame(c, backdrop, panel)
		dialogTitle(c, processDialogTitle(st), false)
		_, height := c.Size()
		ui.Scroll(c.Key("process-form")).MaxHeight(height - 180).Children(func() {
			if st.oldName == "" && len(st.suggestions) > 0 {
				ui.Text(c, "Suggestions").FontSize(11.5).TextColor(pal.TextMuted)
				ui.Scroll(c.Key("process-suggestions")).MaxHeight(116).Children(func() {
					for i, suggestion := range st.suggestions {
						row := ui.Row(c.Key("suggest-"+strconv.Itoa(i))).FillWidth().Height(28).Padding(0, 8).
							Gap(8).AlignItems(ui.Center).Radius(5).Cursor(ui.CursorPointer)
						if row.Hovered() {
							row.Background(pal.Hover)
						}
						row.Children(func() {
							ui.Text(c, suggestion.Name).FontSize(12).TextColor(pal.Text).Width(90).SingleLine().Ellipsis("…")
							ui.Text(c, suggestion.Command).FontSize(11).TextColor(pal.TextMuted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
							if suggestion.Port > 0 {
								ui.Text(c, ":"+strconv.Itoa(suggestion.Port)).FontSize(11).TextColor(pal.TextFaint)
							}
						})
						if row.Clicked() {
							a.applyProcessSuggestion(st, suggestion)
						}
					}
				})
			}
			dialogField(c, "Name", func() { processTextInput(c, "proc-name", &st.name, "name", "Process name", false) })
			dialogField(c, "Command", func() { processTextInput(c, "proc-command", &st.command, "npm run dev", "Command input", true) })
			dialogField(c, "Directory", func() { processTextInput(c, "proc-dir", &st.dir, ".", "Directory input", false) })
			if err := processDirectoryError(st); err != "" {
				dialogNote(c, err, true)
			}
			dialogField(c, "Port", func() { processTextInput(c, "proc-port", &st.port, "5173", "Port input", false) })
			if !onWindows {
				// Nothing serves a URL on Windows yet.
				dialogField(c, "URL", func() {
					processURLInput(c, "proc-url", &st.url, processURLPlaceholder(st), "URL input")
				})
				if err := processURLError(st); err != "" {
					dialogNote(c, err, true)
				} else if strings.TrimSpace(st.url) != "" && strings.TrimSpace(st.port) == "" {
					dialogNote(c, "omaterm picks a free port and tells it to the process as PORT.", false)
				}
			}
			dialogField(c, "Autostart", func() { a.settingsSwitch(c, "proc-autostart", &st.autostart, false) })
			dialogField(c, "Restart", func() {
				a.settingsSegmented(c, "proc-restart", &st.restart, []string{"Never", "On failure", "Always"})
			})
			ui.Text(c, "Watch (one glob per line)").FontSize(12.5).TextColor(pal.Text)
			processTextArea(c, "proc-watch", "Watch", &st.watch, "src/**", 48)
			ui.Text(c, "Environment (one KEY=value per line)").FontSize(12.5).TextColor(pal.Text)
			processTextArea(c, "proc-env", "Environment", &st.env, "NODE_ENV=development", 48)
		})
		dialogField(c, "Save in omaterm.yml", func() { a.settingsSwitch(c, "proc-shared", &st.shared, false) })
		if st.err != "" {
			ui.Text(c, st.err).FontSize(12).TextColor(pal.Bad).Wrap()
		}
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			if a.dialogButton(c, "Cancel", false, false) {
				st.open = false
			}
			if a.dialogButton(c, "Save", true, false) {
				save = true
			}
		})
	})
	if save && st.open {
		a.saveProcessDialog(st)
	}
}

func processDialogTitle(st *processDialogState) string {
	if st.oldName != "" {
		return "Edit Process"
	}
	return "Add Process"
}

func processTextInput(c *ui.Context, key string, value *string, placeholder, label string, mono bool) {
	f := ui.Row(c.Key(key)).Height(28).Grow(1).Padding(0, 8).Radius(6).AlignItems(ui.Center).
		Background(pal.Input).Border(1, pal.BorderStrong)
	f.Children(func() {
		in := ui.TextInputBase(c, value).Placeholder(placeholder).FontSize(12.5).Grow(1).MinWidth(0).Label(label)
		if mono {
			in.Font("monospace")
		}
		if in.Focused() {
			f.Border(1, pal.Accent)
		}
	})
}

func processTextArea(c *ui.Context, key, label string, value *string, placeholder string, height float32) {
	ui.Box(c.Key(key+"-box")).FillWidth().Radius(6).Border(1, pal.BorderStrong).Background(pal.Input).Children(func() {
		ui.TextAreaBase(c, value).Placeholder(placeholder).FontSize(12).Padding(8).MinHeight(height).MaxHeight(height * 2).FillWidth().Label(label)
	})
}

// processURLInput is a text input with ".localhost" after it, the part of
// the address omaterm adds.
func processURLInput(c *ui.Context, key string, value *string, placeholder, label string) {
	f := ui.Row(c.Key(key)).Height(28).Grow(1).Padding(0, 8).Radius(6).AlignItems(ui.Center).Gap(2).
		Background(pal.Input).Border(1, pal.BorderStrong)
	f.Children(func() {
		in := ui.TextInputBase(c, value).Placeholder(placeholder).FontSize(12.5).Grow(1).MinWidth(0).Label(label)
		if in.Focused() {
			f.Border(1, pal.Accent)
		}
		ui.Text(c, ".localhost").FontSize(12.5).TextColor(pal.TextFaint).Shrink(0)
	})
}

// processURLPlaceholder is the URL name the field suggests: a process
// named for the server itself (web, dev, start, serve) is served at the
// project's name, as a shop's dev server is shop.localhost; any other is
// served at its own.
func processURLPlaceholder(st *processDialogState) string {
	name := urlName(st.name)
	switch name {
	case "", "web", "dev", "start", "serve":
		return urlName(st.projectName)
	}
	return name
}

// urlName makes a URL name out of a process's or project's name: what a
// name may hold, lowercased, with the rest turned into hyphens.
func urlName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// processURLError is what is wrong with the URL typed, if anything.
func processURLError(st *processDialogState) string {
	name := strings.TrimSpace(st.url)
	if name == "" {
		return ""
	}
	if err := procs.ValidURL(strings.ToLower(name)); err != nil {
		return err.Error()
	}
	return ""
}

// applyProcessSuggestion fills the form from a suggestion: what it has,
// such as a Solo process's env and watch globs, and nothing it hasn't. A
// suggestion that serves HTTP is given the project's name as its URL, so
// that it is reachable as https://<project>.localhost, unless the user
// typed a URL of their own.
func (a *appState) applyProcessSuggestion(st *processDialogState, def procs.Def) {
	st.name, st.command, st.dir = def.Name, def.Command, def.Dir
	st.port = ""
	if def.Port > 0 {
		st.port = strconv.Itoa(def.Port)
	}
	st.watch, st.env = strings.Join(def.Watch, "\n"), formatProcessEnv(def.Env)
	if !servesHTTP(def) {
		return
	}
	if strings.TrimSpace(st.url) != "" && st.url != st.autoURL {
		return
	}
	if name := urlName(st.projectName); name != "" {
		st.url, st.autoURL = name, name
	}
}

// servesHTTP tells whether a suggested process is a server: one with a
// port, or one named for a server script, as a dev or start script is.
func servesHTTP(def procs.Def) bool {
	if def.Port > 0 {
		return true
	}
	switch def.Name {
	case "dev", "start", "serve", "preview", "storybook", "rails", "django", "go", "compose":
		return true
	}
	return false
}

func restartLabel(policy string) string {
	switch policy {
	case "on-failure":
		return "On failure"
	case "always":
		return "Always"
	default:
		return "Never"
	}
}

func restartValue(label string) string {
	switch label {
	case "On failure":
		return "on-failure"
	case "Always":
		return "always"
	default:
		return "never"
	}
}

func formatProcessEnv(env map[string]string) string {
	var lines []string
	for key, value := range env {
		lines = append(lines, key+"="+value)
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

func parseProcessEnv(text string) (map[string]string, error) {
	env := map[string]string{}
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("environment line %d must be KEY=value", n+1)
		}
		key = strings.TrimSpace(key)
		if _, exists := env[key]; exists {
			return nil, fmt.Errorf("environment key %q is listed twice", key)
		}
		env[key] = strings.TrimSpace(value)
	}
	return env, nil
}

func processDefFromDialog(st *processDialogState) (procs.Def, error) {
	port := 0
	if strings.TrimSpace(st.port) != "" {
		var err error
		port, err = strconv.Atoi(strings.TrimSpace(st.port))
		if err != nil {
			return procs.Def{}, fmt.Errorf("the port must be a number")
		}
	}
	env, err := parseProcessEnv(st.env)
	if err != nil {
		return procs.Def{}, err
	}
	var watch []string
	for _, line := range strings.Split(st.watch, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			watch = append(watch, line)
		}
	}
	return procs.Def{Name: strings.TrimSpace(st.name), Command: strings.TrimSpace(st.command), Dir: strings.TrimSpace(st.dir),
		Port: port, URL: strings.ToLower(strings.TrimSpace(st.url)), Autostart: st.autostart, Restart: restartValue(st.restart), Watch: watch, Env: env}, nil
}

func processDirectoryError(st *processDialogState) string {
	if strings.TrimSpace(st.dir) == "" {
		return ""
	}
	d := procs.Def{Name: "process", Command: "command", Dir: strings.TrimSpace(st.dir)}
	if err := d.Validate(st.root); err != nil && (strings.Contains(err.Error(), "dir") || strings.Contains(err.Error(), "project")) {
		return err.Error()
	}
	return ""
}

func (a *appState) saveProcessDialog(st *processDialogState) {
	fail := func(err error) {
		st.err = err.Error()
		a.toast(nil, err.Error())
	}
	d, err := processDefFromDialog(st)
	if err == nil {
		err = d.Validate(st.root)
	}
	if err != nil {
		fail(err)
		return
	}
	pi := -1
	for i := range a.projects {
		if a.projects[i].ID == st.project {
			pi = i
			break
		}
	}
	if err := a.saveProcessDef(pi, d, st.shared, st.oldName); err != nil {
		fail(err)
		return
	}
	shared := st.shared
	a.processShared = &shared
	if err := a.trustAndSyncProcess(st.project, d.Name); err != nil {
		fail(err)
		return
	}
	st.open = false
	a.toast(nil, "Saved "+d.Name)
}

// saveProcessDef writes a process to one store and removes its old entry.
func (a *appState) saveProcessDef(pi int, def procs.Def, shared bool, oldName string) error {
	if pi < 0 || pi >= len(a.projects) {
		return fmt.Errorf("the project is no longer open")
	}
	project := &a.projects[pi]
	if err := def.Validate(project.Path); err != nil {
		return err
	}
	files, err := procs.LoadFile(project.Path)
	if err != nil {
		return err
	}
	locals := append([]procs.Def(nil), project.Processes...)
	remove := func(defs []procs.Def, name string) []procs.Def {
		out := defs[:0]
		for _, existing := range defs {
			if existing.Name != name {
				out = append(out, existing)
			}
		}
		return out
	}
	if oldName != "" {
		files, locals = remove(files, oldName), remove(locals, oldName)
	}
	for _, defs := range [][]procs.Def{files, locals} {
		for _, existing := range defs {
			if existing.Name == def.Name {
				return fmt.Errorf("a process named %q already exists", def.Name)
			}
		}
	}
	if shared {
		def.Local = false
		files = append(files, def)
	} else {
		def.Local = true
		locals = append(locals, def)
	}
	if err := procs.SaveFile(project.Path, files); err != nil {
		return err
	}
	project.Processes = locals
	a.saveState()
	return nil
}

func (a *appState) trustAndSyncProcess(project, name string) error {
	if a.procHost == nil {
		return nil
	}
	a.procHost.Sync()
	if err := a.procHost.Trust(project, name); err != nil {
		return err
	}
	a.procHost.Sync()
	a.refreshProcList()
	return nil
}
