package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"omaterm/internal/agent"
	"omaterm/internal/ctl"
	"omaterm/internal/model"
	"omaterm/internal/paths"
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
	Size    int64
	Image   *imageSide // a picture: shown in the image viewer
}

type appState struct {
	win          *mygo.Window
	processTimer *time.Timer

	projects  []project
	active    int
	tabs      []topTab
	activeTab int

	activeView topTabKind

	sidebarVisible   bool
	inspectorVisible bool
	sidebarW         float32
	inspectorW       float32
	activePaneTermID string // TermID of the focused terminal pane

	inspectorTab int // 0 info, 1 files, 2 git

	processes []service.Process
	ports     []service.Port

	// Machine load shown in the status bar.
	sysStats   service.SystemStats
	sysSampler service.SystemSampler
	sysTimer   *time.Timer

	commitMsg string

	diffSplit bool

	fileTree *fileNode

	// --- Real workspace/git state ---

	workspace string // absolute path of the active project

	repos      []service.Repo
	activeRepo int
	repoJob    service.Job
	repoErr    string

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
	stashMsg              string
	stashIncludeUntracked bool

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
	paneFailed map[string]bool
	status     map[string]session.Info

	// settings are the user's preferences (settings.json); themeMode is
	// settings.Theme as shown, themeStamp the Omarchy theme.name mtime it
	// last loaded, themeWatchStop ends the theme watch.
	settings       Settings
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
	pendingCloseTab     int
	closeTabConfirmOpen *bool

	pendingRemoveProjectIdx  int
	pendingRemoveProjectName string
	removeProjectConfirmOpen *bool

	pendingKillPid  int
	pendingKillName string
	killConfirmOpen *bool
}

type fileNode struct {
	Name     string
	Path     string
	Dir      bool
	Open     bool
	Status   string
	Children []*fileNode
	Action   string
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
}

func newApp() *appState {
	a := &appState{
		sidebarVisible:   true,
		inspectorVisible: true,
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
	a.termFont = termFontFamily()
	a.loadUIPrefs()
	a.themeMode = a.settings.Theme
	a.applyTheme(themeSource(a.themeMode))
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
	a.repoErr = ""
	a.commits = nil
	a.graphLoaded = false
	a.commitFiles = map[string][]service.CommitFile{}
	a.commitStats = map[string][2]int{}
	a.fileIndex = nil
	a.fileTree = nil
	a.diffOld, a.diffNew, a.diffPatch = nil, nil, nil
	a.diffDs, a.diffKey = nil, ""
	a.closeOverlay()

	a.loadProjectData()
	a.saveSoon()
}

// start begins the window's work once it exists: the loads of the saved
// active project (newApp chose it, but had no window for the results to
// reach), and the timers of the status bar and the Info panel.
func (a *appState) start() {
	a.startThemeWatch()
	a.startPanes()
	if a.project() != nil {
		a.loadProjectData()
	}
	a.startSystemStats()
	if a.inspectorTab == 0 && a.inspectorVisible {
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
	a.repoJob.Run(func(ctx context.Context) error {
		repos := service.DiscoverRepos(workspace, 1)
		if win != nil {
			r := repos
			win.Update(func() {
				a.repos = r
				if a.activeRepo >= len(a.repos) {
					a.activeRepo = 0
				}
				if r := a.activeRepoRef(); r != nil {
					p := a.project()
					p.Branch = r.Branch
					if r.Ahead > 0 || r.Behind > 0 {
						p.Branch += branchDelta(r.Ahead, r.Behind)
					}
				}
			})
		}
		return nil
	}, nil)
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
	a.repoJob.Run(func(ctx context.Context) error {
		fresh, err := service.RepoStatus(root, a.workspace)
		if err != nil {
			return err
		}
		win.Update(func() {
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
func listProcesses(workspace string) ([]service.Process, error) {
	procs, err := service.ListProjectProcesses(workspace)
	if procs == nil {
		procs = []service.Process{}
	}
	return procs, err
}

func (a *appState) refreshProcesses() {
	workspace := a.workspace
	// In test mode (no window) run synchronously to avoid goroutine races.
	if a.win == nil {
		procs, err := listProcesses(workspace)
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
		procs, err := listProcesses(workspace)
		ports, _ := service.ListProjectPorts(procs)
		win.Update(func() {
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
	a.sysTimer = time.AfterFunc(3*time.Second, func() {
		a.win.Update(func() {
			a.sysStats = a.sysSampler.Sample()
			a.scheduleSystemStats()
		})
	})
}

// stopSystemStats halts the periodic machine-load sampling.
func (a *appState) stopSystemStats() {
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
	a.processTimer = time.AfterFunc(5*time.Second, func() {
		a.win.Update(func() { a.startProcessRefresh() })
	})
}

// stopProcessRefresh halts the periodic refresh.
func (a *appState) stopProcessRefresh() {
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
		win.Update(func() { a.fileIndex = files })
		return nil
	}, nil)
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
	root := &fileNode{Name: rootName, Dir: true, Open: true}
	a.fileTree = root
	if a.workspace == "" {
		return
	}
	entries, err := os.ReadDir(a.workspace)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := buildFileNode(a.workspace, e.Name(), e.IsDir(), 0)
		if n != nil {
			root.Children = append(root.Children, n)
		}
	}
}

func buildFileNode(parent, name string, isDir bool, depth int) *fileNode {
	if depth > 3 {
		return nil
	}
	// Skip hidden files/dirs (e.g. .DS_Store, .bashrc).
	if len(name) > 0 && name[0] == '.' {
		return nil
	}
	if isDir {
		if service.Protected(filepath.Join(parent, name)) {
			return nil
		}
		switch name {
		case "node_modules", ".mygo", "build", "dist", ".cache":
			return nil
		}
	}
	n := &fileNode{Name: name, Path: filepath.Join(parent, name), Dir: isDir, Open: depth < 1}
	if !isDir {
		n.Action = "editor"
		return n
	}
	entries, err := os.ReadDir(filepath.Join(parent, name))
	if err != nil {
		return n
	}
	for _, e := range entries {
		if ch := buildFileNode(filepath.Join(parent, name), e.Name(), e.IsDir(), depth+1); ch != nil {
			n.Children = append(n.Children, ch)
		}
	}
	return n
}
