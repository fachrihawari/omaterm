package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"omaterm/internal/agent"
	"omaterm/internal/cli"
	"omaterm/internal/crash"
	"omaterm/internal/ctl"
	"omaterm/internal/model"
	"omaterm/internal/notice"
	"omaterm/internal/paths"
	"omaterm/internal/proto"
	"omaterm/internal/session"
	"omaterm/service"
)

// App state: projects, tabs, git data, overlays.

type topTabKind int

const (
	tabTerminal topTabKind = iota
	tabEditor
	tabDiff
	tabGraph
)

type topTab struct {
	// ID names a terminal tab, which is a session: its panes report it,
	// and the CLI targets it.
	ID     string
	Kind   topTabKind
	Label  string
	Detail string
	Dot    int
	Icon   int
	Dirty  bool
	Path   string // editor tabs: workspace-relative file path
	// Split is the terminal pane tree (nil or single leaf = one pane);
	// Zoomed is the pane shown alone, "" for all.
	Split  *splitNode
	Zoomed string
	// AgentNamed tells a name an agent gave the session.
	AgentNamed bool
	// Focus is the pane that had the keyboard when the tab was left;
	// Created when the session was made (unix ms).
	Focus   string
	Created int64
	// Diff is what a diff tab compares.
	Diff *diffSource
	// Worktree is the git worktree a session works in, nil for none.
	Worktree *model.Worktree
}

const (
	dotGreen = iota
	dotYellow
	dotGray
)

const (
	fileIconTS = iota
	fileIconDiff
	fileIconGraph
	fileIconNone = -1
)

// overlayKind selects which modal overlay is open, if any.
type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayQuickOpen
	overlayCommandPalette
	overlayGlobalSearch
	overlayBranch
	overlayShortcuts
)

// diffSourceKind says what the diff view compares.
type diffSourceKind int

const (
	diffWorking diffSourceKind = iota // INDEX ↔ WORKING TREE
	diffStaged                        // HEAD ↔ INDEX
	diffStash                         // stash@{n} patch
	diffCommit                        // commit parent ↔ commit
)

type diffSource struct {
	Kind   diffSourceKind
	Path   string // file path, for working/staged diffs
	Repo   string // repo root
	Stash  int    // stash index, for diffStash
	Commit string // commit hash, for diffCommit
	Title  string // header label
}

// openFile is one open editor document.
type openFile struct {
	Path    string // workspace-relative
	State   *editor.State
	Preview bool // markdown preview mode (default true for .md)
	Binary  bool // not text: show a placeholder instead of the editor
	// TooLarge is a file over maxEditorSize, not read: a placeholder too.
	TooLarge bool
	Size     int64
	Image    *imageSide // a picture: shown in the image viewer
	// Disk is the hash of the file as last read or written: a save checks
	// it, so changes made on disk meanwhile (an agent's) aren't
	// overwritten unasked. checked is when the file was last looked at.
	Disk    [32]byte
	checked time.Time
}

