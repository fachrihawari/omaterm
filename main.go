package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/cli"
	"omaterm/internal/clilink"
	"omaterm/internal/crash"
	"omaterm/internal/ctl"
	"omaterm/internal/daemon"
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

// appMenu is the macOS menu bar: the app's menu (⌘Q, ⌘H), Edit (copy,
// paste, undo) and Window (⌘M). mygo's default one also has File and View,
// whose keys come before the window's: ⌘W closed the window, so quit the
// app, and ⌘R, ⇧⌘R and the zoom keys did nothing at all. omaterm's own
// table has those keys. Linux and Windows get no menu bar.
func appMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu}, {Role: mygo.RoleEditMenu}, {Role: mygo.RoleWindowMenu},
	})
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
	a.hostReady.Store(a.host)
	s, err := ctl.Listen(paths.SocketPath(), a.host, appVersion())
	if err != nil {
		log.Printf("control socket: %v", err)
		return
	}
	a.ctl = s
}

// openFromArgs opens the folder an `omaterm <dir>` names as a project, or
// shows the session `omaterm --session=<id>` names.
func (a *appState) openFromArgs(args []string, wd string) {
	if a.win != nil {
		a.win.Show()
		a.win.Focus()
	}
	if len(args) == 0 {
		return
	}
	// --session=<id> shows a session: a notification of the session
	// daemon's, clicked while the window was closed, opens the app so.
	if id, ok := strings.CutPrefix(args[0], "--session="); ok {
		a.showSession(id)
		return
	}
	// --settings=<page> opens the Settings window: omaterm settings.
	if page, ok := strings.CutPrefix(args[0], "--settings="); ok {
		a.openSettings(page)
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
	// The session daemon the app starts: no window, the panes' shells.
	if len(os.Args) > 1 && os.Args[1] == "__daemon" {
		crash.Watch(paths.StateDir(), "daemon", appVersion())
		err := daemon.Run(daemon.Options{Version: daemon.Build(appVersion()),
			Serve: os.Getenv("OMATERM_SERVICE") != "", Restore: os.Getenv("OMATERM_SERVICE") != ""})
		if err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	// omaterm <command> is the CLI: it drives the running app's sessions
	// over the control socket, and never opens a window.
	if len(os.Args) > 1 && cli.IsCommand(os.Args[1]) {
		cli.Version, cli.Build = appVersion(), daemon.Build(appVersion())
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
	// A crash prints its report and is gone: keep it, and tell the user
	// of the ones past runs left.
	crashes := crash.Unseen(paths.StateDir())
	crash.Watch(paths.StateDir(), "app", appVersion())
	// The daemon removes them too, and says so in its log; without it,
	// they would stay.
	paths.RemoveLegacy(paths.StateDir())
	var app *appState
	mygo.App.OnSecondInstance(func(args []string, wd string) {
		if app == nil || app.win == nil {
			return
		}
		app.win.Update(func() { app.openFromArgs(args, wd) })
	})

	mygo.App.WhenReady(func() {
		// Once ready, as mygo's own examples do: the roles' labels name
		// the app.
		if runtime.GOOS == "darwin" {
			mygo.App.SetMenu(appMenu())
		}
		systemDark = mygo.Theme.IsDark
		a := newApp()
		a.crashes = crashes
		app = a
		a.useDaemon()
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "omaterm",
			Width:           1440,
			Height:          900,
			MinWidth:        600,
			MinHeight:       400,
			BackgroundColor: theme.Hex(pal.Wash),
			StateKey:        "main",
			Content:         ui.View(a.view),
		})
		a.postWin.Store(a.win)
		a.win.OnClose(a.beforeClose)
		a.win.OnClosed(func() {
			a.windowClosing = true
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
			a.checkDisk(a.activeEditor()) // changed while away
			if a.inspectorShown && a.inspectorTab == 1 {
				a.rebuildFileTree()
			}
			if a.inspectorShown && a.inspectorTab == 2 {
				a.refreshActiveRepo()
			}
		})
		// Following the desktop: its switches between light and dark.
		mygo.Theme.OnUpdated(func() {
			if a.themeMode == themeSystem {
				a.applyTheme(themeSource(themeSystem))
				a.invalidateWindows()
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
