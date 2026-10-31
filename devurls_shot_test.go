package main

// Renders Dev URLs in the app's UI for a look: the process dialog with a
// URL, and a process row and tab with its link pill, in the light palette.

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/procs"
	"omaterm/internal/theme"
	"omaterm/service"
)

func TestDevURLsShot(t *testing.T) {
	dir := os.Getenv("DEVURLS_SHOT_DIR")
	if dir == "" {
		t.Skip("DEVURLS_SHOT_DIR is not set")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	setPalette(theme.BuiltinLight())
	defer stopTermPanes(a)

	// The processes as the daemon's supervisor lists them: running, with
	// the link its proxy gives the routed one.
	a.useLocalProcHost()
	defer a.procHost.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go a.panes.Run(ctx)
	defer cancel()
	// A process that runs at once, so its pane has a header to show.
	defs := []procs.Def{
		{Name: "web", Command: "sh -c 'echo up; sleep 30'"},
		{Name: "api", Command: "sh -c 'echo api; sleep 30'"},
	}
	if err := procs.SaveFile(a.workspace, defs); err != nil {
		t.Fatal(err)
	}
	local := a.procHost.(*localProcs)
	local.Sync()
	projectID := a.projects[a.active].ID
	for _, def := range defs {
		if err := a.procHost.Trust(projectID, def.Name); err != nil {
			t.Fatal(err)
		}
	}
	local.Sync()
	tr := ui.NewTester(a.view, 1440, 900)
	tr.SetDark(false)
	tr.Frame()
	for _, def := range defs {
		a.startProcess(a.active, def.Name)
	}
	waitProcessFrame(t, tr, func() bool {
		list := a.procList[projectID]
		return len(list) == 2 && list[0].Status == procs.Running && list[1].Status == procs.Running
	})
	// What the daemon's supervisor and its proxy give the processes: the
	// ports they listen on, and the link of the routed one.
	for i := range a.procList[projectID] {
		switch a.procList[projectID][i].Name {
		case "web":
			a.procList[projectID][i].Port = 5173
			a.procList[projectID][i].URL = "shop"
			a.procList[projectID][i].Link = "https://shop.localhost"
		case "api":
			a.procList[projectID][i].Port = 3001
		}
	}
	tr.Frame()

	shot := func(name string) {
		os.MkdirAll(dir, 0o755)
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tr.Image()); err != nil {
			t.Fatal(err)
		}
	}
	shot("sidebar")

	// The process tab, with the link beside its name and in its header.
	a.showProcess(a.active, "web")
	waitProcessFrame(t, tr, func() bool {
		pane := a.panes.Get(a.tabs[a.activeTab].Split.TermID)
		return pane != nil && strings.Contains(pane.Text(false, 0), "up")
	})
	shot("tab")

	// The inspector's PORTS, naming the routed port's URL.
	a.showInspectorTab(0)
	a.stopProcessRefresh()
	a.ports = []service.Port{{Port: 5173, Name: "http", Open: true, Pid: 4242, Process: "node"}}
	tr.Frame()
	shot("inspector")
	a.ports = nil

	// The dialog, editing the routed process.
	def := a.procList[a.projects[a.active].ID][0].Def
	a.openProcessDialog(a.active, &def)
	tr.Frame()
	shot("dialog")
	a.procDialog.open = false
	tr.Frame()
}