type appState struct {
	win          *mygo.Window
	processTimer *time.Timer
	// historyMu orders a pane's close against saveHistories, which runs
	// off the UI thread.
	historyMu  sync.Mutex
	processGen int // counts stops: a tick that fired before one ends there

	projects  []project
	active    int
	tabs      []topTab
	activeTab int

	activeView topTabKind

	sidebarVisible   bool
	inspectorVisible bool
	// sidebarShown and inspectorShown are what the window shows of them
	// (fitPanels): too narrow for both beside the panes, one gives way.
	// lastPanel is the one the user opened last, which stays.
	sidebarShown, inspectorShown bool
	lastPanel                    string
	sidebarW                     float32
	inspectorW                   float32
	activePaneTermID             string // TermID of the focused terminal pane

	inspectorTab int // 0 info, 1 files, 2 git

	processes []service.Process
	ports     []service.Port

	// Machine load shown in the status bar.
	sysStats   service.SystemStats
	sysSampler service.SystemSampler
	sysTimer   *time.Timer
	sysGen     int // counts stops, as processGen

	commitMsg string

	diffSplit bool

	fileTree *fileNode

	// --- Real workspace/git state ---

	workspace string // absolute path of the active project

	repos      []service.Repo
	activeRepo int
	repoJob    service.Job
	repoErr    string
	// repoFollow is the session the Git panel last followed, repoMain the
	// repo it showed before following one into its worktree, and
	// repoAsked the worktree last searched for.
	repoFollow string
	repoMain   int
	repoAsked  string

	processJob  service.Job
	processErr  string
	processWarn string

	// Git remote operations (fetch/pull/push)
	gitJob  service.Job
	gitBusy string // verb shown while a remote op runs, "" when idle
	gitMsg  string // result of the last remote op
	gitErr  bool   // whether gitMsg describes a failure

	// --- Editor documents ---

	editors   map[string]*openFile // workspace-relative path -> open document
	diffOld   *editor.Buffer
	diffNew   *editor.Buffer
	diffPatch *editor.FilePatch
	diffDs    *editor.DiffState // persisted: scroll state lives inside DiffState
	// diffFiles are the files of a multi-file diff (a commit, a stash),
	// diffFileIdx the one shown, diffFileShow what shows another.
	diffFiles    []editor.FilePatch
	diffFileIdx  int
	diffFileShow func(int)
	diffKey      string // content key the persisted DiffState was built from
	diffGen      int    // bumped every time new diff content is loaded
	diffSrc      diffSource
	diffTitle    string

	// Binary diffs have no text to show; the view renders a placeholder
	// with the file's name and size instead. A picture's two versions go
	// in diffImgOld/diffImgNew and are shown as pictures.
	diffBinary     bool
	diffImage      bool
	diffBinaryName string
	diffBinarySize int64
	diffImgOld     imageSide
	diffImgNew     imageSide
	diffAdd        int // line stats of the shown patch
	diffDel        int

	// --- Overlays ---

	overlay overlayKind

	// editorChord is how far the editor is in a chord of its keys, as
	// ⌘K ⌘0 (Fold All), and editorChordKey the chord's first key: while
	// one waits for its second key, the window's shortcuts leave that key
	// to it.
	editorChord    editorChordStep
	editorChordKey ui.Key

	// Quick open (Ctrl+P)
	quickQuery    string
	quickSelected int
	quickList     ui.ListState
	fileIndex     []string // cached workspace file list
	indexJob      service.Job

	// Command palette (Ctrl+Shift+P)
	cmdQuery    string
	cmdSelected int
	cmdList     ui.ListState

	// Keyboard Shortcuts (Ctrl+Shift+/)
	keyQuery    string
	keySelected int
	keyList     ui.ListState

	// Global search (Ctrl+Shift+F)
	searchQuery    string
	searchSelected int
	searchList     ui.ListState
	searchResults  []service.Match
	searchJob      service.Job
	searchSplit    float32
	searchPreview  *editor.State // read-only preview editor

	// Branch picker (modal)
	branchQuery    string
	branchSelected int
	branchList     ui.ListState

	// Git stash
	stashMsg string

	// Git branch switching
	branches      []string
	branchesRepo  string
	currentBranch string

	// Git graph
	commits        []service.Commit
	graphLoaded    bool // commits has been read at least once
	expandedCommit string
	graphJob       service.Job
	graphLimit     int
	commitFiles    map[string][]service.CommitFile // per-hash cache: two git calls per frame otherwise
	commitStats    map[string][2]int
	// Git panel collapsible sections
	stagedCollapsed  bool
	changesCollapsed bool
	stashCollapsed   bool
	gitGraphOpen     bool // GIT GRAPH section expanded

	// Terminal panes (Ghostty VT via MyGo's plugin)
	// panes runs the terminal panes of every tab; paneFailed marks those
	// whose shell could not start, so a frame does not retry them; status
	// is a copy of every pane's Info as of the last sync (syncStatus),
	// which views read without locks.
	panes session.Host
	// panesRef is panes for goroutines (offPanes): setPanes swaps panes on
	// the UI thread, and a goroutine reading the field meanwhile raced it.
	panesRef atomic.Pointer[hostRef]
	// ws holds the workspaces of projects not shown, by project ID;
	// wsOwner is the project whose workspace a.tabs is. saved are the
	// sessions state.json had for projects not yet shown, paneDirs the
	// directories their panes start in.
	ws       map[string]*projectWS
	wsOwner  string
	saved    map[string][]model.Session
	paneDirs map[string]string
	// paneAgents are the agent conversations panes ran when saved, which
	// their restore resumes (Phase 5).
	paneAgents map[string]*agent.Session
	// paneURLs are the pages of browser panes, which run no shell, and
	// paneTitles their titles; webs their web views (browser.go).
	// restoring are the panes of the last run not yet started again.
	restoring  map[string]bool
	paneURLs   map[string]string
	paneTitles map[string]string
	webs       map[string]*webEntry
	saveTimer  *time.Timer
	panesStop  func()
	// ctl serves the control socket; host is what it calls.
	ctl *ctl.Server
	// queuedActions are commands `omaterm do` asked for, which the next
	// frame runs.
	queuedActions []string
	// queuedToasts are messages for the next frame, from where there is
	// no ui.Context.
	queuedToasts []string
	// daemonStale is the version of the session daemon the sessions run
	// in, when not this one's; daemonRefused* ask to restart a daemon of
	// another protocol.
	daemonStale        string
	daemonReconnecting bool
	// agentSetupOffer is the agent seen without its hook, whose banner
	// offers the agent integration; agentSetupAsked, that it was answered
	// (ui.json). hookChecked caches whether each agent's hook is in.
	agentSetupOffer string
	agentSetupAsked bool
	hookChecked     map[string]bool
	// wtNew and wtMerge are the New Agent in Worktree and Merge dialogs,
	// open while set; wtAgent, wtEnv and mergeSquash their last choices.
	wtNew       *worktreeNew
	wtMerge     *worktreeMerge
	wtAgent     string
	wtEnv       bool
	mergeSquash bool
	// paneShown is when each pane was last shown: a daemon pane hidden
	// long lets go of its terminal in the app (detachHidden).
	paneShown         map[string]time.Time
	daemonRefusedOpen *bool
	daemonRefusedPID  int
	host              *localHost
	git               *gitWatch

	// The panes' layout: handles of keyed elements, a divider being
	// dragged, the drop zone last shown, the drop preview's target and
	// since when, and panes just moved, which spring in.
	handles    map[string]*ui.Handle
	layoutDrag struct {
		split string
		ratio float64
	}
	lastZone     dropZone
	previewKey   string
	previewSince time.Time
	arrived      map[string]bool

	// changes is the Changes panel's state; snaps the turn snapshots
	// being taken.
	changes changeSet
	snaps   turnSnapshots

	// renaming is the session being renamed in the sidebar, renameText
	// its name as typed.
	// notified is when each session last notified.
	notified map[string]time.Time
	title    string
	// termFont is the terminals' font family (fonts.go).
	termFont string
	// lastViewed is the session last told to the panes as viewed.
	lastViewed string

	renaming   string
	renameText string
	// renameFocused tells the rename field had the keyboard.
	renameFocused bool
	paneFailed    map[string]bool
	status        map[string]session.Info

	// settings are the user's preferences (settings.json); themeMode is
	// settings.Theme as shown, themeStamp the Omarchy theme.name mtime it
	// last loaded, themeWatchStop ends the theme watch.
	settings     Settings
	settingsUI   settingsUI
	settingsPage string
	settingsWin  *mygo.Window
	// termMade is termBehavior as the open terminals were made.
	termMade       termBehavior
	themeMode      string
	themeStamp     time.Time
	themeWatchStop func()

	// posted is work handed to the UI thread while there is no window, as
	// in tests: view runs it at the start of the next frame (see post).
	postedMu sync.Mutex
	posted   []func()
	// termCmd runs instead of the user's shell, for the screenshot suite:
	// a live shell prints its prompt at an unpredictable moment.
	termCmd []string
	// fontSize is the terminal's and the editor's text size in DIPs; 0
	// is the default 13.
	fontSize float32

	// Close tab confirmation
	// pendingClosePath is the editor tab the confirmation closes, by its
	// file: tabs may move while it asks.
	pendingClosePath    string
	closeTabConfirmOpen *bool
	// quitAsk is the dialog asking to quit with editors unsaved; quitWant
	// what to do once the user agrees ("quit" or "relaunch"), and
	// quitAnyway lets that quit through.
	// refocusPane gives the keyboard to the active pane as it is drawn,
	// after the one that had it closed.
	refocusPane bool
	// diffTried is the diff tab's source showDiffTab last tried to show,
	// in the workspace diffTriedFor.
	diffTried    *diffSource
	diffTriedFor string
	// overwriteAsk asks to save over a file changed on disk since it
	// was opened; overwriteDoc is that file's editor.
	overwriteAsk *bool
	overwriteDoc *openFile
	quitAsk      *bool
	quitWant     string
	quitAnyway   bool
	// tabInView is the tab the tab bar last scrolled into view;
	// closingTab, one past the index of a tab whose ✕ was clicked.
	tabInView, closingTab int
	// closeAsk is a session or pane to close once the user confirms.
	closeAsk *closeAsk
	// bells is when each pane's bell last rang, which its card flashes.
	bells map[string]time.Time
	// crashes are the crash reports of past runs the user hasn't seen.
	crashes []crash.Report
	// stateLoadFailed is the copy of a state.json that couldn't be read:
	// while it is set, nothing is saved over the file and no pane ends.
	stateLoadFailed string
	// unavailable are projects whose folder is missing, kept as saved.
	unavailable []model.Project
	// remoteControl and gitSnapshots are those settings for goroutines
	// (the control socket's, the panes' hooks), which must not read
	// a.settings as the UI thread changes it.
	remoteControl, gitSnapshots, controlSocket atomic.Bool
	// hostRef is host for the daemon's forwarded calls, which arrive on
	// a goroutine, as soon as the daemon is in use: before startControl
	// sets host.
	hostReady atomic.Pointer[localHost]
	// serviceConflict tells that the daemon is off but its systemd
	// service would start it: agents aren't resumed until it is settled.
	serviceConflict bool
	// savedIDs are the sessions and projects state.json held as last read
	// or written (rememberSaved); saveDeferred tells that a save waits for
	// the daemon's reconnect, windowClosing that the window is closing,
	// and saveFailed that the last save failed, which was told.
	// programLimit holds back programs' own notifications to one every 5
	// seconds a session.
	programLimit notice.Limiter
	// settingsSaveFailed tells that saving settings failed, which was told.
	settingsSaveFailed bool
	savedIDs           map[string]bool
	saveDeferred       bool
	windowClosing      bool
	saveFailed         bool
	// daemonIgnored tells that the user runs without a session daemon of
	// another version, which still runs its sessions: agents aren't
	// resumed here, which would run each conversation twice.
	daemonIgnored bool
	// postWin is a.win for post, set once the window is made.
	postWin atomic.Pointer[mygo.Window]

	pendingRemoveProjectID   string
	pendingRemoveProjectName string
	removeProjectConfirmOpen *bool

	pendingKillPid  int
	pendingKillName string
	killConfirmOpen *bool

	// webOn tells web access is on, webNetwork that it listens on the
	// network; webPair is the pairing dialog's content while webPairOpen
	// shows it.
	webOn       bool
	webNetwork  bool
	webPair     *webPairing
	webPairOpen *bool
}

