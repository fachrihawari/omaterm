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
	"omaterm/service"
)

// runProcesses starts the app's own process host and a running process
// named web, which the tests then give the link the daemon's proxy would.
func runProcesses(t *testing.T, a *appState) (projectID string, tr *ui.Tester) {
	t.Helper()
	a.useLocalProcHost()
	t.Cleanup(func() { a.procHost.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	go a.panes.Run(ctx)
	t.Cleanup(cancel)
	def := procs.Def{Name: "web", Command: "sh -c 'echo up; sleep 30'", Port: 5173, URL: "shop"}
	if err := procs.SaveFile(a.workspace, []procs.Def{def}); err != nil {
		t.Fatal(err)
	}
	local := a.procHost.(*localProcs)
	local.Sync()
	projectID = a.projects[a.active].ID
	if err := a.procHost.Trust(projectID, def.Name); err != nil {
		t.Fatal(err)
	}
	local.Sync()
	tr = ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	a.startProcess(a.active, def.Name)
	waitProcessFrame(t, tr, func() bool {
		list := a.procList[projectID]
		return len(list) == 1 && list[0].Status == procs.Running
	})
	return projectID, tr
}

// The dialog's URL field is saved with the definition, and a name a Dev
// URL can't take is refused with a note.
func TestProcessDialogURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.useLocalProcHost()
	defer a.procHost.Close()

	tr := ui.NewTester(a.view, 1440, 900)
	a.openProcessDialog(a.active, nil)
	tr.Frame()
	fillProcessDialog(t, tr, "shop", "npm run dev")
	if err := tr.Click("URL input"); err != nil {
		t.Fatal(err)
	}
	tr.Type("shop")
	tr.Frame()
	if !tr.HasText(".localhost") {
		t.Fatalf("the field lacks its .localhost suffix: %v", tr.Texts())
	}
	// A URL without a port has one picked for it, told as PORT.
	if !tr.HasText("picks a free port") {
		t.Fatalf("the free-port note is missing: %v", tr.Texts())
	}
	if err := tr.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	defs, err := procs.LoadFile(a.workspace)
	if err != nil || len(defs) != 1 || defs[0].URL != "shop" {
		t.Fatalf("saved definitions = %+v, err=%v", defs, err)
	}

	// A name a Dev URL can't take is refused, with the reason under the
	// field, and nothing is saved.
	a.openProcessDialog(a.active, nil)
	tr.Frame()
	fillProcessDialog(t, tr, "api", "echo api")
	if err := tr.Click("URL input"); err != nil {
		t.Fatal(err)
	}
	tr.Type("shop/x")
	tr.Frame()
	if !tr.HasText(`the url "shop/x" must be parts of`) {
		t.Fatalf("the URL error note is missing: %v", tr.Texts())
	}
	if err := tr.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if defs, err := procs.LoadFile(a.workspace); err != nil || len(defs) != 1 {
		t.Fatalf("a bad URL was saved: %+v, err=%v", defs, err)
	}
}

// A suggestion for a server fills the URL with the project's name, and
// leaves a URL the user typed.
func TestProcessDialogSuggestionFillsURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.useLocalProcHost()
	defer a.procHost.Close()
	if err := os.WriteFile(filepath.Join(a.workspace, "package.json"), []byte(`{"scripts":{"dev":"vite"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := ui.NewTester(a.view, 1440, 900)
	a.openProcessDialog(a.active, nil)
	tr.Frame()
	if err := tr.Click("npm run dev"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	st := a.procDialog
	if st == nil || st.url != "test" {
		t.Fatalf("the suggestion did not fill the URL from the project: %+v", st)
	}
	// The URL the user typed stays when another suggestion is taken.
	st.url = "api"
	tr.Frame()
	a.applyProcessSuggestion(st, procs.Def{Name: "dev", Command: "npm run dev", Port: 5173})
	if st.url != "api" {
		t.Fatalf("a suggestion replaced the URL the user typed: %q", st.url)
	}
	// A watcher serves nothing: it is given no URL.
	st.url, st.autoURL = "", ""
	a.applyProcessSuggestion(st, procs.Def{Name: "watch", Command: "npm run watch"})
	if st.url != "" {
		t.Fatalf("a watcher was given a URL: %q", st.url)
	}
	// A dev script whose port isn't known still gets one: it serves.
	a.applyProcessSuggestion(st, procs.Def{Name: "dev", Command: "node server.js"})
	if st.url != "test" {
		t.Fatalf("a dev script without a known port got no URL: %q", st.url)
	}
	st.open = false
	tr.Frame()
}

// A routed process shows its host in place of its port, and the host opens
// it; one whose URL the proxy isn't serving keeps its port, with a note on
// where to set Dev URLs up.
func TestProcessRowDevURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	projectID, tr := runProcesses(t, a)

	// Served at its Dev URL.
	a.procList[projectID][0].Link = "https://shop.localhost"
	a.settings.OpenLinks = "pane"
	tr.Frame()
	if !tr.HasText("shop.localhost") || tr.HasText(":5173") {
		t.Fatalf("the row does not show the host in place of the port: %v", tr.Texts())
	}
	if err := tr.Click("shop.localhost"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	var opened bool
	for _, url := range a.paneURLs {
		if url == "https://shop.localhost" {
			opened = true
		}
	}
	if !opened {
		t.Fatalf("clicking the host opened nothing: %v", a.paneURLs)
	}

	// The process's tab and its pane header name the host too.
	a.showProcess(a.active, "web")
	tr.Frame()
	if n := countText(tr, "shop.localhost"); n < 3 {
		t.Fatalf("the host shows %d times, want the row, the tab and the header: %v", n, tr.Texts())
	}

	// The row's menu opens it as well.
	processContextClick(t, tr, "web", "Open shop.localhost")
	opened = false
	for _, url := range a.paneURLs {
		if url == "https://shop.localhost" {
			opened = true
		}
	}
	if !opened {
		t.Fatalf("Open shop.localhost opened nothing: %v", a.paneURLs)
	}

	// The URL is there, but the proxy isn't serving it: the port stays,
	// with a tip on where to set Dev URLs up.
	a.procList[projectID][0].Link = ""
	tr.Frame()
	if !tr.HasText(":5173") {
		t.Fatalf("a process without a link lost its port: %v", tr.Texts())
	}
	port, ok := tr.Find(":5173")
	if !ok {
		t.Fatalf("the port pill is missing: %v", tr.Texts())
	}
	tr.Move(port.X+port.W/2, port.Y+port.H/2)
	tr.Frame()
	tr.Advance(700 * time.Millisecond)
	tr.Frame()
	if !tr.HasText("Set up Dev URLs in Settings to serve it at https://shop.localhost") {
		t.Fatalf("the Dev URLs hint is missing: %v", tr.Texts())
	}
}

// countText is how many of the frame's texts hold s.
func countText(tr *ui.Tester, s string) int {
	n := 0
	for _, text := range tr.Texts() {
		if strings.Contains(text, s) {
			n++
		}
	}
	return n
}

// A process's URL can be copied from its row, whether the proxy serves it
// or not.
func TestProcessCopyURL(t *testing.T) {
	var copied []string
	prev := copyText
	copyText = func(text string) { copied = append(copied, text) }
	t.Cleanup(func() { copyText = prev })

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	projectID, tr := runProcesses(t, a)

	a.procList[projectID][0].Link = "https://shop.localhost"
	tr.Frame()
	processContextClick(t, tr, "web", "Copy URL")
	if len(copied) != 1 || copied[0] != "https://shop.localhost" {
		t.Fatalf("Copy URL copied %q", copied)
	}
	if !tr.HasText("Copied https://shop.localhost") {
		t.Fatalf("Copy URL said nothing: %v", tr.Texts())
	}

	// Without a link, it copies the localhost address.
	copied = nil
	a.procList[projectID][0].Link = ""
	tr.Frame()
	processContextClick(t, tr, "web", "Copy URL")
	if len(copied) != 1 || copied[0] != "http://localhost:5173" {
		t.Fatalf("Copy URL without a link copied %q", copied)
	}
	if !tr.HasText("Copied http://localhost:5173") {
		t.Fatalf("Copy URL said nothing: %v", tr.Texts())
	}
}

// The inspector's PORTS names a routed process's Dev URL, and opens it.
func TestInspectorPortDevURL(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.showInspectorTab(0)
	a.stopProcessRefresh()
	a.settings.OpenLinks = "pane"
	projectID := a.projects[a.active].ID
	a.ports = []service.Port{{Port: 5173, Name: "http", Open: true, Pid: 4242, Process: "node"}}
	a.procList = map[string][]procs.Proc{projectID: {
		{Def: procs.Def{Name: "web", Command: "npm run dev", Port: 5173, URL: "shop"},
			Status: procs.Running, Link: "https://shop.localhost"},
	}}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("shop.localhost") {
		t.Fatalf("the port does not name its Dev URL: %v", tr.Texts())
	}
	if err := tr.Click("Open https://shop.localhost"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	var opened bool
	for _, url := range a.paneURLs {
		if url == "https://shop.localhost" {
			opened = true
		}
	}
	if !opened {
		t.Fatalf("the inspector opened nothing: %v", a.paneURLs)
	}

	// Without a link it names the process and opens the port.
	a.procList[projectID][0].Link = ""
	tr.Frame()
	if err := tr.Click("Open http://localhost:5173"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	opened = false
	for _, url := range a.paneURLs {
		if url == "http://localhost:5173" {
			opened = true
		}
	}
	if !opened {
		t.Fatalf("the inspector opened nothing without a link: %v", a.paneURLs)
	}
}
