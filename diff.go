package main

import (
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"omaterm/service"
)

// The diff view: a real editor.DiffView comparing two buffers, with the
// mock's toolbar of stage/discard/split/inline actions.

// openWorkingDiff shows INDEX ↔ WORKING TREE for path.
func (a *appState) openWorkingDiff(path string, staged bool) {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	if isRasterImagePath(path) {
		// Pictures need their bytes as they are: the text helpers trim
		// trailing newlines, which would corrupt a bitmap.
		var oldData, newData []byte
		if staged {
			oldData, _ = service.HeadBytes(r.Root, path)
			newData, _ = service.IndexBytes(r.Root, path)
		} else {
			oldData, _ = service.IndexBytes(r.Root, path)
			if len(oldData) == 0 {
				oldData, _ = service.HeadBytes(r.Root, path)
			}
			newData, _ = os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(path)))
		}
		a.diffSrc = diffSource{Kind: map[bool]diffSourceKind{true: diffStaged, false: diffWorking}[staged], Path: path, Repo: r.Root}
		scope := "Working Tree · INDEX ↔ WORKING TREE"
		if staged {
			scope = "Staged · HEAD ↔ INDEX"
		}
		a.diffTitle = path + " · " + scope
		a.setDiffImageData(path, oldData, newData)
		a.focusDiffTab(path)
		return
	}
	var oldSrc, newSrc, scope string
	if staged {
		oldSrc, _ = service.HeadVersion(r.Root, path)
		newSrc, _ = service.IndexVersion(r.Root, path)
		scope = "Staged · HEAD ↔ INDEX"
	} else {
		oldSrc, _ = service.IndexVersion(r.Root, path)
		if oldSrc == "" {
			oldSrc, _ = service.HeadVersion(r.Root, path)
		}
		data, err := readFileAbs(r.Root, path)
		if err == nil {
			newSrc = data
		}
		scope = "Working Tree · INDEX ↔ WORKING TREE"
	}
	a.diffSrc = diffSource{Kind: map[bool]diffSourceKind{true: diffStaged, false: diffWorking}[staged], Path: path, Repo: r.Root}
	a.diffTitle = path + " · " + scope
	if service.IsBinary([]byte(oldSrc)) || service.IsBinary([]byte(newSrc)) {
		a.setDiffBinary(path, fileSize(filepath.Join(r.Root, path)))
		a.focusDiffTab(path)
		return
	}
	a.setDiffBuffers(oldSrc, newSrc, path)
	a.focusDiffTab(path)
}

// openStashDiff shows the patch of stash@{index}.
func (a *appState) openStashDiff(index int) {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	patch, err := service.StashShow(r.Root, index)
	if err != nil {
		return
	}
	a.diffSrc = diffSource{Kind: diffStash, Repo: r.Root, Stash: index}
	title := "stash@{" + itoa(index) + "}"
	a.diffFiles, a.diffFileShow = nil, nil
	if files, perr := editor.ParsePatch(patch); perr == nil && len(files) > 0 {
		if len(files) > 1 {
			a.diffFiles = files
			a.diffFileShow = func(i int) {
				a.diffFileIdx = i
				a.showPatchFile(files[i], "stash@{"+itoa(index)+"} · ", r.Root, "stash@{"+itoa(index)+"}^1", "stash@{"+itoa(index)+"}")
			}
			a.diffFileShow(0)
			a.focusDiffTab("stash")
			return
		}
		fp := files[0]
		name := fp.NewName
		if name == "" {
			name = fp.OldName
		}
		if name != "" {
			title += " · " + pathBase(name)
		}
		a.diffTitle = title
		switch {
		case fp.Binary && isRasterImagePath(name):
			// The patch only says the pictures differ; the viewer wants
			// the two versions themselves.
			oldData, _ := service.ShowBytes(r.Root, "stash@{"+itoa(index)+"}^1", name)
			newData, _ := service.ShowBytes(r.Root, "stash@{"+itoa(index)+"}", name)
			a.setDiffImageData(name, oldData, newData)
		case fp.Binary:
			a.setDiffBinary(name, 0)
		default:
			a.setDiffPatch(fp)
		}
	} else {
		a.diffTitle = title
		a.setDiffBuffers("", patch, "")
	}
	a.focusDiffTab("stash")
}

