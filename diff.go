package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"oterm/service"
)

// The diff view: a real editor.DiffView comparing two buffers, with the
// mock's toolbar of stage/discard/split/inline actions.

// openWorkingDiff shows INDEX ↔ WORKING TREE for path.
func (a *appState) openWorkingDiff(path string, staged bool) {
	r := a.activeRepoRef()
	if r == nil {
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
	a.setDiffBuffers(oldSrc, newSrc, path)
	a.diffSrc = diffSource{Kind: map[bool]diffSourceKind{true: diffStaged, false: diffWorking}[staged], Path: path, Repo: r.Root}
	a.diffTitle = path + " · " + scope
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
	files, perr := editor.ParsePatch(patch)
	if perr == nil && len(files) > 0 {
		fp := files[0]
		a.diffOld = nil
		a.diffNew = nil
		a.diffPatch = &fp
	} else {
		a.setDiffBuffers("", patch, "")
		a.diffPatch = nil
	}
	a.diffSrc = diffSource{Kind: diffStash, Repo: r.Root, Stash: index}
	a.diffTitle = "stash@{" + itoa(index) + "}"
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
	title := "Commit " + shortHash(hash)
	if file != "" {
		title += " · " + pathBase(file)
	}
	// Use SetPatch so the diff viewer renders the patch properly
	// (hunks with line numbers, +/- highlighting) instead of treating
	// the patch text as file content.
	files, perr := editor.ParsePatch(patch)
	if perr == nil && len(files) > 0 {
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
		a.diffOld = nil
		a.diffNew = nil
		a.diffPatch = &fp
	} else {
		a.setDiffBuffers("", patch, file)
		a.diffPatch = nil
	}
	a.diffSrc = diffSource{Kind: diffCommit, Repo: r.Root, Commit: hash, Path: file}
	a.diffTitle = title
	a.focusDiffTab("commit")
}

func (a *appState) setDiffBuffers(oldSrc, newSrc, path string) {
	a.diffOld = editor.NewBuffer(oldSrc)
	a.diffNew = editor.NewBuffer(newSrc)
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

func (a *appState) diffView(c *ui.Context) ui.Element {
	return ui.Column(c).Fill().Background(colCodeBg).Children(func() {
		// Toolbar.
		ui.Row(c).Height(40).AlignItems(ui.Center).Padding(0, 12).Gap(8).Background(colPanel).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
			ui.Icon(c, iconGitCompare).Width(16).Height(16).TextColor(colGreen)
			ui.Text(c, a.diffTitle).FontSize(12).SingleLine().Shrink(0)

			ui.Spacer(c)

			// Stage/unstage only makes sense for working/staged file diffs.
			if a.diffSrc.Kind == diffWorking || a.diffSrc.Kind == diffStaged {
				staged := a.diffSrc.Kind == diffStaged
				label := "Stage File"
				if staged {
					label = "Unstage File"
				}
				stage := ui.ButtonBase(c).Radius(6).Padding(4, 10).Background(ui.Hex("#222b36")).Children(func() {
					ui.Text(c, label).FontSize(10).TextColor(colText2)
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
						ui.Text(c, "Discard").FontSize(10).TextColor(colRed)
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

				ui.Box(c).Width(1).Height(16).Background(colBorder).Margin(0, 4)
			}

			split := ui.ButtonBase(c).Radius(6).Padding(4, 10).Children(func() {
				t := ui.Text(c, "Split").FontSize(10)
				if a.diffSplit {
					t.TextColor(ui.Hex("#ffffff"))
				} else {
					t.TextColor(colMuted)
				}
			})
			if a.diffSplit {
				split.Background(ui.Hex("#222b36"))
			}
			if split.Clicked() {
				a.diffSplit = true
			}

			inline := ui.ButtonBase(c).Radius(6).Padding(4, 10).Children(func() {
				t := ui.Text(c, "Inline").FontSize(10)
				if !a.diffSplit {
					t.TextColor(ui.Hex("#ffffff"))
				} else {
					t.TextColor(colMuted)
				}
			})
			if !a.diffSplit {
				inline.Background(ui.Hex("#222b36"))
			}
			if inline.Clicked() {
				a.diffSplit = false
			}
		})

		ds := &editor.DiffState{
			Layout: editor.DiffUnified,
			Inline: editor.DiffInlineWord,
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
				ui.Text(c, "Select a changed file to view its diff").FontSize(12).TextColor(colMuted)
			})
			return
		}
		// Stats line.
		if a.diffSrc.Commit != "" && a.diffSrc.Path == "" {
			add, del, _ := service.CommitStats(a.diffSrc.Repo, a.diffSrc.Commit)
			if add > 0 || del > 0 {
				ui.Row(c).Padding(4, 12).Gap(8).Children(func() {
					ui.Text(c, "+"+itoa(add)).FontSize(10).TextColor(colGreen).SingleLine().Shrink(0)
					ui.Text(c, "-"+itoa(del)).FontSize(10).TextColor(colRed).SingleLine().Shrink(0)
				})
			}
		}
		ui.Box(c).Grow(1).Padding(0, 8).Children(func() {
			editor.DiffView(c, ds).Fill()
		})
	})
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
