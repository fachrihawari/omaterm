package main

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/procs"
)

func TestProcessMoveAndRemove(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.useLocalProcHost()
	defer a.procHost.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go a.panes.Run(ctx)
	defer cancel()

	def := procs.Def{Name: "web", Command: "sh -c 'echo up; sleep 30'"}
	if err := procs.SaveFile(a.workspace, []procs.Def{def}); err != nil {
		t.Fatal(err)
	}
	local := a.procHost.(*localProcs)
	local.Sync()
	if err := a.procHost.Trust(a.projects[a.active].ID, def.Name); err != nil {
		t.Fatal(err)
	}
	local.Sync()
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	a.startProcess(a.active, "web")
	waitProcessFrame(t, tr, func() bool {
		return len(a.procList[a.projects[a.active].ID]) == 1 && a.procList[a.projects[a.active].ID][0].Status == procs.Running
	})
	processContextClick(t, tr, "web", "Keep Local")
	if defs, err := procs.LoadFile(a.workspace); err != nil || len(defs) != 0 {
		t.Fatalf("file definitions after Keep Local = %+v, err=%v", defs, err)
	}
	if state := a.state(); len(state.Projects[0].Processes) != 1 || state.Projects[0].Processes[0].Name != "web" {
		t.Fatalf("local definitions after Keep Local = %+v", state.Projects[0].Processes)
	}
	if list := a.procHost.List(a.projects[a.active].ID); len(list) != 1 || !list[0].Trusted {
		t.Fatalf("the moved process lost trust: %+v", list)
	}
	if list := a.procList[a.projects[a.active].ID]; list[0].Status != procs.Running || !list[0].Local {
		t.Fatalf("a running process did not keep its new local source: %+v", list)
	}

	processContextClick(t, tr, "web", "Move to omaterm.yml")
	if defs, err := procs.LoadFile(a.workspace); err != nil || len(defs) != 1 || defs[0].Name != "web" {
		t.Fatalf("file definitions after Move to omaterm.yml = %+v, err=%v", defs, err)
	}
	if state := a.state(); len(state.Projects[0].Processes) != 0 {
		t.Fatalf("local definitions after Move to omaterm.yml = %+v", state.Projects[0].Processes)
	}

	tr.Frame()
	if err := tr.Click("web"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.activeView != tabProcess {
		t.Fatalf("process tab did not open: view=%v", a.activeView)
	}
	processContextClick(t, tr, "web", "Remove Process…")
	tr.Frame()
	if !tr.HasText("a running one is stopped") {
		t.Fatalf("remove confirmation is missing: %v", tr.Texts())
	}
	if err := tr.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if defs, err := procs.LoadFile(a.workspace); err != nil || len(defs) != 0 {
		t.Fatalf("file definitions after remove = %+v, err=%v", defs, err)
	}
	if len(a.state().Projects[0].Processes) != 0 || len(a.procHost.List(a.projects[a.active].ID)) != 0 {
		t.Fatal("removed process remains in project state or the process list")
	}
	if a.activeView == tabProcess {
		t.Fatal("removed process tab is still open")
	}
	if _, ok := tr.Find("web"); ok {
		t.Fatal("removed process is still shown in the sidebar")
	}
}

func TestProcessStartAllAndStopAll(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.useLocalProcHost()
	defer a.procHost.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go a.panes.Run(ctx)
	defer cancel()

	defs := []procs.Def{
		{Name: "api", Command: "sh -c 'sleep 30'"},
		{Name: "web", Command: "sh -c 'sleep 30'"},
		{Name: "private", Command: "sh -c 'sleep 31'"},
	}
	if err := procs.SaveFile(a.workspace, defs); err != nil {
		t.Fatal(err)
	}
	local := a.procHost.(*localProcs)
	local.Sync()
	projectID := a.projects[a.active].ID
	for _, def := range defs[:2] {
		if err := a.procHost.Trust(projectID, def.Name); err != nil {
			t.Fatal(err)
		}
	}
	local.Sync()
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	projectHeader, ok := tr.Find(a.projects[a.active].Name)
	if !ok {
		t.Fatal("project header is missing")
	}
	tr.RightClickAt(projectHeader.X+projectHeader.W/2, projectHeader.Y+projectHeader.H/2)
	tr.Frame()
	if err := tr.ChooseMenuItem("Start All Processes"); err != nil {
		t.Fatal(err)
	}
	waitProcessFrame(t, tr, func() bool {
		list := a.procList[projectID]
		return len(list) == 3 && list[0].Status == procs.Running && list[1].Status == procs.Running
	})
	if !tr.HasText("2 started, 1 not trusted: start it to review it") {
		t.Fatalf("Start All toast missing: %v", tr.Texts())
	}
	tr.Frame()
	projectHeader, ok = tr.Find(a.projects[a.active].Name)
	if !ok {
		t.Fatal("project header disappeared")
	}
	tr.RightClickAt(projectHeader.X+projectHeader.W/2, projectHeader.Y+projectHeader.H/2)
	tr.Frame()
	if err := tr.ChooseMenuItem("Stop All Processes"); err != nil {
		t.Fatal(err)
	}
	waitProcessFrame(t, tr, func() bool {
		list := a.procList[projectID]
		return len(list) == 3 && list[0].Status == procs.Stopped && list[1].Status == procs.Stopped
	})
}

func processContextClick(t *testing.T, tr *ui.Tester, row, item string) {
	t.Helper()
	rect, ok := tr.Find(row)
	if !ok {
		t.Fatalf("process row %q is missing: %v", row, tr.Texts())
	}
	tr.RightClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tr.Frame()
	if err := tr.ChooseMenuItem(item); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
}