// openCommitDiff shows the commit's full patch against its first parent.
func (a *appState) openCommitDiff(hash string) {
	a.openCommitFileDiff(hash, "")
}

func (a *appState) openCommitFileDiff(hash, file string) {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	var patch string
	var err error
	if file != "" {
		patch, err = service.ShowCommitFilePatch(r.Root, hash, file)
	} else {
		patch, err = service.ShowCommitPatch(r.Root, hash)
	}
	if err != nil {
		return
	}
	a.diffSrc = diffSource{Kind: diffCommit, Repo: r.Root, Commit: hash, Path: file}
	title := "Commit " + shortHash(hash)
	if file != "" {
		title += " · " + pathBase(file)
	}
	// Use SetPatch so the diff viewer renders the patch properly
	// (hunks with line numbers, +/- highlighting) instead of treating
	// the patch text as file content.
	a.diffFiles, a.diffFileShow = nil, nil
	if files, perr := editor.ParsePatch(patch); perr == nil && len(files) > 1 && file == "" {
		// A whole commit: every file, one at a time, with the toolbar's
		// file strip to go from one to the next.
		a.diffFiles = files
		a.diffFileShow = func(i int) {
			a.diffFileIdx = i
			a.showPatchFile(files[i], "Commit "+shortHash(hash)+" · ", r.Root, hash+"^", hash)
		}
		a.diffFileShow(0)
		a.focusDiffTab("commit")
		return
	}
	if files, perr := editor.ParsePatch(patch); perr == nil && len(files) > 0 {
		// Find the right file if specified.
		fp := files[0]
		if file != "" {
			for _, f := range files {
				if f.NewName == file || f.OldName == file {
					fp = f
					break
				}
			}
		}
		shown := fp.NewName
		if shown == "" {
			shown = fp.OldName
		}
		// The title names the file actually shown, so a commit opened
		// without a path does not present files[0] as the whole commit.
		if shown != "" && file == "" {
			title += " · " + pathBase(shown)
			if len(files) > 1 {
				title += " (1/" + itoa(len(files)) + ")"
			}
		}
		a.diffTitle = title
		switch {
		case fp.Binary && isRasterImagePath(shown):
			// Compare the picture against the commit's first parent, or
			// against nothing for a root commit.
			parent := hash + "^"
			oldData, _ := service.ShowBytes(r.Root, parent, shown)
			newData, _ := service.ShowBytes(r.Root, hash, shown)
			a.setDiffImageData(shown, oldData, newData)
		case fp.Binary:
			a.setDiffBinary(shown, 0)
		default:
			a.setDiffPatch(fp)
		}
	} else {
		a.diffTitle = title
		a.setDiffBuffers("", patch, file)
	}
	a.focusDiffTab("commit")
}

// showPatchFile shows one file of a multi-file patch: its patch, or its
// two pictures, or a binary placeholder.
func (a *appState) showPatchFile(fp editor.FilePatch, prefix, root, oldRev, newRev string) {
	name := fp.NewName
	if name == "" {
		name = fp.OldName
	}
	a.diffTitle = prefix + pathBase(name)
	switch {
	case fp.Binary && isRasterImagePath(name):
		oldData, _ := service.ShowBytes(root, oldRev, name)
		newData, _ := service.ShowBytes(root, newRev, name)
		a.setDiffImageData(name, oldData, newData)
	case fp.Binary:
		a.setDiffBinary(name, 0)
	default:
		a.setDiffPatch(fp)
	}
}