type fileNode struct {
	Name     string
	Path     string
	Dir      bool
	Open     bool
	Status   string
	Children []*fileNode
	Action   string
	// Muted is what git ignores, shown dimmed as VS Code does.
	Muted bool
	// Loaded tells a folder's children were read: a muted one's are read
	// as it opens, since it may be node_modules.
	Loaded bool
}

type project struct {
	// ID names the project in state.json and to the CLI.
	ID        string
	Collapsed bool
	Name      string
	Path      string // absolute path; expanded from ~ at load
	Branch    string
	Active    bool
	Tabs      []string
	// ActiveSession is the session to show first, as saved.
	ActiveSession string
}

func newApp() *appState {
	a := &appState{
		sidebarVisible:   true,
		inspectorVisible: true,
		sidebarShown:     true,
		inspectorShown:   true,
		sidebarW:         240,
		inspectorW:       330,
		activePaneTermID: "",
		searchSplit:      35,
		diffSplit:        true,
		commitMsg:        "",
		graphLimit:       300,
		editors:          map[string]*openFile{},
		panes:            session.NewLocalHost(),
		paneFailed:       map[string]bool{},
		wtEnv:            true,
		mergeSquash:      true,
		paneShown:        map[string]time.Time{},
		hookChecked:      map[string]bool{},
		git:              newGitWatch(),
		arrived:          map[string]bool{},
		notified:         map[string]time.Time{},
		ws:               map[string]*projectWS{},
		saved:            map[string][]model.Session{},
		paneDirs:         map[string]string{},
		paneAgents:       map[string]*agent.Session{},
		paneURLs:         map[string]string{},
		restoring:        map[string]bool{},
		paneTitles:       map[string]string{},
		webs:             map[string]*webEntry{},
		status:           map[string]session.Info{},
		commitFiles:      map[string][]service.CommitFile{},
		commitStats:      map[string][2]int{},
		projects:         []project{},
		settings:         loadSettings(),
	}
	a.hookPanes()
	if cfg, err := webStore().Config(); err == nil {
		a.webOn, a.webNetwork = cfg.Enabled, cfg.OnNetwork()
	}
	a.fontSize, a.diffSplit = a.settings.Terminal.FontSize, a.settings.Editor.DiffSplit
	a.termMade = a.termBehavior()
	a.shareSettings()
	proto.SetSearchEngine(a.settings.SearchEngine)
	a.termFont = a.terminalFontFamily()
	a.loadUIPrefs()
	a.themeMode = a.settings.Theme
	a.applyTheme(themeSource(a.themeMode))
	a.panesRef.Store(&hostRef{a.panes})
	// Load saved projects and their sessions. With none saved, it starts
	// empty, and the sidebar offers Open Project.
	if active := a.loadState(); len(a.projects) > 0 {
		a.active = -1
		a.setProject(active)
	} else {
		a.clearWorkspace()
	}
	return a
}

