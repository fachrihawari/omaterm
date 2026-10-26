package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/agent"
	"omaterm/internal/crash"
	"omaterm/internal/model"
	"omaterm/internal/proto"
	"omaterm/internal/session"
	"omaterm/service"
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

// A project's repos or files that arrive after another project is shown,
// or after the last one was removed, are dropped: the Git panel showed
// the last project's changes and wrote its branch into the new project,
// and with no project left the app crashed.
func TestStaleProjectResultsDropped(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	shown := a.workspace
	a.applyRepos("/elsewhere", []service.Repo{{Root: "/elsewhere", Name: "other", Branch: "other-branch"}})
	if len(a.repos) > 0 && a.repos[0].Root == "/elsewhere" {
		t.Error("another project's repos shown")
	}
	if p := a.project(); p != nil && strings.HasPrefix(p.Branch, "other-branch") {
		t.Error("another project's branch written into this one")
	}
	a.applyFileIndex("/elsewhere", []string{"stale.go"})
	if slices.Contains(a.fileIndex, "stale.go") {
		t.Error("another project's files in quick open")
	}

	// The last project removed while its repos load.
	repos := service.DiscoverRepos(shown, 1)
	a.active = -1
	a.applyRepos(a.workspace, repos)
}

// Unsaved edits are never lost unasked: not by a tab's ×, which closed
// the editor where Ctrl+W asked, nor by quitting, closing the window or
// relaunching, which never asked.
func TestUnsavedEditorsAsk(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.openEditor("hello.txt")
	doc := a.editors["hello.txt"]
	doc.State.Buffer.Set("edited\n")
	if !doc.State.Buffer.Modified() {
		t.Fatal("the edit didn't mark the buffer modified")
	}
	i := slices.IndexFunc(a.tabs, func(t topTab) bool { return t.Kind == tabEditor })
	a.closeTabAsking(i)
	if a.editors["hello.txt"] == nil || a.closeTabConfirmOpen == nil {
		t.Error("the tab's × closed unsaved edits without asking")
	}
	a.closeTabConfirmOpen = nil

	// Another project's unsaved editor counts too.
	a.ws["other"] = &projectWS{editors: map[string]*openFile{"b.go": doc}}
	if got := a.unsavedEditors(); !slices.Equal(got, []string{"b.go", "hello.txt"}) {
		t.Errorf("unsaved editors %v", got)
	}
	var e mygo.CloseEvent
	a.beforeClose(&e)
	if !e.DefaultPrevented() || a.quitAsk == nil {
		t.Error("closing the window with unsaved edits didn't ask")
	}

	// Agreed to: the quit goes through.
	a.quitAsk, a.quitAnyway = nil, true
	e = mygo.CloseEvent{}
	a.beforeClose(&e)
	if e.DefaultPrevented() {
		t.Error("the quit the user agreed to was stopped again")
	}
}

