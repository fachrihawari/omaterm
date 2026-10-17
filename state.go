package main

// The mock's hardcoded data (omaterm_mock_ui_v5.html). All of this stands
// in for the real project/terminal/git services of a later phase.

type project struct {
	Name   string
	Path   string
	Branch string
	Active bool // green dot in the sidebar
	Tabs   []string
}

type process struct {
	Name string
	Pid  int
	CPU  string
	Mem  string
}

type port struct {
	Port int
	Name string
	Open bool // has an "open in browser" button
}

type gitFile struct {
	Name   string
	Dir    string
	Status string // M, A, U (mock renders untracked "?" as U)
}

// termLine is one line of a terminal pane: spans of colored text.
type termLine struct {
	Spans []span
}

type span struct {
	Text  string
	Color int // index into termColors, -1 for default
}

type paneData struct {
	Title string
	Shell string
	Pid   int
	Dim   bool // pane 3 renders its output dimmer
	Lines []termLine
}

type layoutKind int

const (
	layoutMixed layoutKind = iota
	layoutSingle
	layoutVertical
	layoutHorizontal
)

type topTabKind int

const (
	tabTerminal topTabKind = iota
	tabEditor
	tabDiff
)

type topTab struct {
	Kind   topTabKind
	Label  string // terminal tabs: session name
	Detail string // terminal tabs: "3 panes"
	Dot    int    // status color index, -1 for none
	Icon   int    // file icon for editor/diff tabs, -1 for none
	Dirty  bool   // "M" marker
}

const (
	dotGreen = iota
	dotYellow
	dotGray
)

const (
	fileIconTS = iota
	fileIconDiff
	fileIconNone = -1
)

type appState struct {
	projects  []project
	active    int // index into projects
	tabs      []topTab
	activeTab int

	activeView topTabKind // the view the active tab shows

	sidebarVisible   bool
	inspectorVisible bool
	sidebarPct       float32 // percent of the window width the sidebar takes
	inspectorPct     float32 // percent the inspector takes
	splitVPct        float32 // percent for the terminal vertical split
	splitHPct        float32 // percent for the terminal horizontal split
	diffSplitPct     float32 // percent for the diff columns

	inspectorTab int // 0 info, 1 files, 2 git

	layout layoutKind

	processes []process
	ports     []port

	staged    []gitFile
	changes   []gitFile
	commitMsg string

	diffStaged bool // diff view shows HEAD↔INDEX
	diffSplit  bool // split vs inline diff

	panes [3]paneData

	fileSearch string
	fileTree   *fileNode
}

type fileNode struct {
	Name     string
	Dir      bool
	Open     bool
	Status   string // git status letter, "" if clean
	Children []*fileNode
	// Open action: "" none, "editor", "diff"
	Action string
}

func newApp() *appState {
	a := &appState{
		sidebarVisible:   true,
		inspectorVisible: true,
		sidebarPct:       14.6,
		inspectorPct:     27,
		splitVPct:        50,
		splitHPct:        50,
		diffSplitPct:     50,
		layout:           layoutMixed,
		diffSplit:        true,
		commitMsg:        "feat: improve split terminal project",
		projects: []project{
			{Name: "omaterm", Path: "~/dev/omaterm", Branch: "main*", Active: true, Tabs: []string{"dev", "api", "tests"}},
			{Name: "phoenix", Path: "~/dev/phoenix", Branch: "main", Active: true, Tabs: []string{"dev", "worker", "db"}},
			{Name: "dolphin", Path: "~/dev/dolphin", Branch: "feat/booking", Active: true, Tabs: []string{"dev", "booking", "jobs"}},
			{Name: "mindwave", Path: "~/dev/mindwave", Branch: "main", Tabs: []string{"dev", "checkout"}},
			{Name: "fortis", Path: "~/dev/fortis", Branch: "develop", Active: true, Tabs: []string{"frontend", "api", "workers"}},
		},
		processes: []process{
			{Name: "bun", Pid: 36587, CPU: "0%", Mem: "6 MB"},
			{Name: "node", Pid: 36588, CPU: "0%", Mem: "427 MB"},
			{Name: "worker", Pid: 36596, CPU: "0%", Mem: "75 MB"},
		},
		ports: []port{
			{Port: 3000, Name: "web", Open: true},
			{Port: 4000, Name: "api", Open: true},
			{Port: 9229, Name: "debug"},
			{Port: 61922, Name: "worker"},
		},
		staged: []gitFile{
			{Name: "App.tsx", Dir: "src/components", Status: "M"},
			{Name: "GitPanel.tsx", Dir: "src/components", Status: "A"},
		},
		changes: []gitFile{
			{Name: "FileTree.tsx", Dir: "src/components", Status: "M"},
			{Name: "TerminalProject.tsx", Dir: "src/components", Status: "M"},
			{Name: "terminal-layout.ts", Dir: "src/lib", Status: "U"},
		},
	}
	a.setProject(0)
	a.panes = mockPanes()
	a.fileTree = mockFiles()
	return a
}

func (a *appState) project() *project { return &a.projects[a.active] }

func (a *appState) setProject(i int) {
	a.active = i
	p := &a.projects[i]
	a.tabs = nil
	for j, name := range p.Tabs {
		t := topTab{Kind: tabTerminal, Label: name, Dot: dotGreen}
		if j == 0 && len(p.Tabs) == 3 {
			t.Detail = "3 panes"
		}
		if name == "tests" {
			t.Dot = dotYellow
		}
		a.tabs = append(a.tabs, t)
	}
	if p.Name == "omaterm" {
		a.tabs = append(a.tabs,
			topTab{Kind: tabEditor, Label: "App.tsx", Icon: fileIconTS, Dirty: true},
			topTab{Kind: tabDiff, Label: "Diff · GitPanel.tsx", Icon: fileIconDiff},
		)
	}
	a.activeTab = 0
	a.activeView = tabTerminal
}

func (a *appState) gitCount() int { return len(a.staged) + len(a.changes) }

func mockFiles() *fileNode {
	return &fileNode{Dir: true, Open: true, Children: []*fileNode{
		{Name: "src", Dir: true, Open: true, Children: []*fileNode{
			{Name: "components", Dir: true, Open: true, Children: []*fileNode{
				{Name: "App.tsx", Status: "M", Action: "editor"},
				{Name: "FileTree.tsx", Status: "M", Action: "editor"},
				{Name: "GitPanel.tsx", Status: "U", Action: "diff"},
				{Name: "TerminalProject.tsx", Action: "editor"},
			}},
			{Name: "lib", Dir: true, Children: []*fileNode{
				{Name: "terminal-layout.ts", Action: "editor"},
			}},
		}},
		{Name: "package.json", Action: "editor"},
		{Name: "README.md", Action: "editor"},
	}}
}