func stateFilePath() string { return paths.StateFile() }

// legacyStateFilePath is where the app kept the project list while it was
// called oterm. Renaming the app must not lose it.
func legacyStateFilePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.ExpandEnv("$HOME/.config")
	}
	return filepath.Join(dir, "oterm", "state.json")
}

func (a *appState) project() *project {
	if a.active < 0 || a.active >= len(a.projects) {
		return nil
	}
	return &a.projects[a.active]
}

// activeRepoRef returns the active repo, or nil when none was discovered.
func (a *appState) activeRepoRef() *service.Repo {
	if a.activeRepo < 0 || a.activeRepo >= len(a.repos) {
		return nil
	}
	return &a.repos[a.activeRepo]
}

func (a *appState) gitCount() int {
	r := a.activeRepoRef()
	if r == nil {
		return 0
	}
	return len(r.Staged) + len(r.Changes)
}

func (a *appState) setProject(i int) {
	if i < 0 || i >= len(a.projects) {
		return
	}
	// The project shown keeps its sessions running: its tabs, panes and
	// editors wait in a.ws until it shows again.
	a.stashWorkspace()
	a.active = i
	p := &a.projects[i]
	if p.ID == "" {
		p.ID = newID()
	}
	a.workspace = p.Path
	a.restoreWorkspace(p)
	a.gitBusy = ""
	a.gitMsg = ""
	a.gitErr = false
	a.branches = nil
	a.branchesRepo = ""
	a.currentBranch = ""
	a.expandedCommit = ""

	// Reset per-project state.
	a.repos = nil
	a.activeRepo = 0
	a.repoFollow, a.repoMain, a.repoAsked = "", 0, ""
	a.repoErr = ""
	a.commits = nil
	a.graphLoaded = false
	a.commitFiles = map[string][]service.CommitFile{}
	a.commitStats = map[string][2]int{}
	a.fileIndex = nil
	a.fileTree = nil
	a.diffOld, a.diffNew, a.diffPatch = nil, nil, nil
	a.diffDs, a.diffKey = nil, ""
	// Nor the last project's diff: its header's Stage and Discard acted
	// on that project's file, in its repo.
	a.diffSrc, a.diffTitle, a.diffImage, a.diffBinary = diffSource{}, "", false, false
	a.closeOverlay()

	a.loadProjectData()
	a.saveSoon()
}

