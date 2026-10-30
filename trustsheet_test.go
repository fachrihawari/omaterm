package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/procs"
)

// seedUntrusted writes a project with one process, web, and no trust in it.
func seedUntrusted(t *testing.T, a *appState) {
	t.Helper()
	body := "processes:\n  web:\n    command: \"sh -c 'sleep 30'\"\n    port: 3000\n"
	if err := os.WriteFile(filepath.Join(a.workspace, procs.FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTrustSheetTrustsThenStarts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.useLocalProcHost()
	local := a.procHost.(*localProcs)
	defer local.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go a.panes.Run(ctx)
	defer cancel()
	seedUntrusted(t, a)
	local.Sync()
	projectID := a.projects[a.active].ID

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("untrusted") {
		t.Fatal("an untrusted process shows no untrusted badge")
	}
	row, ok := tr.Find("web")
	if !ok {
		t.Fatal("web row is missing")
	}
	tr.Move(row.X+40, row.Y+row.H/2)
	tr.Frame()
	if err := tr.Click("Start"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.trustAsk == nil || !a.trustAsk.open {
		t.Fatal("Start did not open the trust sheet")
	}
	if !tr.HasText("Trust web?") || !tr.HasText("sh -c 'sleep 30'") || !tr.HasText("3000") {
		t.Fatalf("the sheet does not show what the process runs: %v", tr.Texts())
	}
	if local.s.List(projectID)[0].Status != procs.Untrusted {
		t.Fatal("opening the sheet trusted the process")
	}

	if err := tr.Click("Trust and Start"); err != nil {
		t.Fatal(err)
	}
	waitProcessFrame(t, tr, func() bool {
		return a.procList[projectID][0].Status == procs.Running && a.trustAsk == nil
	})
	file, err := procs.LoadFile(a.workspace)
	if err != nil || len(file) != 1 {
		t.Fatalf("defs %v %v", file, err)
	}
	if !procs.OpenTrust(procs.TrustPath()).Trusted(a.workspace, file[0].Variant()) {
		t.Fatal("Trust and Start did not keep the trust")
	}
}

func TestTrustSheetCancelStartsNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.useLocalProcHost()
	local := a.procHost.(*localProcs)
	defer local.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go a.panes.Run(ctx)
	defer cancel()
	seedUntrusted(t, a)
	local.Sync()
	projectID := a.projects[a.active].ID

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	row, ok := tr.Find("web")
	if !ok {
		t.Fatal("web row is missing")
	}
	tr.Move(row.X+40, row.Y+row.H/2)
	tr.Frame()
	if err := tr.Click("Start"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if err := tr.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	waitProcessFrame(t, tr, func() bool { return a.trustAsk == nil })
	if got := a.procList[projectID][0]; got.Status != procs.Untrusted || got.Trusted {
		t.Fatalf("cancel left %+v", got)
	}
	file, _ := procs.LoadFile(a.workspace)
	if procs.OpenTrust(procs.TrustPath()).Trusted(a.workspace, file[0].Variant()) {
		t.Fatal("cancel trusted the definition")
	}
}
