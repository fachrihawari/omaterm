package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/agent"
	"omaterm/internal/crash"
	"omaterm/internal/model"
	"omaterm/internal/proto"
	"omaterm/internal/session"
)

// Regression tests of the stability work (0.9.2 on): each names the bug
// it keeps fixed.

// A crash a past run left shows a banner until dismissed.
func TestCrashBanner(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.crashes = []crash.Report{{Name: "app", Path: "/nonexistent/crash-app.log"}}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("omaterm quit unexpectedly last time. Its report can help fix it.") {
		t.Fatal("no crash banner")
	}
	if err := tr.Click("Dismiss"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if len(a.crashes) != 0 || tr.HasText("Show Report") {
		t.Fatal("the banner stayed after Dismiss")
	}
}

// A session closed (by the omaterm command, or its shell exiting) in a
// project that isn't shown left that project's kept workspace pointing
// at a tab that was gone: showing it again panicked.
func TestCloseSessionInHiddenProject(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	other := t.TempDir()
	pi := a.addProject(other, "")
	a.switchProject(pi)
	for i := 0; i < 2; i++ {
		a.newTerminalTab()
	}
	last := a.tabs[len(a.tabs)-1].ID
	a.activeTab = len(a.tabs) - 1
	a.switchProject(0) // the other project is kept, its last tab active
	if err := a.cliCloseSession(last); err != nil {
		t.Fatal(err)
	}
	a.switchProject(pi)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame() // panicked: index out of range
	if a.activeTab >= len(a.tabs) {
		t.Fatalf("activeTab %d with %d tabs", a.activeTab, len(a.tabs))
	}
}

// A shell exiting in a project that isn't shown closes its session there
// too: it stayed as a dead session.
func TestPaneExitInHiddenProject(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	other := t.TempDir()
	pi := a.addProject(other, "")
	a.switchProject(pi)
	a.newTerminalTab()
	tab := a.tabs[a.activeTab]
	id := tab.Split.termIDs()[0]
	if a.term(id) == nil {
		t.Fatal("the pane did not start")
	}
	a.switchProject(0)
	a.paneExited(id)
	if _, _, _, ok := a.findPane(id); ok {
		t.Fatal("the exited pane's session is still there")
	}
}

// A state.json that can't be read is kept, copied, and never written over
// or used to end panes; 0.9.1 started empty, ended every daemon pane, and
// removed the file as the window closed.
func TestBrokenStateKept(t *testing.T) {
	path := stateFilePath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	broken := []byte(`{"version": 2, "projects": [`)
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Remove(path)
		bad, _ := filepath.Glob(path + ".bad-*")
		for _, f := range bad {
			os.Remove(f)
		}
	})
	a := newApp()
	defer stopTermPanes(a)
	if a.stateLoadFailed == "" {
		t.Fatal("the broken file went unnoticed")
	}
	if b, err := os.ReadFile(a.stateLoadFailed); err != nil || string(b) != string(broken) {
		t.Fatalf("no copy of the broken file: %v", err)
	}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("Show File") {
		t.Fatal("no banner about the broken file")
	}
}

// A project whose folder is missing at launch keeps its sessions; 0.9.1
// dropped it, and its sessions, at the next save.
func TestMissingProjectFolderKept(t *testing.T) {
	path := stateFilePath()
	gone := filepath.Join(t.TempDir(), "unplugged")
	st := model.State{Projects: []model.Project{{ID: "pgone", Name: "gone", Dir: gone}},
		Sessions: []model.Session{{ID: "sgone", Project: "pgone", Name: "agent",
			Layout: &model.Layout{Pane: "xgone"}, Panes: []model.Pane{{ID: "xgone", Dir: gone}}}}}
	if err := model.Save(path, st); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })
	a := newApp()
	defer stopTermPanes(a)
	if len(a.unavailable) != 1 || a.unavailable[0].ID != "pgone" {
		t.Fatalf("unavailable = %+v", a.unavailable)
	}
	saved := a.state()
	if len(saved.Projects) != 1 || len(saved.Sessions) != 1 || saved.Sessions[0].ID != "sgone" {
		t.Fatalf("the save drops the missing project: %+v", saved)
	}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("folder missing") {
		t.Fatal("the sidebar doesn't show the missing project")
	}
}