// start begins the window's work once it exists: the loads of the saved
// active project (newApp chose it, but had no window for the results to
// reach), and the timers of the status bar and the Info panel.
func (a *appState) start() {
	// What ui.json kept before it was a setting (the text size, the diff
	// layout) is moved to settings.json now, not when a setting next
	// changes: ui.json no longer keeps it.
	if !settingsFileHas("terminal") {
		a.saveSettings()
	}
	if settingsBroken != "" {
		a.queuedToasts = append(a.queuedToasts, "Your settings file couldn't be read, so the defaults are in use. Its copy: "+cli.ShortPath(settingsBroken))
	}
	if settingsWrongKey != "" {
		a.queuedToasts = append(a.queuedToasts, "Your settings file gives \""+settingsWrongKey+"\" a value it can't use: its default is in use")
	}
	a.startThemeWatch()
	a.startPanes()
	if a.project() != nil {
		a.loadProjectData()
	}
	a.startSystemStats()
	if a.inspectorTab == 0 && a.inspectorShown {
		a.startProcessRefresh()
	}
}

// loadProjectData starts the loads of the active project's repos, files
// and processes. setProject calls it; so does start, once the window
// exists, since loads begun before it had no window to report to.
func (a *appState) loadProjectData() {
	a.refreshRepos()
	a.rebuildFileIndex()
	a.rebuildFileTree()
	a.refreshProcesses()
}