// A file changed on disk while open, by an agent say, is never
// overwritten unasked: an unmodified editor takes the change, and saving
// a modified one asks. Saving wrote the stale text over the change.
func TestEditorFollowsDisk(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	path := filepath.Join(a.workspace, "hello.txt")
	a.openEditor("hello.txt")
	doc := a.editors["hello.txt"]

	os.WriteFile(path, []byte("agent wrote this\n"), 0o644)
	a.checkDisk(doc)
	if got := doc.State.Buffer.String(); got != "agent wrote this\n" {
		t.Errorf("an unmodified editor shows %q, not the file's new text", got)
	}

	doc.State.Buffer.Set("mine\n")
	os.WriteFile(path, []byte("agent again\n"), 0o644)
	a.checkDisk(doc)
	if doc.State.Buffer.String() != "mine\n" {
		t.Fatal("a modified editor lost its edits to the file's change")
	}
	tr := ui.NewTester(func(c *ui.Context) { a.saveEditor(doc, c) }, 400, 300)
	tr.Frame()
	if data, _ := os.ReadFile(path); string(data) != "agent again\n" {
		t.Errorf("saving overwrote the change on disk unasked: %q", data)
	}
	if a.overwriteAsk == nil {
		t.Error("saving over a change on disk didn't ask")
	}

	// Agreed to: it writes, keeping the file's mode.
	os.Chmod(path, 0o755)
	tr = ui.NewTester(func(c *ui.Context) { a.writeEditor(doc, c) }, 400, 300)
	tr.Frame()
	if data, _ := os.ReadFile(path); string(data) != "mine\n" {
		t.Errorf("the file holds %q after the save", data)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o755 {
		t.Errorf("the save changed the file's mode to %v", st.Mode().Perm())
	}
}

// Discarding a file's changes asks first; and a stash is found by its
// commit, not where the list showed it: a quick second Drop dropped the
// stash that had moved into the first one's place.
func TestDiscardAsksAndStashByHash(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	root := a.workspace
	file := filepath.Join(root, "hello.txt")
	a.askDiscard(root, "hello.txt", false)
	if data, _ := os.ReadFile(file); string(data) != "one\ntwo\n" {
		t.Fatalf("discarded before the user confirmed: %q", data)
	}
	if a.closeAsk == nil {
		t.Fatal("no confirmation asked")
	}
	a.closeAsk.run()
	if data, _ := os.ReadFile(file); string(data) != "one\n" {
		t.Errorf("confirmed, the file holds %q", data)
	}

	for _, msg := range []string{"first", "second"} {
		os.WriteFile(file, []byte(msg+"\n"), 0o644)
		if err := service.StashPush(root, msg, false); err != nil {
			t.Fatal(err)
		}
	}
	a.applyRepos(root, service.DiscoverRepos(root, 1))
	stashes, _ := service.StashList(root)
	first := stashes[1].Hash // stash@{1}
	service.StashDrop(root, 0)
	i, ok := a.stashIndex(nil, first)
	if !ok || i != 0 {
		t.Errorf("the first stash found at %d (%v), want stash@{0} now", i, ok)
	}
}

// Every way to end a session that still runs a program says so first:
// Remove Project and the palette's Close Pane ended agents and servers
// without a word, as the pane's × didn't.
func TestEndingRunningAsks(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.settings.ConfirmClose = true
	ids := a.projectPaneIDs(a.projects[0].ID)
	if len(ids) == 0 {
		t.Fatal("the project has no panes")
	}
	a.status[ids[0]] = session.Info{ID: ids[0], Status: session.Working, Agent: "claude"}
	if text := a.endingText(ids, 0); !strings.Contains(text, "Claude Code is still running") {
		t.Errorf("Remove Project says %q: not that Claude Code runs", text)
	}

	// Split, so Close Pane closes the pane, not the tab.
	other := a.splitPane(ids[0], splitVertical)
	a.status[other] = session.Info{ID: other, Status: session.Working, Process: "vite"}
	a.activePaneTermID = other
	tr := ui.NewTester(func(c *ui.Context) { a.closeActivePane(c) }, 400, 300)
	tr.Frame()
	if !slices.Contains(a.activeSplit().termIDs(), other) || a.closeAsk == nil {
		t.Fatal("Close Pane ended a running program without asking")
	}
	a.closeAsk.run()
	if slices.Contains(a.activeSplit().termIDs(), other) {
		t.Error("confirmed, the pane stayed")
	}
}

// Running without a session daemon of another version, which still runs
// the sessions, resumes no agent here: each conversation ran twice.
func TestRunWithoutDaemonNoResume(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	open := true
	a.daemonRefusedOpen = &open
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if err := tr.Click("Run Without It"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	a.settings.RestoreSessions, a.settings.ResumeAgents = true, true
	a.restoring["p9"] = true
	a.paneAgents["p9"] = &agent.Session{Agent: "claude", ID: "abc"}
	if _, cmd := a.restoreOptions("p9"); cmd != nil {
		t.Errorf("an agent resumed here too: %v", cmd)
	}
	if !tr.HasText("Restart omaterm") {
		t.Error("no banner says the sessions also run elsewhere")
	}
}

// `omaterm do` runs nothing that ends every session, writes outside
// omaterm or lets a device in: an agent with remote control ran
// daemon.restart, ending every session.
func TestRemoteActionsSafe(t *testing.T) {
	for _, id := range []string{"daemon.restart", "agents.setup", "cli.install", "web.pair", "app.quit", "git.commit"} {
		if remoteAction(id) {
			t.Errorf("omaterm do may run %s", id)
		}
	}
	if !remoteAction("terminal.new") {
		t.Error("omaterm do may not open a terminal")
	}
}

// Goroutines read the pane host while the UI thread swaps it (a session
// daemon taking over): run under -race, a plain field read raced.
func TestHostSwapNoRace(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			a.offPanes().Get("none")
			a.writeHistory("none", nil)
		}
	}()
	for i := 0; i < 200; i++ {
		a.setPanes(session.NewLocalHost())
	}
	<-done
}

// A control-socket call answered "busy" never runs later: a create the
// caller was told failed happened anyway, and an agent that tried again
// made a second session.
func TestBusyCallNeverRuns(t *testing.T) {
	a := newApp()
	ran := false
	_, err := onUIWithin(a, 50*time.Millisecond, true, func() (any, error) { ran = true; return nil, nil })
	if err == nil {
		t.Fatal("a call the UI thread never took was answered")
	}
	a.drainPosted() // the UI thread frees up
	if ran {
		t.Error("the call answered busy ran after all")
	}
}