// A session's focused pane, zoom and agent-given name, and the session a
// project showed, come back after a restart; 0.9.1 saved none of them, so
// the window always opened on the first pane of the first session.
func TestSessionLayoutSurvivesRestart(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	for i := 0; i < 2; i++ {
		a.newTerminalTab()
	}
	second := a.activeTab
	tab := &a.tabs[second]
	first := tab.Split.TermID
	tab.Split = &splitNode{Dir: splitVertical, Ratio: 50, First: &splitNode{TermID: first}, Second: &splitNode{TermID: "zzpane"}}
	tab.Zoomed = "zzpane"
	tab.AgentNamed = true
	a.activePaneTermID = "zzpane"
	sessionID := tab.ID

	path := stateFilePath()
	if err := model.Save(path, a.state()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })

	b := newApp()
	defer stopTermPanes(b)
	pi := -1
	for i, p := range b.projects {
		if p.Path == a.projects[0].Path {
			pi = i
		}
	}
	if pi < 0 {
		t.Fatal("the project didn't come back")
	}
	b.switchProject(pi)
	got := b.tabs[b.activeTab]
	if got.ID != sessionID {
		t.Fatalf("shows session %s, want %s", got.ID, sessionID)
	}
	if b.activePaneTermID != "zzpane" || got.Zoomed != "zzpane" || !got.AgentNamed {
		t.Fatalf("focus %q, zoomed %q, agentNamed %v", b.activePaneTermID, got.Zoomed, got.AgentNamed)
	}
}

// A name the user gives a session sticks: an agent's next conversation
// renamed it back to the shell's when an agent had named it first.
func TestUserRenameBeatsAgentName(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.tabs[0].AgentNamed = true
	a.renameSession(0, a.tabs[0].ID, "mine")
	if a.tabs[0].AgentNamed || a.tabs[0].Label != "mine" {
		t.Fatalf("label %q, agentNamed %v", a.tabs[0].Label, a.tabs[0].AgentNamed)
	}
}

// The agent a pane runs, and its conversation, outlive the pane's status:
// a session daemon that restarted took the status with it, and the pane
// came back as a bare shell.
func TestAgentKeptWhenStatusIsLost(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	id := a.tabs[0].Split.termIDs()[0]
	conv := &agent.Session{Agent: "claude", ID: "conv-1"}
	a.keepAgents(map[string]session.Info{id: {ID: id, Agent: "claude", AgentSession: conv, Process: "claude"}})
	delete(a.status, id) // the daemon went away with it
	var saved *agent.Session
	for _, s := range a.state().Sessions {
		for _, p := range s.Panes {
			if p.ID == id {
				saved = p.Agent
			}
		}
	}
	if saved == nil || saved.ID != "conv-1" {
		t.Fatalf("saved agent = %+v, want conversation conv-1", saved)
	}
	// Back at the prompt, the agent quit: nothing to resume.
	a.keepAgents(map[string]session.Info{id: {ID: id}})
	if a.paneAgents[id] != nil {
		t.Fatal("an agent that quit is still kept")
	}
}

// When another host takes over the panes (a session daemon, after
// "Restart Sessions"), it is the one run and followed: the old host's
// goroutines went on, and the status froze for the rest of the run.
func TestStatusFollowsNewHost(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.startPanes()
	defer func() { a.panesStop() }()
	h := &runSpy{Host: session.NewLocalHost(), ran: make(chan struct{}, 1)}
	a.setPanes(h)
	select {
	case <-h.ran:
	case <-time.After(3 * time.Second):
		t.Fatal("the new host is never run, nor its changes followed")
	}
}

// runSpy is a host that tells when it is run.
type runSpy struct {
	session.Host
	ran chan struct{}
}

func (h *runSpy) Run(ctx context.Context) {
	select {
	case h.ran <- struct{}{}:
	default:
	}
	h.Host.Run(ctx)
}