// switchProject shows project i, as clicking it in the sidebar does, and
// saves the choice so the next launch opens it.
func (a *appState) switchProject(i int) {
	if i < 0 || i >= len(a.projects) || i == a.active {
		return
	}
	a.setProject(i)
}

func (a *appState) removeProject(i int) {
	if i < 0 || i >= len(a.projects) {
		return
	}
	// Its sessions end with it; other projects' keep running.
	removed := a.projects[i]
	a.closeProjectPanes(removed.ID)
	if i == a.active {
		a.wsOwner = ""
	}
	delete(a.saved, removed.ID)
	a.projects = append(a.projects[:i], a.projects[i+1:]...)
	if i < a.active {
		a.active--
	} else if i == a.active {
		a.active = -1
	}
	if len(a.projects) == 0 {
		a.clearWorkspace()
	} else {
		next := a.active
		if next < 0 {
			next = min(i, len(a.projects)-1)
			a.active = -1
		}
		if a.active < 0 {
			a.setProject(next)
		}
	}
	a.saveSoon()
}

// clearWorkspace shows no project, as when there are none.
func (a *appState) clearWorkspace() {
	a.active = -1
	a.workspace = ""
	a.tabs = nil
	a.activeTab = -1
	a.activeView = tabTerminal
	a.repos = nil
	a.processes = nil
	a.ports = nil
	a.processErr = ""
	a.processWarn = ""
	a.stopProcessRefresh()
}

// refreshRepos discovers repos and loads their status, off the UI thread.
func (a *appState) refreshRepos() {
	workspace := a.workspace
	win := a.win
	worktrees := a.worktreeDirs()
	a.repoJob.Run(func(ctx context.Context) error {
		repos := service.DiscoverRepos(workspace, 1)
		// The worktrees of its sessions, which the panel follows: none is
		// found in the project's folders, as they are hidden.
		for _, dir := range worktrees {
			if r, err := service.RepoStatus(dir, workspace); err == nil {
				repos = append(repos, *r)
			}
		}
		if win != nil && ctx.Err() == nil {
			win.Update(func() { a.applyRepos(workspace, repos) })
		}
		return nil
	}, nil)
}

// applyRepos shows the repos found in workspace, unless another project
// is shown meanwhile, or none (the last one removed): they are not its.
// Applied anyway, the Git panel listed the last project's changes, and
// its branch was written into the new one; with no project, it crashed.
func (a *appState) applyRepos(workspace string, repos []service.Repo) {
	if a.workspace != workspace {
		return
	}
	a.repos = repos
	if a.activeRepo >= len(a.repos) {
		a.activeRepo = 0
	}
	a.repoFollow = "" // a worktree's repo may be here now
	r, p := a.activeRepoRef(), a.project()
	if r == nil || p == nil {
		return
	}
	// The project's branch is its checkout's, not a worktree's.
	for i := 0; a.isWorktreeRoot(r.Root) && i < len(a.repos); i++ {
		if !a.isWorktreeRoot(a.repos[i].Root) {
			r = &a.repos[i]
		}
	}
	p.Branch = r.Branch
	if r.Ahead > 0 || r.Behind > 0 {
		p.Branch += branchDelta(r.Ahead, r.Behind)
	}
}

// refreshActiveRepo reloads status of the active repo only.
func (a *appState) refreshActiveRepo() {
	r := a.activeRepoRef()
	if r == nil || a.win == nil {
		return
	}
	root := r.Root
	win := a.win
	idx := a.activeRepo
	workspace := a.workspace
	a.repoJob.Run(func(ctx context.Context) error {
		fresh, err := service.RepoStatus(root, workspace)
		if err != nil || ctx.Err() != nil {
			return err
		}
		win.Update(func() {
			if a.workspace != workspace {
				return
			}
			if idx < len(a.repos) && a.repos[idx].Root == root {
				a.repos[idx] = *fresh
			}
		})
		return nil
	}, nil)
}

