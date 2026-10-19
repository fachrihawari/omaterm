package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"oterm/service"
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
	Kind   topTabKind
	Label  string
	Detail string
	Dot    int
	Icon   int
	Dirty  bool
	Path   string // editor tabs: workspace-relative file path
	// Split is the terminal pane tree (nil or single leaf = one pane).
	Split *splitNode
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

	commitMsg string

	diffSplit bool

	paneFocus int

	fileSearch string
	fileTree   *fileNode

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
	diffKey   string            // content key the persisted DiffState was built from
	diffGen   int               // bumped every time new diff content is loaded
	diffSrc   diffSource
	diffTitle string

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
	expandedCommit string
	graphJob       service.Job
	graphLimit     int
	// Git panel collapsible sections
	stagedCollapsed  bool
	changesCollapsed bool
	stashCollapsed   bool
	gitGraphOpen     bool // GIT GRAPH section expanded

	// Terminal panes (Ghostty VT via MyGo's plugin)
	termPanes   map[string]*terminal.Terminal
	termPanesMu sync.Mutex

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
	Name   string
	Path   string // absolute path; expanded from ~ at load
	Branch string
	Active bool
	Tabs   []string
}

func newApp() *appState {
	a := &appState{
		sidebarVisible:   true,
		inspectorVisible: true,
		sidebarW:         210,
		inspectorW:       330,
		activePaneTermID: "",
		searchSplit:      35,
		diffSplit:        true,
		commitMsg:        "",
		graphLimit:       300,
		editors:          map[string]*openFile{},
		termPanes:        map[string]*terminal.Terminal{},
		projects:         []project{},
	}
	// Load saved projects.
	if saved, active := loadSavedProjects(); len(saved) > 0 {
		a.projects = saved
		a.active = active
		a.setProject(a.active)
	} else {
		// No saved projects: use home directory as default.
		home, _ := os.UserHomeDir()
		if home == "" {
			home = "/"
		}
		a.projects = []project{{Name: filepath.Base(home), Path: home, Tabs: []string{"dev"}}}
		a.active = 0
		a.setProject(0)
	}
	return a
}

// savedState is the on-disk project list.
type savedState struct {
	Projects []project `json:"projects"`
	Active   int       `json:"active"`
}

func stateFilePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.ExpandEnv("$HOME/.config")
	}
	return filepath.Join(dir, "oterm", "state.json")
}

func loadSavedProjects() ([]project, int) {
	data, err := os.ReadFile(stateFilePath())
	if err != nil {
		return nil, 0
	}
	var st savedState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, 0
	}
	// Validate paths exist.
	var valid []project
	for _, p := range st.Projects {
		if p.Path == "" {
			continue
		}
		abs, err := filepath.Abs(p.Path)
		if err != nil {
			continue
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			continue
		}
		// Reject overly broad paths that would match all processes.
		if service.IsWorkspaceTooBroad(abs) {
			continue
		}
		p.Path = abs
		if len(p.Tabs) == 0 {
			p.Tabs = []string{"dev"}
		}
		valid = append(valid, p)
	}
	active := 0
	if st.Active >= 0 && st.Active < len(valid) {
		active = st.Active
	}
	return valid, active
}

func (a *appState) saveProjects() {
	if a.win == nil {
		return // don't persist in test mode
	}
	if len(a.projects) == 0 {
		// Remove state file if no projects.
		os.Remove(stateFilePath())
		return
	}
	// Resolve all paths to absolute before saving.
	projects := make([]project, len(a.projects))
	copy(projects, a.projects)
	for i := range projects {
		if abs, err := filepath.Abs(projects[i].Path); err == nil {
			projects[i].Path = abs
		}
	}
	st := savedState{Projects: projects, Active: a.active}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	path := stateFilePath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	os.Rename(tmp, path)
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
	a.active = i
	p := &a.projects[i]
	a.workspace = p.Path

	// Stop and clean up all terminal panes before switching.
	a.termPanesMu.Lock()
	for _, tp := range a.termPanes {
		if tp != nil {
			tp.Close()
		}
	}
	a.termPanes = map[string]*terminal.Terminal{}
	a.termPanesMu.Unlock()

	a.tabs = nil
	for _, name := range p.Tabs {
		t := topTab{Kind: tabTerminal, Label: name, Dot: dotGreen,
			Split: &splitNode{TermID: randomTermID()}}
		if name == "tests" {
			t.Dot = dotYellow
		}
		a.tabs = append(a.tabs, t)
	}
	a.activeTab = 0
	a.activeView = tabTerminal
	if len(a.tabs) > 0 && a.tabs[0].Split != nil {
		ids := a.tabs[0].Split.termIDs()
		if len(ids) > 0 {
			a.activePaneTermID = ids[0]
		}
	}
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
	a.fileIndex = nil
	a.editors = map[string]*openFile{}
	a.fileTree = nil
	a.diffOld, a.diffNew, a.diffPatch = nil, nil, nil
	a.diffDs, a.diffKey = nil, ""
	a.closeOverlay()

	a.refreshRepos()
	a.rebuildFileIndex()
	a.rebuildFileTree()
	a.refreshProcesses()
	a.saveProjects()
}

func (a *appState) removeProject(i int) {
	if i < 0 || i >= len(a.projects) {
		return
	}
	// Stop terminal panes if removing the active project.
	if i == a.active {
		a.termPanesMu.Lock()
		for _, tp := range a.termPanes {
			if tp != nil {
				tp.Close()
			}
		}
		a.termPanes = map[string]*terminal.Terminal{}
		a.termPanesMu.Unlock()
	}
	name := a.projects[i].Name
	a.projects = append(a.projects[:i], a.projects[i+1:]...)
	if len(a.projects) == 0 {
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
	} else {
		if a.active >= len(a.projects) {
			a.active = len(a.projects) - 1
		}
		if a.active < 0 {
			a.active = 0
		}
		a.setProject(a.active)
	}
	a.saveProjects()
	_ = name
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
func (a *appState) refreshProcesses() {
	workspace := a.workspace
	// In test mode (no window) run synchronously to avoid goroutine races.
	if a.win == nil {
		procs, err := service.ListProjectProcesses(workspace)
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
		procs, err := service.ListProjectProcesses(workspace)
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