// diffFileStrip goes from file to file of a multi-file diff: "3/7", with
// the previous and next files, and a menu of them all.
func (a *appState) diffFileStrip(c *ui.Context) {
	n := len(a.diffFiles)
	if n < 2 || a.diffFileShow == nil {
		return
	}
	i := a.diffFileIdx
	ui.Row(c.Key("files")).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		if cmdButton(c, iconChevronLeft, 13, false).Tooltip("Previous file").Clicked() && i > 0 {
			a.diffFileShow(i - 1)
		}
		label := ui.Text(c, itoa(i+1)+"/"+itoa(n)).FontSize(11.5).TextColor(pal.TextMuted).SingleLine().Cursor(ui.CursorPointer)
		label.ContextMenu(func(m *ui.Menu) {
			for k, f := range a.diffFiles {
				name := f.NewName
				if name == "" {
					name = f.OldName
				}
				if m.Item(name).Chosen() {
					a.diffFileShow(k)
				}
			}
		})
		if cmdButton(c, iconChevronRight, 13, false).Tooltip("Next file").Clicked() && i < n-1 {
			a.diffFileShow(i + 1)
		}
	})
}

// setDiffPatch shows a parsed file patch and caches its line counts for
// the toolbar, so the stats do not run git on every frame.
func (a *appState) setDiffPatch(fp editor.FilePatch) {
	a.diffBinary = false
	a.diffImage = false
	a.diffBinaryName = ""
	a.diffBinarySize = 0
	a.diffImgOld, a.diffImgNew = imageSide{}, imageSide{}
	a.diffOld = nil
	a.diffNew = nil
	a.diffPatch = &fp
	a.diffAdd, a.diffDel = patchStats(fp)
	a.diffGen++
}

// setDiffImageData shows two versions of a picture side by side. An empty
// side is one the file does not exist in.
func (a *appState) setDiffImageData(name string, oldData, newData []byte) {
	a.diffOld = nil
	a.diffNew = nil
	a.diffPatch = nil
	a.diffBinary = false
	a.diffImage = true
	a.diffBinaryName = name
	a.diffBinarySize = int64(len(newData))
	a.diffImgOld = decodeImage(name, oldData)
	a.diffImgNew = decodeImage(name, newData)
	a.diffAdd, a.diffDel = 0, 0
	a.diffGen++
}

// setDiffBinary shows a placeholder for a file with no text diff.
func (a *appState) setDiffBinary(name string, size int64) {
	a.diffOld = nil
	a.diffNew = nil
	a.diffPatch = nil
	a.diffBinary = true
	a.diffImage = false
	a.diffBinaryName = name
	a.diffBinarySize = size
	a.diffImgOld, a.diffImgNew = imageSide{}, imageSide{}
	a.diffAdd, a.diffDel = 0, 0
	a.diffGen++
}

// patchStats counts added and deleted lines in a file patch.
func patchStats(fp editor.FilePatch) (add, del int) {
	for _, h := range fp.Hunks {
		for _, line := range h.Lines {
			if line == "" {
				continue
			}
			switch line[0] {
			case '+':
				add++
			case '-':
				del++
			}
		}
	}
	return add, del
}

// fileSize returns the size of abs, or 0 when it cannot be read.
func fileSize(abs string) int64 {
	if info, err := os.Stat(abs); err == nil {
		return info.Size()
	}
	return 0
}

func (a *appState) setDiffBuffers(oldSrc, newSrc, path string) {
	a.diffBinary = false
	a.diffImage = false
	a.diffBinaryName = ""
	a.diffBinarySize = 0
	a.diffImgOld, a.diffImgNew = imageSide{}, imageSide{}
	a.diffOld = editor.NewBuffer(oldSrc)
	a.diffNew = editor.NewBuffer(newSrc)
	a.diffAdd, a.diffDel = 0, 0
	a.diffGen++
	if path != "" {
		if lang := editor.LanguageFor(path); lang != nil {
			a.diffOld.SetLanguage(lang)
			a.diffNew.SetLanguage(lang)
		}
	}
}

// focusDiffTab activates a diff tab, creating it if needed.
func (a *appState) focusDiffTab(key string) {
	for i, t := range a.tabs {
		if t.Kind == tabDiff && t.Path == key {
			a.activeTab = i
			a.activeView = tabDiff
			return
		}
	}
	a.tabs = append(a.tabs, topTab{
		Kind:  tabDiff,
		Label: "Diff",
		Icon:  fileIconDiff,
		Path:  key,
	})
	a.activeTab = len(a.tabs) - 1
	a.activeView = tabDiff
}

