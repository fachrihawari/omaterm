package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"oterm/service"
)

// App state: projects, tabs, git data, overlays.

type process struct {
	Name string
	Pid  int
	CPU  string
	Mem  string
}

type port struct {
	Port int
	Name string
	Open bool
}

// termLine is one line of a terminal pane: spans of colored text.
type termLine struct {
	Top   float32
	Spans []span
}

type span struct {
	Text  string
	Color int
}

type paneData struct {
	Title  string
	Shell  string
	Pid    int
	Dim    bool
	Cursor bool
	Lines  []termLine
}

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
	Path  string // workspace-relative
	State *editor.State
}

type appState struct {
	win *mygo.Window

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

	processes []process
	ports     []port

	commitMsg string

	diffSplit bool

	panes     [3]paneData
	paneFocus int

	fileSearch string
	fileTree   *fileNode

	// --- Real workspace/git state ---

	workspace string // absolute path of the active project

	repos      []service.Repo
	activeRepo int
	repoJob    service.Job
	repoErr    string

	// Git remote operations (fetch/pull/push)
	gitJob  service.Job
	gitBusy string // verb shown while a remote op runs, "" when idle
	gitMsg  string // result of the last remote op
	gitErr  bool   // whether gitMsg describes a failure

	// --- Editor documents ---

	editors   map[string]*openFile // workspace-relative path -> open document
	diffOld   *editor.Buffer
	diffNew   *editor.Buffer
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

	// Git stash
	stashMsg              string
	stashIncludeUntracked bool

	// Git graph
	commits    []service.Commit
	graphJob   service.Job
	graphLimit int
	// Git panel collapsible sections
	stagedCollapsed  bool
	changesCollapsed bool
	stashCollapsed   bool
	gitGraphOpen     bool // GIT GRAPH section expanded

	// Terminal panes (Ghostty VT via MyGo's plugin)
	termPanes map[string]*terminal.Terminal

	// Close tab confirmation
	pendingCloseTab     int
	closeTabConfirmOpen *bool
}

type fileNode struct {
	Name     string
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
	home, _ := os.UserHomeDir()
	expand := func(p string) string {
		if len(p) > 1 && p[:2] == "~/" {
			return filepath.Join(home, p[2:])
		}
		return p
	}
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
		projects: []project{
			{Name: "oterm", Path: expand("~/Projects/personal/oterm"), Active: true, Tabs: []string{"dev", "api", "tests"}},
		},
		processes: []process{
			{Name: "bun", Pid: 36587, CPU: "0%", Mem: "6 MB"},
			{Name: "node", Pid: 36588, CPU: "0%", Mem: "427 MB"},
		},
		ports: []port{
			{Port: 3000, Name: "web", Open: true},
			{Port: 4000, Name: "api", Open: true},
		},
	}
	a.setProject(0)
	a.panes = mockPanes()
	a.paneFocus = 0
	return a
}

func (a *appState) project() *project { return &a.projects[a.active] }

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
	a.active = i
	p := &a.projects[i]
	a.workspace = p.Path

	// Stop and clean up all terminal panes before switching.
	for _, tp := range a.termPanes {
		if tp != nil {
			tp.Close()
		}
	}
	a.termPanes = map[string]*terminal.Terminal{}

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
	a.gitBusy = ""
	a.gitMsg = ""
	a.gitErr = false

	// Reset per-project state.
	a.repos = nil
	a.activeRepo = 0
	a.repoErr = ""
	a.commits = nil
	a.fileIndex = nil
	a.editors = map[string]*openFile{}
	a.fileTree = nil
	a.diffOld, a.diffNew = nil, nil
	a.closeOverlay()

	a.refreshRepos()
	a.rebuildFileIndex()
	a.rebuildFileTree()
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
	root := &fileNode{Name: a.project().Name, Dir: true, Open: true}
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
	if isDir {
		switch name {
		case ".git", "node_modules", ".mygo", "build", "dist", ".cache":
			return nil
		}
		if len(name) > 0 && name[0] == '.' {
			return nil
		}
	}
	n := &fileNode{Name: name, Dir: isDir, Open: depth < 1}
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
