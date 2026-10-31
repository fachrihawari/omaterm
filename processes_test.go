package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/procs"
	"omaterm/internal/proto"
)

func TestProcessSidebarAndTab(t *testing.T) {
	setConfigHome(t, t.TempDir())
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
	// Its pane's header names the process and its command, not the shell
	// running it.
	tr.Frame()
	if !tr.HasText("sh -c 'echo up; sleep 30'") {
		t.Fatalf("the process pane's header lacks its command: %q", tr.Texts())
	}
	// The process's tab is a tab like any other: in the tab bar, and on
	// Next Tab's way.
	if got := a.visibleTabIndices(); len(got) != before+1 || !slices.Contains(got, a.activeTab) {
		t.Fatalf("the process tab is not in the tab bar: %v (active %d)", got, a.activeTab)
	}
	webTab := a.activeTab
	a.cycleTab(1)
	if a.activeTab == webTab || a.activeView != tabTerminal {
		t.Fatalf("Next Tab stayed on the process tab: view=%v", a.activeView)
	}
	for range a.tabs {
		if a.activeTab == webTab {
			break
		}
		a.cycleTab(1)
	}
	if a.activeTab != webTab || a.activeView != tabProcess {
		t.Fatalf("Next Tab never came back to the process tab: view=%v", a.activeView)
	}
	// Its × hides the tab; the process goes on, and the toast says so.
	a.queuedToasts = nil
	a.closeTabAsking(webTab)
	if len(a.visibleTabIndices()) != before || a.procList[projectID][0].Status != procs.Running {
		t.Fatalf("closing the process tab: tabs=%v status=%v", a.visibleTabIndices(), a.procList[projectID][0].Status)
	}
	if !slices.Contains(a.queuedToasts, "web keeps running. Stop it from the sidebar.") {
		t.Fatalf("closing the process tab said %q", a.queuedToasts)
	}
	tr.Frame()
	if err := tr.Click("web"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.activeView != tabProcess || a.tabs[a.activeTab].Process != "web" {
		t.Fatal("the sidebar row did not bring the process tab back")
	}
	// The untrusted process uses the same Start entry point: the app asks
	// the user to trust its definition, then starts it.
	privateRow, ok := tr.Find("private")
	if !ok {
		t.Fatal("private process row is missing")
	}
	tr.Move(privateRow.X+40, privateRow.Y+privateRow.H/2)
	tr.Frame()
	if err := tr.Click("Start"); err != nil {
		t.Fatal(err)
	}
	// Start opens the trust sheet first; its button trusts, then starts.
	tr.Frame()
	if err := tr.Click("Trust and Start"); err != nil {
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

// A process's session in the state carries the process: its status, port,
// link and message, so a client of the state needs no second call.
func TestProtoStateProcessSessionCarriesItsProcess(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	projectID := a.projects[a.active].ID
	def := procs.Def{Name: "web", Command: "serve", URL: "shop", Port: 4001}
	a.procList = map[string][]procs.Proc{projectID: {
		{Def: def, Status: procs.Running, Pane: "proc-web-1", Session: procs.SessionID(projectID, "web"), Trusted: true, Link: "https://shop.localhost", Message: "hello"},
		{Def: procs.Def{Name: "idle", Command: "x"}, Status: procs.Stopped, Session: procs.SessionID(projectID, "idle")}, // no pane: no session
	}}
	var found *proto.Session
	st := a.protoState(time.Now())
	for i := range st.Sessions {
		if st.Sessions[i].Process == "web" {
			found = &st.Sessions[i]
		}
	}
	if found == nil || found.Proc == nil {
		t.Fatalf("no process session with its process: %+v", st.Sessions)
	}
	p := found.Proc
	if p.Status != procs.Running || p.Port != 4001 || p.Link != "https://shop.localhost" || p.Message != "hello" || !p.Trusted || p.URL != "shop" {
		t.Fatalf("proc %+v", p)
	}
	b, _ := json.Marshal(found)
	if !strings.Contains(string(b), `"proc":{`) || !strings.Contains(string(b), `"link":"https://shop.localhost"`) {
		t.Fatalf("json: %s", b)
	}
	// A session that isn't a process has none.
	for _, s := range st.Sessions {
		if s.Process == "" && s.Proc != nil {
			t.Fatalf("a plain session has a proc: %+v", s)
		}
	}
}

// Sharing a process needs the session daemon: the app's own supervisor
// says so, and ending a share it never had is no error.
func TestLocalProcessesCannotShare(t *testing.T) {
	var host procHost = &localProcs{}
	if err := host.Share("p", "web", "tailscale"); err == nil || !strings.Contains(err.Error(), "session daemon") {
		t.Fatalf("Share: %v", err)
	}
	if err := host.Unshare("p", "web"); err != nil {
		t.Fatalf("Unshare: %v", err)
	}
}
