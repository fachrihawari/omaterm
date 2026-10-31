package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/procs"
)

// captureNotice replaces the app's notification for a test, and returns
// what it was told.
func captureNotice(t *testing.T) *[]string {
	t.Helper()
	sent := &[]string{}
	old := processNotice
	processNotice = func(a *appState, sessionID, title, body string) {
		*sent = append(*sent, sessionID+"|"+title+"|"+body)
	}
	t.Cleanup(func() { processNotice = old })
	return sent
}

func TestProcessFailureNotifies(t *testing.T) {
	setConfigHome(t, t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	sent := captureNotice(t)
	a.settings.Notify.Error = true

	a.processEvent(procs.Event{Project: "p1", Name: "web", Kind: procs.EventFailed, Code: 1})
	if len(*sent) != 1 {
		t.Fatalf("a failed process did not notify: %v", *sent)
	}
	want := procs.SessionID("p1", "web") + "|Process failed: web|exit 1"
	if (*sent)[0] != want {
		t.Fatalf("notification: %q, want %q", (*sent)[0], want)
	}

	// Paused restarts are the same trouble, with their own words.
	a.processEvent(procs.Event{Project: "p1", Name: "worker", Kind: procs.EventPaused})
	if len(*sent) != 2 {
		t.Fatalf("paused restarts did not notify: %v", *sent)
	}
	if got, want := (*sent)[1], procs.SessionID("p1", "worker")+"|Restart paused: worker|restarted too often"; got != want {
		t.Fatalf("notification: %q, want %q", got, want)
	}
}

func TestProcessFailureIsQuietWhenTheSettingIsOff(t *testing.T) {
	setConfigHome(t, t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	sent := captureNotice(t)
	a.settings.Notify.Error = false

	a.processEvent(procs.Event{Project: "p1", Name: "web", Kind: procs.EventFailed, Code: 1})
	if len(*sent) != 0 {
		t.Fatalf("a notification was sent with the setting off: %v", *sent)
	}
}

// A start or a stop was asked for, and says nothing.
func TestProcessLifecycleIsQuiet(t *testing.T) {
	setConfigHome(t, t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	sent := captureNotice(t)
	a.settings.Notify.Error = true

	a.processEvent(procs.Event{Project: "p1", Name: "web", Kind: procs.EventStarted})
	a.processEvent(procs.Event{Project: "p1", Name: "web", Kind: procs.EventStopped})
	if len(*sent) != 0 {
		t.Fatalf("a start or a stop notified: %v", *sent)
	}
}

// One notification every five seconds a process: a crash loop does not
// fill the screen with notifications.
func TestProcessFailureNotifiesOnceInABurst(t *testing.T) {
	setConfigHome(t, t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	sent := captureNotice(t)
	a.settings.Notify.Error = true

	for range 5 {
		a.processEvent(procs.Event{Project: "p1", Name: "web", Kind: procs.EventFailed, Code: 1})
	}
	if len(*sent) != 1 {
		t.Fatalf("a burst notified %d times", len(*sent))
	}
}

// The port a running process listens on opens its page, and clicking it
// does not also open the process's tab.
func TestProcessPortPillOpensThePage(t *testing.T) {
	setConfigHome(t, t.TempDir())
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.settings.OpenLinks = "pane"
	a.useLocalProcHost()
	local := a.procHost.(*localProcs)
	defer local.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go a.panes.Run(ctx)
	defer cancel()
	if err := os.WriteFile(filepath.Join(a.workspace, procs.FileName),
		[]byte("processes:\n  web:\n    command: \"sleep 30\"\n    port: 5173\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	def := procs.Def{Name: "web", Command: "sleep 30", Port: 5173}
	if err := procs.OpenTrust(procs.TrustPath()).Add(a.workspace, def.Variant()); err != nil {
		t.Fatal(err)
	}
	local.Sync()
	projectID := a.projects[a.active].ID
	if err := local.Start(projectID, "web"); err != nil {
		t.Fatal(err)
	}
	tr := ui.NewTester(a.view, 1440, 900)
	waitProcessFrame(t, tr, func() bool {
		list := a.procList[projectID]
		return len(list) == 1 && (list[0].Status == procs.Starting || list[0].Status == procs.Running)
	})
	browsers := func() int { return len(a.paneURLs) }
	before := browsers()
	pillRect, ok := tr.Find(":5173")
	if !ok {
		t.Fatalf("the port pill is missing: %+v", a.procList[projectID])
	}
	tr.Move(pillRect.X+pillRect.W/2, pillRect.Y+pillRect.H/2)
	tr.Frame()
	if err := tr.Click(":5173"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if got := browsers(); got != before+1 {
		t.Fatalf("the port opened %d browser panes, want 1", got-before)
	}
	// The click was the port's, not the row's: it did not open the
	// process's tab as well.
	if a.activeView == tabProcess {
		t.Fatal("clicking the port also opened the process tab")
	}
}
