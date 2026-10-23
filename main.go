package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/cli"
	"omaterm/internal/clilink"
	"omaterm/internal/ctl"
	"omaterm/internal/paths"
	"omaterm/internal/theme"
)

// appVersion is the app's version, from mygo.json as built.
func appVersion() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return "dev"
}

// startControl serves the control socket, unless the settings say not to,
// and links the omaterm command where panes find it.
func (a *appState) startControl() {
	if exe, err := os.Executable(); err == nil {
		if err := clilink.Link(exe, paths.BinDir()); err != nil {
			log.Printf("linking the omaterm command: %v", err)
		}
	}
	if !a.settings.ControlSocket {
		return
	}
	a.host = newLocalHost(a)
	s, err := ctl.Listen(paths.SocketPath(), a.host, appVersion())
	if err != nil {
		log.Printf("control socket: %v", err)
		return
	}
	a.ctl = s
}

// openFromArgs opens the folder an `omaterm <dir>` names as a project.
func (a *appState) openFromArgs(args []string, wd string) {
	if a.win != nil {
		a.win.Show()
		a.win.Focus()
	}
	if len(args) == 0 {
		return
	}
	dir := args[0]
	if !filepath.IsAbs(dir) && wd != "" {
		dir = filepath.Join(wd, dir)
	}
	dir, _ = filepath.Abs(dir)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return
	}
	a.switchProject(a.addProject(dir, ""))
}

func main() {
	// omaterm <command> is the CLI: it drives the running app's sessions
	// over the control socket, and never opens a window.
	if len(os.Args) > 1 && cli.IsCommand(os.Args[1]) {
		cli.Version = appVersion()
		os.Exit(cli.Run(os.Args[1:]))
	}
	// A dev build started from a pane must not take the pane's variables
	// for its own.
	for _, k := range []string{"OMATERM_PANE", "OMATERM_SESSION", "OMATERM_SOCKET"} {
		os.Unsetenv(k)
	}

	// Updates come from the signed manifests of the GitHub releases named in
	// mygo.json. The native window is the one for this app: omaterm shows
	// no web page, so it needs no webview to offer an update. The app's own
	// Info panel and palette check from there; a menu bar would not exist on
	// Linux, where omaterm mostly runs.
	mygo.Use(native.Plugin)
	updatesReady = true

	// One app at a time: a second `omaterm [dir]` shows the first one, with
	// the folder it names opened as a project.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	var app *appState
	mygo.App.OnSecondInstance(func(args []string, wd string) {
		if app == nil || app.win == nil {
			return
		}
		app.win.Update(func() { app.openFromArgs(args, wd) })
	})

	mygo.App.WhenReady(func() {
		a := newApp()
		app = a
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "omaterm",
			Width:           1440,
			Height:          900,
			MinWidth:        900,
			MinHeight:       560,
			BackgroundColor: theme.Hex(pal.Wash),
			StateKey:        "main",
			Content:         ui.View(a.view),
		})
		a.win.OnClosed(func() {
			a.stopSystemStats()
			if a.themeWatchStop != nil {
				a.themeWatchStop()
			}
			a.saveState()
			a.saveHistories(true)
			a.saveUIPrefs()
			if a.ctl != nil {
				a.ctl.Close()
			}
			if a.panesStop != nil {
				a.panesStop()
			}
			a.panes.CloseAll()
		})
		// Git status goes stale while the app is in the background; reload
		// it when the window comes back instead of asking for a button.
		a.win.OnFocus(func() {
			a.recheckTheme()
			if a.inspectorVisible && a.inspectorTab == 2 {
				a.refreshActiveRepo()
			}
		})
		a.start()
		a.startControl()
		a.openFromArgs(os.Args[1:], "")
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