// refreshProcesses loads the project's processes and ports.
// listProcesses lists a project's processes, never nil once listed: the
// Info panel shows nil as still loading, and a folder too broad to list
// (the home folder) lists none.
func listProcesses(workspace string, roots []int) ([]service.Process, error) {
	procs, err := service.ListProjectProcesses(workspace, roots)
	if procs == nil {
		procs = []service.Process{}
	}
	return procs, err
}

// paneShells returns the PIDs of the shown project's pane shells.
func (a *appState) paneShells() []int {
	p := a.project()
	if p == nil {
		return nil
	}
	var pids []int
	for _, t := range a.projectTabs(p.ID) {
		if t.Kind != tabTerminal || t.Split == nil {
			continue
		}
		for _, id := range t.Split.termIDs() {
			if pid := a.status[id].PID; pid > 0 {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

func (a *appState) refreshProcesses() {
	workspace := a.workspace
	roots := a.paneShells()
	// In test mode (no window) run synchronously to avoid goroutine races.
	if a.win == nil {
		procs, err := listProcesses(workspace, roots)
		ports, _ := service.ListProjectPorts(procs)
		a.processes = procs
		a.ports = ports
		if err != nil {
			a.processErr = err.Error()
		} else {
			a.processErr = ""
		}
		a.processWarn = ""
		if service.IsWorkspaceTooBroad(workspace) {
			a.processWarn = "Workspace path is too broad; open a specific project folder"
		}
		return
	}
	win := a.win
	a.processJob.Run(func(ctx context.Context) error {
		procs, err := listProcesses(workspace, roots)
		ports, _ := service.ListProjectPorts(procs)
		if ctx.Err() != nil {
			return nil
		}
		win.Update(func() {
			if a.workspace != workspace {
				return // another project's processes
			}
			a.processes = procs
			a.ports = ports
			if err != nil {
				a.processErr = err.Error()
			} else {
				a.processErr = ""
			}
			if service.IsWorkspaceTooBroad(workspace) {
				a.processWarn = "Workspace path is too broad; open a specific project folder"
			} else {
				a.processWarn = ""
			}
		})
		return err
	}, nil)
}

// startSystemStats samples CPU and memory now, then every 3s while the
// window lives. The first sample only primes the CPU delta.
func (a *appState) startSystemStats() {
	a.stopSystemStats()
	if a.win == nil {
		return // no invented numbers in tests
	}
	a.sysStats = a.sysSampler.Sample()
	a.scheduleSystemStats()
}

// scheduleSystemStats samples again 3s from now. It does not sample on the
// way in: the CPU delta is between consecutive samples, and a second sample
// right after the first would measure no elapsed time.
func (a *appState) scheduleSystemStats() {
	gen := a.sysGen
	a.sysTimer = time.AfterFunc(3*time.Second, func() {
		a.win.Update(func() { a.sysTick(gen) })
	})
}

// sysTick samples and schedules the next tick, unless the stats were
// stopped after the timer fired but before this ran: they stay stopped,
// not sampling on after the window closed. It says whether it went on.
func (a *appState) sysTick(gen int) bool {
	if a.sysGen != gen {
		return false
	}
	a.sysStats = a.sysSampler.Sample()
	a.scheduleSystemStats()
	return true
}

// stopSystemStats halts the periodic machine-load sampling.
func (a *appState) stopSystemStats() {
	a.sysGen++
	if a.sysTimer != nil {
		a.sysTimer.Stop()
		a.sysTimer = nil
	}
}

// startProcessRefresh begins periodic process/port refresh (5s) while the
// info panel is visible.
func (a *appState) startProcessRefresh() {
	a.stopProcessRefresh()
	a.refreshProcesses()
	if a.win == nil {
		return // no timer in test mode
	}
	gen := a.processGen
	a.processTimer = time.AfterFunc(5*time.Second, func() {
		a.win.Update(func() { a.processTick(gen) })
	})
}

// processTick refreshes again, unless the refresh was stopped meanwhile
// (the Info tab hidden): it stays stopped. It says whether it went on.
func (a *appState) processTick(gen int) bool {
	if a.processGen != gen {
		return false
	}
	a.startProcessRefresh()
	return true
}

// stopProcessRefresh halts the periodic refresh.
func (a *appState) stopProcessRefresh() {
	a.processGen++
	if a.processTimer != nil {
		a.processTimer.Stop()
		a.processTimer = nil
	}
}

// rebuildFileIndex refreshes the quick-open file index.
func (a *appState) rebuildFileIndex() {
	workspace := a.workspace
	win := a.win
	if workspace == "" || win == nil {
		return
	}
	a.indexJob.Run(func(ctx context.Context) error {
		files := service.IndexFiles(workspace, 200000)
		if ctx.Err() != nil {
			return nil
		}
		win.Update(func() { a.applyFileIndex(workspace, files) })
		return nil
	}, nil)
}

// applyFileIndex keeps the files of workspace for quick open, unless
// another project is shown meanwhile: its paths resolved under the new
// project's folder, and opening one did nothing.
func (a *appState) applyFileIndex(workspace string, files []string) {
	if a.workspace == workspace {
		a.fileIndex = files
	}
}

func branchDelta(ahead, behind int) string {
	s := ""
	if ahead > 0 {
		s += " ↑" + itoa(ahead)
	}
	if behind > 0 {
		s += " ↓" + itoa(behind)
	}
	return s
}

// rebuildFileTree builds the inspector file tree from the real workspace.
func (a *appState) rebuildFileTree() {
	p := a.project()
	rootName := "workspace"
	if p != nil {
		rootName = p.Name
	}
	// Folders open as they were.
	open := map[string]bool{}
	var walk func(n *fileNode)
	walk = func(n *fileNode) {
		if n.Dir {
			open[n.Path] = n.Open
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	if a.fileTree != nil {
		walk(a.fileTree)
	}
	root := &fileNode{Name: rootName, Dir: true, Open: true}
	a.fileTree = root
	defer func() {
		var apply func(n *fileNode)
		apply = func(n *fileNode) {
			if o, ok := open[n.Path]; ok && n.Dir && n.Path != "" {
				n.Open = o
			}
			if n.Open && !n.Loaded {
				loadFileNode(n)
			}
			for _, c := range n.Children {
				apply(c)
			}
		}
		apply(root)
	}()
	if a.workspace == "" {
		return
	}
	root.Path = a.workspace
	root.Loaded = true
	fileScan{root: a.workspace, ignored: service.Ignored(a.workspace)}.children(root, -1)
}

// foldersFirst orders a folder's entries as file managers do: its
// folders, then its files, each by name.
func foldersFirst(entries []os.DirEntry) []os.DirEntry {
	slices.SortStableFunc(entries, func(x, y os.DirEntry) int {
		if x.IsDir() != y.IsDir() {
			if x.IsDir() {
				return -1
			}
			return 1
		}
		return strings.Compare(x.Name(), y.Name())
	})
	return entries
}

// fileScan reads the workspace for the file tree.
type fileScan struct {
	root    string
	ignored map[string]bool // what git ignores, as slash paths under root
}

func (s fileScan) node(parent, name string, isDir bool, depth int, muted bool) *fileNode {
	if depth > 3 {
		return nil
	}
	path := filepath.Join(parent, name)
	if isDir && (name == ".git" || service.Protected(path)) {
		return nil
	}
	if !muted {
		rel, _ := filepath.Rel(s.root, path)
		muted = name == "node_modules" || s.ignored[filepath.ToSlash(rel)]
	}
	n := &fileNode{Name: name, Path: path, Dir: isDir, Open: depth < 1 && !muted, Muted: muted}
	if !isDir {
		n.Action = "editor"
		return n
	}
	if !muted {
		n.Loaded = true
		s.children(n, depth)
	}
	return n
}

// children reads a folder's entries into it, its folders first.
func (s fileScan) children(n *fileNode, depth int) {
	entries, err := os.ReadDir(n.Path)
	if err != nil {
		return
	}
	for _, e := range foldersFirst(entries) {
		if ch := s.node(n.Path, e.Name(), e.IsDir(), depth+1, n.Muted); ch != nil {
			n.Children = append(n.Children, ch)
		}
	}
}

// loadFileNode reads a muted folder's children as it opens, one level:
// its own folders wait for theirs.
func loadFileNode(n *fileNode) {
	n.Loaded = true
	fileScan{}.children(n, 0)
}
