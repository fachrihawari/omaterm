package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/procs"
)

func TestProcessSidebarAndTab(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.useLocalProcHost()
	local := a.procHost.(*localProcs)
	defer local.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go a.panes.Run(ctx)
	defer cancel()
	web := procs.Def{Name: "web", Command: "sh -c 'echo up; sleep 30'"}
	if err := os.WriteFile(filepath.Join(a.workspace, procs.FileName), []byte("processes:\n  web:\n    command: \"sh -c 'echo up; sleep 30'\"\n  private:\n    command: \"sleep 30\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	trust := procs.OpenTrust(procs.TrustPath())
	if err := trust.Add(a.workspace, web.Variant()); err != nil {
		t.Fatal(err)
	}
	local.Sync()
	projectID := a.projects[a.active].ID
	if len(a.procList[projectID]) != 2 || a.procList[projectID][1].Status != procs.Untrusted {
		t.Fatalf("process list: %+v", a.procList[projectID])
	}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	row, ok := tr.Find("web")
	if !ok || row.X > a.sidebarWidth() {
		t.Fatalf("web process row not in the sidebar: found=%v rect=%+v", ok, row)
	}
	before := len(a.visibleTabIndices())
	tr.Move(row.X+40, row.Y+row.H/2)
	tr.Frame()
	if err := tr.Click("Start"); err != nil {
		t.Fatal(err)
	}
	waitProcessFrame(t, tr, func() bool {
		return len(a.procList[projectID]) == 2 && a.procList[projectID][0].Status == procs.Running
	})
	if len(a.visibleTabIndices()) != before {
		t.Fatalf("starting a process added a tab to the tab bar: %v", a.visibleTabIndices())
	}
	if len(a.state().Sessions) != 1 || len(a.protoState(time.Now()).Sessions) != 2 {
		t.Fatalf("process session state: saved=%+v proto=%+v", a.state().Sessions, a.protoState(time.Now()).Sessions)
	}
	var processSession bool
	for _, s := range a.protoState(time.Now()).Sessions {
		if s.Name == "web" && s.Process == "web" && len(s.Panes) == 1 {
			processSession = true
		}
	}
	if !processSession {
		t.Fatal("the app control state lacks the web process session")
	}
	tr.Frame()
	if err := tr.Click("web"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.activeView != tabProcess || a.tabs[a.activeTab].Process != "web" || a.tabs[a.activeTab].Split == nil {
		t.Fatalf("web process tab: view=%v tab=%+v", a.activeView, a.tabs[a.activeTab])
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tr.Frame()
		if pane := a.panes.Get(a.tabs[a.activeTab].Split.TermID); pane != nil && strings.Contains(pane.Text(false, 0), "up") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pane := a.panes.Get(a.tabs[a.activeTab].Split.TermID); pane == nil || !strings.Contains(pane.Text(false, 0), "up") {
		t.Fatal("web process output is missing from its pane")
	}
	if len(a.visibleTabIndices()) != before {
		t.Fatal("the process tab appeared in the tab bar")
	}
	// The untrusted process uses the same Start entry point: the app trusts
	// its definition, then starts it.
	privateRow, ok := tr.Find("private")
	if !ok {
		t.Fatal("private process row is missing")
	}
	tr.Move(privateRow.X+40, privateRow.Y+privateRow.H/2)
	tr.Frame()
	if err := tr.Click("Start"); err != nil {
		t.Fatal(err)
	}
	waitProcessFrame(t, tr, func() bool {
		return a.procList[projectID][1].Status == procs.Starting || a.procList[projectID][1].Status == procs.Running
	})
	if a.procList[projectID][1].Trusted != true {
		t.Fatalf("Start did not trust the private definition: %+v", a.procList[projectID][1])
	}
	webRow, ok := tr.Find("web")
	if !ok {
		t.Fatal("web row disappeared")
	}
	tr.Move(webRow.X+40, webRow.Y+webRow.H/2)
	tr.Frame()
	if err := tr.Click("Stop"); err != nil {
		t.Fatal(err)
	}
	waitProcessFrame(t, tr, func() bool { return a.procList[projectID][0].Status == procs.Stopped })
	if a.activeView != tabProcess || a.tabs[a.activeTab].Split != nil || !tr.HasText("Stopped") {
		t.Fatalf("stopped process placeholder: tab=%+v", a.tabs[a.activeTab])
	}
}

func waitProcessFrame(t *testing.T, tr *ui.Tester, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tr.Frame()
		if ready() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("process did not reach the expected state")
}