// 0.8 kept the text size and the diff layout in ui.json; moving them into
// the settings must keep them. 0.9.0 dropped them when no setting changed
// in the first run.
func TestUIPrefsMigrate(t *testing.T) {
	os.MkdirAll(filepath.Dir(uiPrefsPath()), 0o755)
	if err := os.WriteFile(uiPrefsPath(), []byte(`{"fontSize": 15, "diffSplit": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(uiPrefsPath()) })
	a := newApp()
	defer stopTermPanes(a)
	if a.fontSize != 15 || a.settings.Terminal.FontSize != 15 {
		t.Fatalf("font size %v / %v, want 15", a.fontSize, a.settings.Terminal.FontSize)
	}
	if a.diffSplit || a.settings.Editor.DiffSplit {
		t.Fatal("the unified diff layout was lost")
	}
}

// A settings.json that can't be read is copied aside before the defaults
// replace it.
func TestBrokenSettingsKept(t *testing.T) {
	os.MkdirAll(filepath.Dir(settingsFilePath()), 0o755)
	if err := os.WriteFile(settingsFilePath(), []byte(`{"theme": "dark",`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Remove(settingsFilePath())
		if settingsBroken != "" {
			os.Remove(settingsBroken)
			settingsBroken = ""
		}
	})
	s := loadSettings()
	if s.Theme != defaultSettings().Theme {
		t.Fatalf("theme %q, want the default", s.Theme)
	}
	if b, err := os.ReadFile(settingsBroken); err != nil || string(b) != `{"theme": "dark",` {
		t.Fatalf("no copy of the broken settings: %v", err)
	}
}

// Sessions the session daemon made while the window was reconnecting to
// it are taken from state.json; the window's next save dropped them, and
// the next launch ended their panes.
func TestAdoptSessionsMadeWhileReconnecting(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	st := a.state()
	st.Sessions = append(st.Sessions, model.Session{ID: "fromweb", Project: a.projects[0].ID, Name: "web",
		Layout: &model.Layout{Pane: "webpane"}, Panes: []model.Pane{{ID: "webpane", Dir: a.projects[0].Path}}})
	path := stateFilePath()
	if err := model.Save(path, st); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(path) })
	a.adoptSavedSessions()
	if _, _, _, ok := a.findSession("fromweb"); !ok {
		t.Fatal("the session the daemon made was not adopted")
	}
	n := len(a.tabs)
	a.adoptSavedSessions()
	if len(a.tabs) != n {
		t.Fatal("adopted twice")
	}
}

// With the daemon off but its service installed, a banner asks to settle
// it, and agents aren't resumed meanwhile.
func TestServiceConflictBanner(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.serviceConflict = true
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("Turn the Daemon Back On") {
		t.Fatal("no banner")
	}
}

// The unsaved-changes dialog closes the file it asked about, though tabs
// moved while it asked; it closed whatever took that tab's place.
func TestCloseConfirmFollowsItsTab(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.openEditor("hello.txt")
	a.editors["hello.txt"].State.Buffer.Set("changed\n")
	a.closeActiveTab(nil)
	if a.closeTabConfirmOpen == nil {
		t.Fatal("no confirmation for an unsaved file")
	}
	// A session is made before it while the dialog asks.
	a.tabs = append([]topTab{{ID: "new", Kind: tabTerminal, Label: "new", Split: &splitNode{TermID: "newpane"}}}, a.tabs...)
	a.closePendingEditor()
	for _, tab := range a.tabs {
		if tab.Path == "hello.txt" {
			t.Fatal("the file's tab is still open")
		}
	}
	if a.tabs[0].ID != "new" {
		t.Fatal("another tab was closed")
	}
}

// A dialog answers first: the palette under it closes, and the omaterm
// command's actions wait. They covered it, two scrims deep.
func TestDialogBeatsOverlays(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.openOverlay(overlayCommandPalette)
	a.closeAsk = &closeAsk{open: true, title: "Close Session", text: "claude is still running.", run: func() {}}
	a.queuedActions = append(a.queuedActions, "app.commandPalette")
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if a.overlay != overlayNone {
		t.Fatal("the palette stayed over the dialog")
	}
	if len(a.queuedActions) != 1 {
		t.Fatal("a queued action ran over the dialog")
	}
	a.openOverlay(overlayQuickOpen)
	if a.overlay != overlayNone {
		t.Fatal("an overlay opened over the dialog")
	}
}

// The terminal view asked for while a file's tab is shown shows a session;
// it added a split to the file's tab, starting a shell nothing closed.
func TestTerminalViewNeverSplitsAFileTab(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.openEditor("hello.txt")
	file := a.activeTab
	a.activeView = tabTerminal
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if a.tabs[file].Split != nil {
		t.Fatal("the file's tab got a terminal")
	}
	if a.tabs[a.activeTab].Kind != tabTerminal {
		t.Fatal("the terminal view doesn't show a session")
	}
}

// A session rename is taken when the field loses the keyboard to a click
// in the window, as with Enter; it was dropped.
func TestRenameSavedOnClickAway(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	a.renaming, a.renameText = a.tabs[0].ID, "renamed"
	tr.Frame()
	tr.Frame()
	if !a.renameFocused {
		t.Fatal("the rename field never took the keyboard")
	}
	if err := tr.Click("Projects"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.tabs[0].Label != "renamed" || a.renaming != "" {
		t.Fatalf("label %q, still renaming %q", a.tabs[0].Label, a.renaming)
	}
}

// omaterm restart runs a pane's command again, with the environment and
// project it was made with; it started a bare shell, and the app dropped
// --env and the project.
func TestRestartKeepsCommandAndEnv(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	cmd := []string{"sh", "-c", "echo got-$OMATERM_TEST_ENV; sleep 30"}
	made, err := a.cliCreate(proto.CreateParams{Name: "dev", Dir: a.projects[0].Path, Command: cmd, Env: []string{"OMATERM_TEST_ENV=yes"}})
	if err != nil {
		t.Fatal(err)
	}
	pane := made.(proto.Created).Pane
	h := a.panes.Get(pane)
	if h == nil {
		t.Fatal("no pane")
	}
	if got := h.Info().Project; got != a.projects[0].ID {
		t.Errorf("project %q, want %q", got, a.projects[0].ID)
	}
	waitText := func(want string) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if strings.Contains(a.panes.Get(pane).Text(false, 0), want) {
				return
			}
		}
		t.Fatalf("the pane never showed %q:\n%s", want, a.panes.Get(pane).Text(false, 0))
	}
	waitText("got-yes")
	a.status[pane] = h.Info()
	if err := a.cliRestart(pane); err != nil {
		t.Fatal(err)
	}
	if got := a.panes.Get(pane).Info().Command; strings.Join(got, " ") != strings.Join(cmd, " ") {
		t.Fatalf("restarted with %q, want %q", got, cmd)
	}
}

// A timer that fired just before its refresh was stopped must not start
// it again: the Info tab's process list refreshed on after it was hidden,
// the system stats after the window closed.
func TestStoppedTimersStayStopped(t *testing.T) {
	a := newTestApp(t)
	gen := a.processGen
	a.stopProcessRefresh()
	if a.processTick(gen) {
		t.Error("process refresh restarted after it was stopped")
	}
	gen = a.sysGen
	a.stopSystemStats()
	if a.sysTick(gen) {
		t.Error("system stats restarted after they were stopped")
	}
}

// Scrollback read just before its pane closed must not be written after:
// the file stayed behind, the pane's output on disk for good.
func TestNoHistoryAfterClose(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a := newTestApp(t)
	p, err := a.panes.Start(session.StartOptions{ID: "p1", Command: []string{"sleep", "30"}, Exec: true, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	id := p.ID()
	a.writeHistory(id, []byte("open"))
	if _, err := os.Stat(historyPath(id)); err != nil {
		t.Fatalf("an open pane's scrollback isn't written: %v", err)
	}
	a.closePaneRuntime(id)
	a.writeHistory(id, []byte("late"))
	if _, err := os.Stat(historyPath(id)); err == nil {
		t.Error("scrollback written after its pane closed")
	}
}
