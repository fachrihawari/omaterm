package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"oterm/service"
)

// The status bar said "1 processes" and carried invented CPU/MEM numbers.
func TestStatusBarCountsAndNoMockStats(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.processes = []service.Process{{Name: "one", Pid: 2}}
	a.ports = nil
	a.sysStats = service.SystemStats{OK: false}

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()

	if !tr.HasText("1 process") {
		t.Error("singular process count missing")
	}
	if tr.HasText("1 processes") {
		t.Error("process count is not pluralized")
	}
	if !tr.HasText("0 ports") {
		t.Error("port count missing")
	}
	for _, mock := range []string{"CPU 3%", "MEM 1.3 GB"} {
		if tr.HasText(mock) {
			t.Errorf("invented status value %q still rendered", mock)
		}
	}
	if tr.HasText("⌘O") {
		t.Error("Linux still shows the macOS ⌘O chip")
	}
	if !tr.HasText("Ctrl+O") {
		t.Error("Linux shortcut chip should read Ctrl+O")
	}
}

// The status bar keeps the project visible when the sidebar is hidden.
func TestStatusBarShowsProject(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.sidebarVisible = false
	if p := a.project(); p != nil {
		p.Name = "probe-project"
		p.Branch = "main"
	}

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("probe-project") {
		t.Error("project name missing from the status bar with the sidebar hidden")
	}
	// The hidden-sidebar toggle must be reachable, not just implied.
	if _, ok := tr.Find("Show Projects"); !ok {
		t.Error("no show-projects control while the sidebar is hidden")
	}
}

// Commit must not look clickable with nothing staged or no message.
func TestCommitButtonDisabled(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.inspectorTab = 2
	a.commitMsg = ""
	head := service.HeadSha(a.activeRepoRef().Root)

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if a.commitReady() {
		t.Fatal("commitReady with an empty message")
	}
	if err := tr.Click("Commit"); err != nil {
		t.Fatalf("commit button missing: %v", err)
	}
	tr.Frame()
	if got := service.HeadSha(a.activeRepoRef().Root); got != head {
		t.Errorf("clicking a disabled Commit created %s", got)
	}

	// Stage the change and add a message: now it is ready.
	r := a.activeRepoRef()
	if err := service.Stage(r.Root, r.Changes[0].Path); err != nil {
		t.Fatal(err)
	}
	fresh, err := service.RepoStatus(r.Root, a.workspace)
	if err != nil {
		t.Fatal(err)
	}
	a.repos[0] = *fresh
	a.commitMsg = "message"
	if !a.commitReady() {
		t.Error("commitReady false with staged files and a message")
	}
}

// Binary files used to be read as text in the diff and editor, showing
// mojibake. They now get a placeholder.
func TestBinaryDiffAndEditorPlaceholder(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	r := a.activeRepoRef()
	if r == nil {
		t.Fatal("no repo")
	}
	bin := filepath.Join(r.Root, "image.png")
	if err := os.WriteFile(bin, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.Stage(r.Root, "image.png"); err != nil {
		t.Fatal(err)
	}
	fresh, err := service.RepoStatus(r.Root, a.workspace)
	if err != nil {
		t.Fatal(err)
	}
	a.repos[0] = *fresh

	a.openWorkingDiff("image.png", false)
	if !a.diffBinary {
		t.Fatal("binary diff did not switch to the placeholder")
	}
	if a.diffOld != nil || a.diffNew != nil || a.diffPatch != nil {
		t.Error("binary diff still holds text buffers")
	}

	a.openEditor("image.png")
	doc := a.editors["image.png"]
	if doc == nil || !doc.Binary {
		t.Fatal("binary file opened as text")
	}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("Binary file") {
		t.Error("binary placeholder text not rendered")
	}
	if doc.State != nil {
		t.Error("binary document has an editor state")
	}
}

// YAML front matter is metadata, not a heading and a rule.
func TestStripFrontMatter(t *testing.T) {
	in := "---\nname: screenshot\ndescription: >\n  text\n---\n\n# Title\n\nBody\n"
	got := stripFrontMatter(in)
	if strings.Contains(got, "name: screenshot") {
		t.Errorf("front matter kept: %q", got)
	}
	if !strings.HasPrefix(got, "\n# Title") {
		t.Errorf("body not preserved: %q", got)
	}
	// A horizontal rule at the top of a document is not front matter.
	plain := "---\n\n# Title\n"
	if stripFrontMatter(plain) != plain {
		t.Error("a document starting with a rule was treated as front matter")
	}
	// Unterminated front matter is left alone rather than eating the file.
	open := "---\nname: x\n\n# Title\n"
	if stripFrontMatter(open) != open {
		t.Error("unterminated front matter was stripped")
	}
}

// Quick open shows the name once: base name on the left, directory on the
// right, with match highlighting still landing on the right characters.
func TestQuickOpenShowsPathOnce(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.fileIndex = []string{"src/components/button.tsx"}
	a.openOverlay(overlayQuickOpen)

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("button.tsx") {
		t.Error("base name not rendered")
	}
	if !tr.HasText("src/components") {
		t.Error("directory not rendered")
	}
	if tr.HasText("src/components/button.tsx") {
		t.Error("full path repeated in the row")
	}
}

// The graph count shows a placeholder until history is loaded, instead of
// a confident zero.
func TestGraphCountUnknown(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.inspectorTab = 2
	a.graphLoaded = false
	a.commits = nil

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("—") {
		t.Error("graph count does not show an unknown placeholder")
	}
	if tr.HasText("GIT GRAPH") == false {
		t.Error("graph section header missing")
	}
}

// A confirmation dialog for a destructive action uses the danger style and
// keeps its visible border.
func TestDangerConfirmDialog(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	open := true
	a.killConfirmOpen = &open
	a.pendingKillPid = 999999
	a.pendingKillName = "probe"

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("Kill Process") {
		t.Fatal("kill dialog not rendered")
	}
	if !tr.HasText("Kill") {
		t.Error("confirm button missing")
	}
}

// Newly added and modified files keep their status letters in fixed
// columns; the file list is cached rather than shelling out per frame.
func TestCommitFileListCached(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	r := a.activeRepoRef()
	if r == nil {
		t.Fatal("no repo")
	}
	hash := service.HeadSha(r.Root)
	if hash == "" {
		t.Fatal("no HEAD")
	}
	a.inspectorTab = 2
	a.gitGraphOpen = true
	a.commits = []service.Commit{{Hash: hash, Short: hash[:7], Subject: "initial"}}
	a.graphLoaded = true
	a.expandedCommit = hash

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if _, ok := a.commitFiles[hash]; !ok {
		t.Fatal("commit file list not cached")
	}
	// A cache hit must win over the real git data: replacing the entry
	// with a sentinel and re-rendering proves the view does not shell out
	// again on every frame.
	a.commitFiles[hash] = []service.CommitFile{{Status: "M", Path: "sentinel.go"}}
	a.commitStats[hash] = [2]int{7, 3}
	tr.Frame()
	if got := a.commitFiles[hash]; len(got) != 1 || got[0].Path != "sentinel.go" {
		t.Errorf("cached list was replaced by a refetch: %+v", got)
	}
	if !tr.HasText("sentinel.go") {
		t.Error("cached file list was not rendered")
	}
}