// diffViewKey identifies the content + layout a persisted DiffState shows.
// The DiffState is rebuilt only when this key changes, so scrolling
// survives re-renders.
func (a *appState) diffViewKey() string {
	kind := "patch"
	if a.diffPatch == nil {
		kind = "bufs"
	}
	if a.diffBinary {
		kind = "binary"
	}
	if a.diffImage {
		kind = "image"
	}
	layout := "unified"
	if a.diffSplit {
		layout = "split"
	}
	return a.diffTitle + "\x00" + kind + "\x00" + layout + "\x00" + itoa(a.diffGen)
}

func (a *appState) diffView(c *ui.Context) ui.Element {
	return ui.Column(c).Fill().Background(pal.Code).Children(func() {
		// Toolbar.
		ui.Row(c).Height(40).AlignItems(ui.Center).Padding(0, 12).Gap(8).Background(pal.Sidebar).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
			ui.Icon(c, iconGitCompare).Width(16).Height(16).TextColor(pal.Ok)
			ui.Text(c, a.diffTitle).FontSize(12).SingleLine().Shrink(1).MinWidth(0).
				Ellipsis("…").Tooltip(a.diffTitle)

			// Per-file line stats, next to the title they belong to.
			if !a.diffBinary && (a.diffAdd > 0 || a.diffDel > 0) {
				ui.Text(c, "+"+itoa(a.diffAdd)).FontSize(10).TextColor(pal.Ok).SingleLine().Shrink(0)
				ui.Text(c, "-"+itoa(a.diffDel)).FontSize(10).TextColor(pal.Bad).SingleLine().Shrink(0)
			}

			a.diffFileStrip(c)
			ui.Spacer(c)

			// Stage/unstage only makes sense for working/staged file diffs.
			if a.diffSrc.Kind == diffWorking || a.diffSrc.Kind == diffStaged {
				staged := a.diffSrc.Kind == diffStaged
				label := "Stage File"
				if staged {
					label = "Unstage File"
				}
				stage := ui.ButtonBase(c).Radius(6).Padding(4, 10).Background(pal.Chip).Children(func() {
					ui.Text(c, label).FontSize(10).TextColor(pal.TextSoft)
				})
				if stage.Clicked() {
					var err error
					if staged {
						err = service.Unstage(a.diffSrc.Repo, a.diffSrc.Path)
					} else {
						err = service.Stage(a.diffSrc.Repo, a.diffSrc.Path)
					}
					if err != nil {
						c.Toast("Git error: " + err.Error())
					} else {
						a.refreshActiveRepo()
					}
				}

				if !staged {
					discard := ui.ButtonBase(c).Radius(6).Padding(4, 10).Children(func() {
						ui.Text(c, "Discard").FontSize(10).TextColor(pal.Bad)
					})
					if discard.Clicked() {
						untracked := false
						if r := a.activeRepoRef(); r != nil {
							for _, f := range r.Changes {
								if f.Path == a.diffSrc.Path && f.Status == "U" {
									untracked = true
								}
							}
						}
						if err := service.Discard(a.diffSrc.Repo, a.diffSrc.Path, untracked); err != nil {
							c.Toast("Git error: " + err.Error())
						} else {
							a.refreshActiveRepo()
							a.activeView = tabTerminal
						}
					}
				}

				ui.Box(c).Width(1).Height(16).Background(pal.Border).Margin(0, 4)
			}

			// Split/Inline choose between two text layouts; a picture or
			// binary diff has no lines to lay out.
			if !a.diffImage && !a.diffBinary {
				split := ui.ButtonBase(c).Radius(6).Padding(4, 10).Children(func() {
					t := ui.Text(c, "Split").FontSize(10)
					if a.diffSplit {
						t.TextColor(pal.TextStrong)
					} else {
						t.TextColor(pal.TextMuted)
					}
				})
				if a.diffSplit {
					split.Background(pal.Chip)
				}
				if split.Clicked() {
					a.diffSplit = true
					a.saveSettings()
				}

				inline := ui.ButtonBase(c).Radius(6).Padding(4, 10).Children(func() {
					t := ui.Text(c, "Inline").FontSize(10)
					if !a.diffSplit {
						t.TextColor(pal.TextStrong)
					} else {
						t.TextColor(pal.TextMuted)
					}
				})
				if !a.diffSplit {
					inline.Background(pal.Chip)
				}
				if inline.Clicked() {
					a.diffSplit = false
					a.saveSettings()
				}
			}
		})

		// A picture diff is the two pictures side by side: a binary patch
		// has no lines to compare, and Split/Inline describe text.
		if a.diffImage {
			a.imageDiff(c, a.diffImgOld, a.diffImgNew)
			return
		}

		if a.diffBinary {
			ui.Column(c).Grow(1).Center().Gap(6).Children(func() {
				ui.Icon(c, iconFileCode).Width(28).Height(28).TextColor(pal.TextFaint)
				name := a.diffBinaryName
				if name == "" {
					name = a.diffTitle
				}
				ui.Text(c, pathBase(name)).FontSize(13).TextColor(pal.TextSoft)
				size := ""
				if a.diffBinarySize > 0 {
					size = service.FormatBytes(uint64(a.diffBinarySize)) + " · "
				}
				ui.Text(c, "Binary file · "+size+"no text diff to show").FontSize(11).TextColor(pal.TextMuted)
			})
			return
		}

		// Side labels in split view, so it is clear which column is which
		// side of the comparison.
		if a.diffSplit {
			oldLabel, newLabel := a.diffSideLabels()
			ui.Row(c).Height(24).Padding(0, 16).Gap(8).Background(pal.Sidebar).
				BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
				ui.Text(c, oldLabel).FontSize(9).TextColor(pal.TextMuted).LetterSpacing(1.0).Grow(1).SingleLine()
				ui.Text(c, newLabel).FontSize(9).TextColor(pal.TextMuted).LetterSpacing(1.0).Grow(1).SingleLine()
			})
		}

		// Keep one DiffState alive across frames: MyGo stores the viewport
		// scroll inside DiffState, so rebuilding it per frame resets the
		// scrollbar before every draw. Rebuild only when the content or
		// the split/inline layout changes.
		key := a.diffViewKey()
		ds := a.diffDs
		if ds == nil || a.diffKey != key {
			ds = &editor.DiffState{
				Layout: editor.DiffUnified,
				Inline: editor.DiffInlineWord,
				Theme:  omaEditorTheme(),
				Font:   a.editorFont(a.fontSize),
			}
			if a.diffSplit {
				ds.Layout = editor.DiffSplit
			}
			if a.diffPatch != nil {
				ds.SetPatch(*a.diffPatch)
			} else if a.diffOld != nil && a.diffNew != nil {
				ds.Old = a.diffOld
				ds.New = a.diffNew
			} else {
				ui.Column(c).Grow(1).Center().Children(func() {
					ui.Text(c, "Select a changed file to view its diff").FontSize(12).TextColor(pal.TextMuted)
				})
				return
			}
			a.diffDs = ds
			a.diffKey = key
		}
		ui.Box(c).Grow(1).Padding(0, 8).Children(func() {
			editor.DiffView(c, ds).Fill()
		})
	})
}

// diffSideLabels names the two columns of a split diff.
func (a *appState) diffSideLabels() (oldLabel, newLabel string) {
	switch a.diffSrc.Kind {
	case diffStaged:
		return "HEAD", "INDEX"
	case diffCommit:
		return "PARENT", "COMMIT"
	case diffStash:
		return "STASH^", "STASH"
	default:
		return "INDEX", "WORKING TREE"
	}
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// pathListSummary renders "N files changed" for a commit, singular at one.
func pathListSummary(n int) string {
	word := "files changed"
	if n == 1 {
		word = "file changed"
	}
	return itoa(n) + " " + word
}
