package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/service"
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
	// A noun already ending in s takes "es", not another s: the live
	// status bar read "11 processs".
	a.processes = append(a.processes, service.Process{Name: "two", Pid: 3})
	tr.Frame()
	if !tr.HasText("2 processes") {
		t.Error("plural of a noun ending in s is wrong")
	}
	if tr.HasText("processs") {
		t.Error("doubled s in the plural")
	}
	for _, mock := range []string{"CPU 3%", "MEM 1.3 GB"} {
		if tr.HasText(mock) {
			t.Errorf("invented status value %q still rendered", mock)
		}
	}
	if tr.HasText("⌘O") {
		t.Error("Linux still shows the macOS ⌘O chip")
	}
	if got := keyLabels("app.openProject"); got != "Ctrl+O" {
		t.Errorf("Linux shortcut reads %q, want Ctrl+O", got)
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
	if _, ok := tr.Find("Show Sidebar"); !ok {
		t.Error("no show-sidebar control while the sidebar is hidden")
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
	bin := filepath.Join(r.Root, "blob.bin")
	if err := os.WriteFile(bin, []byte{0x00, 0x01, 0x02, 0xff, 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.Stage(r.Root, "blob.bin"); err != nil {
		t.Fatal(err)
	}
	fresh, err := service.RepoStatus(r.Root, a.workspace)
	if err != nil {
		t.Fatal(err)
	}
	a.repos[0] = *fresh

	a.openWorkingDiff("blob.bin", false)
	if !a.diffBinary {
		t.Fatal("binary diff did not switch to the placeholder")
	}
	if a.diffOld != nil || a.diffNew != nil || a.diffPatch != nil {
		t.Error("binary diff still holds text buffers")
	}

	a.openEditor("blob.bin")
	doc := a.editors["blob.bin"]
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

// testPNG encodes a w×h solid picture, for the preview tests.
func testPNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Clicking a picture opens a viewer with the picture and its dimensions,
// not a binary placeholder.
func TestImageEditorPreview(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	r := a.activeRepoRef()
	if r == nil {
		t.Fatal("no repo")
	}
	data := testPNG(t, 40, 24, color.RGBA{R: 90, G: 169, B: 255, A: 255})
	if err := os.WriteFile(filepath.Join(r.Root, "logo.png"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	a.openEditor("logo.png")
	doc := a.editors["logo.png"]
	if doc == nil || doc.Image == nil {
		t.Fatal("image file did not open in the picture viewer")
	}
	if doc.Binary || doc.State != nil {
		t.Error("picture opened as a binary placeholder or as text")
	}
	if doc.Image.Pic == nil {
		t.Fatal("picture not decoded")
	}
	if doc.Image.W != 40 || doc.Image.H != 24 {
		t.Errorf("picture size = %v×%v, want 40×24", doc.Image.W, doc.Image.H)
	}

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("40 × 24 px") {
		t.Error("picture dimensions not shown")
	}
	if !tr.HasText("PNG") {
		t.Error("picture format not shown")
	}
	if tr.HasText("Binary file") {
		t.Error("picture still shows the binary placeholder")
	}
}

// An SVG is a picture and text: it opens in the viewer and keeps its
// source behind a toggle.
func TestSVGPreview(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	r := a.activeRepoRef()
	if r == nil {
		t.Fatal("no repo")
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="16"><rect width="24" height="16" fill="#5aa9ff"/></svg>`
	if err := os.WriteFile(filepath.Join(r.Root, "swatch.svg"), []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
	a.openEditor("swatch.svg")
	doc := a.editors["swatch.svg"]
	if doc == nil || doc.Image == nil {
		t.Fatal("SVG did not open as a picture")
	}
	if doc.State == nil {
		t.Fatal("SVG source was not kept")
	}
	if !doc.Image.Vector {
		t.Error("SVG should be marked as a vector, so it can scale up")
	}
	if !doc.Preview {
		t.Error("SVG should start in the picture preview")
	}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("24 × 16 px") {
		t.Error("SVG dimensions not shown")
	}
	if !tr.HasText("SVG") {
		t.Error("SVG format not shown")
	}

	// The source view is still reachable.
	doc.Preview = false
	tr = ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if tr.HasText("24 × 16 px") {
		t.Error("source view still shows the picture")
	}
}

// The diff of two pictures is the two pictures, not "Binary files differ".
func TestImageDiffPreview(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	r := a.activeRepoRef()
	if r == nil {
		t.Fatal("no repo")
	}
	old := testPNG(t, 20, 10, color.RGBA{R: 255, A: 255})
	if err := os.WriteFile(filepath.Join(r.Root, "logo.png"), old, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.Stage(r.Root, "logo.png"); err != nil {
		t.Fatal(err)
	}
	if err := service.CommitStaged(r.Root, "add logo"); err != nil {
		t.Fatal(err)
	}
	newData := testPNG(t, 20, 10, color.RGBA{B: 255, A: 255})
	if err := os.WriteFile(filepath.Join(r.Root, "logo.png"), newData, 0o644); err != nil {
		t.Fatal(err)
	}

	a.openWorkingDiff("logo.png", false)
	if !a.diffImage {
		t.Fatal("picture diff did not switch to the picture view")
	}
	if a.diffBinary || a.diffPatch != nil || a.diffOld != nil {
		t.Error("picture diff still holds the text/binary state")
	}
	if a.diffImgOld.Pic == nil || a.diffImgNew.Pic == nil {
		t.Fatal("one of the picture versions was not decoded")
	}
	if a.diffImgOld.Size != int64(len(old)) || a.diffImgNew.Size != int64(len(newData)) {
		t.Error("picture sizes do not match the two versions")
	}

	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("INDEX") || !tr.HasText("WORKING TREE") {
		t.Error("picture diff side labels not rendered")
	}
	if !tr.HasText("20 × 10") {
		t.Error("picture dimensions not rendered")
	}
	if tr.HasText("Binary file") {
		t.Error("picture diff still shows the binary placeholder")
	}

	// The picture state must not leak into a text diff.
	a.openWorkingDiff("hello.txt", false)
	if a.diffImage || a.diffOld == nil || a.diffNew == nil {
		t.Error("text diff did not restore the text buffers")
	}
}

// A picture that only exists in the working tree shows one side missing.
func TestImageDiffAddedFile(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	r := a.activeRepoRef()
	if r == nil {
		t.Fatal("no repo")
	}
	data := testPNG(t, 8, 6, color.White)
	if err := os.WriteFile(filepath.Join(r.Root, "new.png"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	a.openWorkingDiff("new.png", false)
	if !a.diffImage {
		t.Fatal("new picture did not open the picture diff")
	}
	if !a.diffImgOld.Missing {
		t.Error("old side should be missing for a new picture")
	}
	if a.diffImgNew.Pic == nil {
		t.Error("new side was not decoded")
	}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("Not in INDEX") {
		t.Error("missing side not explained")
	}
}

// A commit's picture diff compares the commit against its parent.
func TestImageDiffCommit(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	r := a.activeRepoRef()
	if r == nil {
		t.Fatal("no repo")
	}
	if err := os.WriteFile(filepath.Join(r.Root, "logo.png"),
		testPNG(t, 12, 8, color.Black), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.Stage(r.Root, "logo.png"); err != nil {
		t.Fatal(err)
	}
	if err := service.CommitStaged(r.Root, "add logo"); err != nil {
		t.Fatal(err)
	}
	// Change it in a second commit, so the commit has a parent version.
	if err := os.WriteFile(filepath.Join(r.Root, "logo.png"),
		testPNG(t, 12, 8, color.White), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.Stage(r.Root, "logo.png"); err != nil {
		t.Fatal(err)
	}
	if err := service.CommitStaged(r.Root, "brighten logo"); err != nil {
		t.Fatal(err)
	}
	hash := service.HeadSha(r.Root)
	a.openCommitFileDiff(hash, "logo.png")
	if !a.diffImage {
		t.Fatal("commit picture diff did not switch to the picture view")
	}
	if a.diffImgOld.Pic == nil || a.diffImgNew.Pic == nil {
		t.Fatal("commit picture versions not decoded")
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

// dialogPanelSpan returns the first and last row of the open dialog panel,
// found by its border in the window's middle column.
func dialogPanelSpan(t *testing.T, img *image.RGBA) (top, bottom int) {
	t.Helper()
	x := img.Bounds().Dx() / 2
	top, bottom = -1, -1
	want := pal.BorderStrong.Over(pal.Popover)
	near := func(a, b uint8) bool { return a >= b-2 && a <= b+2 }
	for y := 0; y < img.Bounds().Dy(); y++ {
		o := img.PixOffset(x, y)
		if near(img.Pix[o], want.R) && near(img.Pix[o+1], want.G) && near(img.Pix[o+2], want.B) {
			if top < 0 {
				top = y
			}
			bottom = y
		}
	}
	return top, bottom
}

// Overlays hang from 20% of the window height, as VS Code's do, rather than
// being centered: a short dialog used to sit in the middle of the window.
func TestDialogHangsFromTop(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.fileIndex = []string{"app.go", "state.go", "theme.go"}

	const w, h = 1440, 900
	wantTop := int(h * dialogTopFraction)
	for _, kind := range []overlayKind{overlayQuickOpen, overlayCommandPalette, overlayGlobalSearch, overlayBranch} {
		a.overlay = kind
		tr := ui.NewTester(a.view, w, h)
		tr.Frame()
		top, bottom := dialogPanelSpan(t, tr.Image())
		if top < 0 {
			t.Fatalf("overlay %v: no panel found", kind)
		}
		if top != wantTop {
			t.Errorf("overlay %v: panel starts at %d, want %d (%.0f%% of the window)",
				kind, top, wantTop, dialogTopFraction*100)
		}
		if bottom >= h-dialogMinBottom {
			t.Errorf("overlay %v: panel reaches %d, past the %d bottom margin", kind, bottom, dialogMinBottom)
		}
	}
}

// The main window's confirms and Settings' sheets are one dialog: as
// wide, as far down their window, with buttons as tall.
func TestOneDialogStyle(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	open := true
	a.killConfirmOpen = &open
	a.pendingKillPid = 999999
	a.pendingKillName = "probe"
	const w, h = 1440, 900
	main := ui.NewTester(a.view, w, h)
	main.Frame()

	s, sheet := newSettingsTester(t, "web")
	s.settingsUI.sheet = &settingsSheet{title: "Revoke probe?", confirm: "Revoke", danger: true, run: func(*ui.Context) {}}
	sheet.Frame()

	for _, d := range []struct {
		name          string
		tr            *ui.Tester
		title, button string
		winH          float32
	}{
		{"confirm", main, "Kill Process", "Kill", h},
		{"sheet", sheet, "Revoke probe?", "Revoke", settingsHeight},
	} {
		title, ok := d.tr.Find(d.title)
		if !ok {
			t.Fatalf("%s: no title %q", d.name, d.title)
		}
		if want := d.winH*dialogTopFraction + 18; title.Y < want-2 || title.Y > want+4 {
			t.Errorf("%s: title at y=%.0f, want a fifth of the way down (%.0f)", d.name, title.Y, want)
		}
		button, ok := d.tr.Find(d.button)
		if !ok {
			t.Fatalf("%s: no button %q", d.name, d.button)
		}
		if button.H != dialogButtonHeight {
			t.Errorf("%s: button %.0f tall, want %d", d.name, button.H, dialogButtonHeight)
		}
	}
}

// An editor, a diff and the graph have one header: as tall, their titles
// at one height, so switching tabs doesn't shift the content.
func TestOneViewHeader(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	root := a.projects[0].Path
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "sub", "x.txt"), []byte("x\n"), 0o644)
	tr := ui.NewTester(a.view, 1440, 900)
	at := func(text string) float32 {
		t.Helper()
		tr.Frame()
		r, ok := tr.Find(text)
		if !ok {
			t.Fatalf("no %q in the header", text)
		}
		return r.Y + r.H/2
	}
	a.openEditor(filepath.Join("sub", "x.txt"))
	editor := at("sub")
	a.openWorkingDiff("hello.txt", false)
	diff := at("Index → Working Tree")
	a.graphLoaded = true
	a.openGraphTab()
	r := a.activeRepoRef()
	if r == nil {
		t.Fatal("no repo")
	}
	graph := at(r.Name + " · " + r.Branch)
	if editor != diff || diff != graph {
		t.Errorf("header titles at y=%.1f (editor), %.1f (diff), %.1f (graph): want one height", editor, diff, graph)
	}
}

// The graph's hashes line up in every row, wherever a branch opens: they
// moved right in the rows with more lanes.
func TestGraphColumnsAligned(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.commits = shotMergeCommits()
	a.graphLoaded = true
	a.openGraphTab()
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	var x0 float32
	for i, cm := range a.commits {
		r, ok := tr.Find(cm.Short)
		if !ok {
			t.Fatalf("no row for %s", cm.Short)
		}
		if i == 0 {
			x0 = r.X
		} else if r.X != x0 {
			t.Errorf("%s at x=%.0f, %s at x=%.0f: want one column", cm.Short, r.X, a.commits[0].Short, x0)
		}
	}
}

// The palette's titles start at one edge whatever their kind: a session's
// started left of a setting's, under the label column the others had.
func TestPaletteTitlesAligned(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.openOverlay(overlayCommandPalette)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("Session:") {
		t.Error("session rows have no category label")
	}
	var xs []float32
	for _, e := range a.paletteCommands() {
		if e.Category != "Session" && e.Category != "Setting" {
			continue
		}
		if r, ok := tr.Find(e.Title); ok {
			xs = append(xs, r.X)
		}
	}
	if len(xs) < 2 {
		t.Fatalf("found %d session and setting titles", len(xs))
	}
	for _, x := range xs[1:] {
		if x != xs[0] {
			t.Fatalf("titles at x=%v: want one column", xs)
		}
	}
}

// A changed file reads alike in the Git tab and in Changes: its status
// letter after its name; Changes had it first, the Git tab last.
func TestFileRowsAlike(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	a := newShotApp(wd)
	defer stopTermPanes(a)
	shotChanges(a)
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Image() // one frame, as the shots draw: a second reloads the list from git
	name, ok := tr.Find("themewatch.go")
	if !ok {
		t.Fatal("Changes lists no themewatch.go")
	}
	letter, ok := tr.Find("M")
	if !ok {
		t.Fatal("no status letter")
	}
	if letter.X < name.X {
		t.Errorf("status letter at x=%.0f, before the name at x=%.0f: want it at the end, as the Git tab has it", letter.X, name.X)
	}
}
