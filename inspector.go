package main

import (
	"context"
	"os"
	"strings"

	"github.com/egoist/mygo/ui"

	"oterm/service"
)

// The right inspector: Info / Files / Git panels behind a tab strip.

func (a *appState) inspector(c *ui.Context) ui.Element {
	return ui.Column(c).FillHeight().Background(colPanel).BorderWidth(0, 0, 0, 1).BorderColor(colBorder).Children(func() {
		ui.Row(c).Height(40).AlignItems(ui.End).Padding(0, 4).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
			a.inspectorTabButton(c, 0, iconInfo, "Info", 0)
			a.inspectorTabButton(c, 1, iconFolder, "Files", 0)
			a.inspectorTabButton(c, 2, iconGitBranch, "Git", a.gitCount())
			ui.Spacer(c)
			cmdButton(c, iconEllipsis, 14, false).Margin(0, 4)
		})

		switch a.inspectorTab {
		case 1:
			a.filesPanel(c)
		case 2:
			a.gitPanel(c)
		default:
			a.infoPanel(c)
		}
	})
}

func (a *appState) inspectorTabButton(c *ui.Context, idx int, icon *ui.SVG, label string, badge int) ui.Element {
	active := a.inspectorTab == idx
	e := ui.ButtonBase(c).Height(32).Padding(0, 12).Gap(6).Shrink(0)
	e.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			col := colMuted
			if active {
				col = colText2
			}
			ui.Icon(c, icon).Width(14).Height(14).TextColor(col)
			ui.Text(c, label).FontSize(11).TextColor(col).SingleLine().Shrink(0)
			if badge > 0 {
				countBadge(c, badge)
			}
		})
	})
	if active {
		e.BorderWidth(0, 0, 2, 0).BorderColor(colBlue2)
	}
	if e.Clicked() {
		a.inspectorTab = idx
		if idx == 0 {
			a.startProcessRefresh()
		} else {
			a.stopProcessRefresh()
		}
		if idx == 2 && a.gitGraphOpen && len(a.commits) == 0 {
			a.refreshGraph()
		}
	}
	return e
}

// INFO

func (a *appState) infoPanel(c *ui.Context) ui.Element {
	return ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(12).Gap(16).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				sectionLabel(c, "PROJECT")
				ui.Row(c).Radius(8).Border(1, colBorder).Background(colCard).Padding(12).Gap(8).AlignItems(ui.Center).ClipX().Children(func() {
					ui.Box(c).Width(28).Height(28).Radius(6).Border(1, colBorder2).Background(ui.Hex("#10151a")).Shrink(0).Children(func() {
						ui.Column(c).Fill().Center().Children(func() {
							ui.Icon(c, iconFolderGit).Width(16).Height(16).TextColor(colBlue)
						})
					})
					ui.Column(c).Gap(2).MinWidth(0).Grow(1).Children(func() {
						txt(c, a.project().Name, 12, colText)
						txt(c, a.project().Path, 10, colMuted)
					})
					pill(c, a.project().Branch)
				})
			})

			// PROCESSES
			ui.Column(c).Gap(8).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					sectionLabel(c, "PROCESSES")
					ui.Spacer(c)
					countBadge(c, len(a.processes))
					if cmdButton(c, iconRefresh, 14, false).Tooltip("Refresh").Clicked() {
						a.refreshProcesses()
					}
				})
				if a.processErr != "" {
					ui.Text(c, a.processErr).FontSize(10).TextColor(colRed).Padding(4, 0)
				}
				ui.Column(c).Radius(8).Border(1, colBorder).Children(func() {
					if len(a.processes) == 0 {
						ui.Box(c).Padding(16).Children(func() {
							ui.Text(c, "No processes in this project").FontSize(11).TextColor(colMuted)
						})
					}
					for i, p := range a.processes {
						row := ui.Row(c).Padding(10, 12).Gap(8).AlignItems(ui.Center).ClipX()
						if i > 0 {
							row = row.BorderWidth(1, 0, 0, 0).BorderColor(colBorder)
						}
						proc := p
						row.Children(func() {
							dot(c, dotGreen, 6)
							txt(c, proc.Name, 11, colText)
							mutedTxt2(c, itoa(proc.Pid), 11)
							ui.Spacer(c)
							mutedTxt2(c, proc.CPU+" · "+proc.Mem, 11)
							if proc.Pid != 1 && proc.Pid != os.Getpid() {
								if cmdButton(c, iconTrash, 14, false).Tooltip("Kill process").Clicked() {
									a.pendingKillPid = proc.Pid
									a.pendingKillName = proc.Name
									open := true
									a.killConfirmOpen = &open
								}
							}
						})
					}
				})
			})

			// PORTS
			ui.Column(c).Gap(8).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					sectionLabel(c, "PORTS")
					ui.Spacer(c)
					countBadge(c, len(a.ports))
					if cmdButton(c, iconRefresh, 14, false).Tooltip("Refresh").Clicked() {
						a.refreshProcesses()
					}
				})
				ui.Column(c).Radius(8).Border(1, colBorder).Children(func() {
					if len(a.ports) == 0 {
						ui.Box(c).Padding(16).Children(func() {
							ui.Text(c, "No listening ports").FontSize(11).TextColor(colMuted)
						})
					}
					for i, p := range a.ports {
						row := ui.Row(c).Padding(10, 12).Gap(8).AlignItems(ui.Center).ClipX()
						if i > 0 {
							row = row.BorderWidth(1, 0, 0, 0).BorderColor(colBorder)
						}
						port := p
						row.Children(func() {
							ui.Icon(c, iconGlobe).Width(14).Height(14).TextColor(colBlue)
							txt(c, itoa(port.Port), 11, colText)
							mutedTxt2(c, port.Name, 11)
							ui.Spacer(c)
							if port.Open {
								if cmdButton(c, iconExternalLink, 14, false).Clicked() {
									c.Toast("Open :" + itoa(port.Port))
								}
							}
						})
					}
				})
			})
		})
	})
}

// FILES

func (a *appState) filesPanel(c *ui.Context) ui.Element {
	return ui.Column(c).Grow(1).Children(func() {
		ui.Box(c).Padding(8).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
			ui.Row(c).Height(32).Radius(6).Border(1, colBorder).Background(colInput).AlignItems(ui.Center).Padding(0, 8).Gap(8).Children(func() {
				ui.Icon(c, iconSearch).Width(14).Height(14).TextColor(colMuted)
				ui.TextInputBase(c, &a.fileSearch).Placeholder("Search files").FontSize(11).Grow(1)
			})
		})

		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(4, 0).Children(func() {
				if a.fileTree != nil {
					for _, n := range a.fileTree.Children {
						a.fileNodeView(c, n, 0)
					}
				}
			})
		})
	})
}

func (a *appState) fileNodeView(c *ui.Context, n *fileNode, depth int) {
	if a.fileSearch != "" && !n.Dir && !contains(n.Name, a.fileSearch) {
		return
	}
	e := ui.Row(c).Key(n.Name).FillWidth().Justify(ui.Start).Focusable().Cursor(ui.CursorPointer).Height(28).Padding(0, 8).PaddingStart(8 + 16*float32(depth)).Gap(6)
	if e.Hovered() {
		e.Background(ui.Hex("#171d25"))
	}
	e.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			if n.Dir {
				ic := iconChevronRight
				if n.Open {
					ic = iconChevronDown
				}
				ui.Icon(c, ic).Width(14).Height(14).TextColor(colMuted)
				fi := iconFolder
				if n.Open {
					fi = iconFolderOpen
				}
				ui.Icon(c, fi).Width(16).Height(16).TextColor(synFolderIc)
			} else {
				ui.Box(c).Width(14)
				fileBadge(c, n.Name)
			}
			ui.Text(c, n.Name).FontSize(11).TextColor(colText2)
			ui.Spacer(c)
			if n.Status != "" {
				ui.Text(c, n.Status).FontSize(10).TextColor(statusColor(n.Status))
			}
		})
	})
	if e.Clicked() {
		if n.Dir {
			n.Open = !n.Open
		} else {
			a.openEditor(a.nodePath(n))
		}
	}
	if n.Dir && n.Open {
		for _, ch := range n.Children {
			a.fileNodeView(c, ch, depth+1)
		}
	}
}

// nodePath reconstructs the workspace-relative path of a file node by
// walking the tree.
func (a *appState) nodePath(target *fileNode) string {
	var path []string
	var walk func(n *fileNode, acc []string) bool
	walk = func(n *fileNode, acc []string) bool {
		acc = append(acc, n.Name)
		if n == target {
			path = acc
			return true
		}
		for _, ch := range n.Children {
			if walk(ch, acc) {
				return true
			}
		}
		return false
	}
	if a.fileTree != nil {
		// Skip the synthetic root node.
		for _, ch := range a.fileTree.Children {
			if walk(ch, nil) {
				break
			}
		}
	}
	return strings.Join(path, "/")
}

func contains(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	ls, lsub := strings.ToLower(s), strings.ToLower(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return true
		}
	}
	return false
}

// fileBadge renders the little "TS"/"{ }" emblem or a file icon.
func fileBadge(c *ui.Context, name string) ui.Element {
	switch {
	case hasSuffix(name, ".tsx") || hasSuffix(name, ".ts"):
		return ui.Text(c, "TS").FontSize(10).FontWeight(700).TextColor(synTS).Width(18)
	case hasSuffix(name, ".go"):
		return ui.Text(c, "GO").FontSize(10).FontWeight(700).TextColor(colCyan).Width(18)
	case hasSuffix(name, ".json"):
		return ui.Text(c, "{ }").FontSize(10).FontWeight(700).TextColor(synJSON).Width(18)
	case hasSuffix(name, ".md"):
		return ui.Icon(c, iconFileText).Width(16).Height(16).TextColor(colBlue).MarginStart(1)
	default:
		return ui.Icon(c, iconFileText).Width(16).Height(16).TextColor(colMuted).MarginStart(1)
	}
}

func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}

// GIT

func (a *appState) gitPanel(c *ui.Context) ui.Element {
	return ui.Column(c).Grow(1).Children(func() {
		// Repo switcher when several repos were discovered.
		if len(a.repos) > 1 {
			a.repoSwitcher(c)
		}

		r := a.activeRepoRef()
		if r == nil {
			ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
				ui.Icon(c, iconGitBranch).Width(22).Height(22).TextColor(colMuted2)
				ui.Text(c, "No git repository found").FontSize(12).TextColor(colMuted)
				if a.repoErr != "" {
					ui.Text(c, a.repoErr).FontSize(10).TextColor(colMuted2)
				}
				b := ui.ButtonBase(c).Radius(6).Border(1, colBorder2).Padding(5, 12).Children(func() {
					ui.Text(c, "Search again").FontSize(11).TextColor(colText2)
				})
				if b.Clicked() {
					a.refreshRepos()
				}
			})
			return
		}

		// Branch + commit box.
		ui.Column(c).Padding(12).Gap(10).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
			ui.Row(c).Height(24).AlignItems(ui.Center).Gap(8).ClipX().Children(func() {
				ui.Icon(c, iconGitBranch).Width(16).Height(16).TextColor(colPurple)
				txt(c, r.Branch, 11, colText)
				pill(c, "↑"+itoa(r.Ahead)+" ↓"+itoa(r.Behind))
				ui.Spacer(c)
				remote := r.HasRemote
				if b := cmdButton(c, iconDownload, 14, false).Tooltip("Fetch"); !remote {
					b.Disabled(true)
				} else if b.Clicked() {
					a.gitFetch(c)
				}
				if b := cmdButton(c, iconArrowDown, 14, false).Tooltip("Pull"); !remote {
					b.Disabled(true)
				} else if b.Clicked() {
					a.gitPull(c)
				}
				if b := cmdButton(c, iconArrowUp, 14, false).Tooltip("Push"); !remote {
					b.Disabled(true)
				} else if b.Clicked() {
					a.gitPush(c)
				}
				if cmdButton(c, iconRefresh, 14, false).Tooltip("Refresh").Clicked() {
					a.refreshActiveRepo()
				}
				if cmdButton(c, iconOpenInNew, 14, false).Tooltip("Open graph in pane").Clicked() {
					a.openGraphTab()
				}
			})
			ui.Box(c).Radius(6).Border(1, colBorder).Background(colInput).Children(func() {
				ui.TextAreaBase(c, &a.commitMsg).Placeholder("Commit message…").FontSize(11).Padding(8).MinHeight(56).FillWidth()
			})
			ui.Row(c).Gap(8).Children(func() {
				commit := ui.PrimaryButton(c, "Commit").Grow(1).Height(32).FontSize(11)
				if commit.Clicked() {
					a.gitCommit(c)
				}
				if stash := cmdButton(c, iconArchive, 14, false); stash.Tooltip("Stash changes").Clicked() {
					a.gitStashPush(c)
				}
			})
			if a.gitBusy != "" || a.gitMsg != "" {
				msg := a.gitMsg
				if a.gitBusy != "" {
					msg = a.gitBusy
				}
				col := colMuted
				if a.gitBusy == "" && a.gitErr {
					col = colRed
				}
				ui.Text(c, msg).FontSize(10).TextColor(col).SingleLine()
			}
		})

		// All sections scroll together, in normal flow: while there is
		// room they all show, and only when the content outgrows the
		// panel does it scroll. Collapsing a section gives the others
		// the space.
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Children(func() {
				a.gitSection(c, "STAGED CHANGES", len(r.Staged), true)
				if !a.stagedCollapsed {
					for i := range r.Staged {
						a.gitFileRow(c, &r.Staged[i], true)
					}
				}
				a.gitSection(c, "CHANGES", len(r.Changes), false)
				if !a.changesCollapsed {
					for i := range r.Changes {
						a.gitFileRow(c, &r.Changes[i], false)
					}
				}
				a.gitGraphSection(c)
				a.stashSection(c, r)
			})
		})

		// Footer.
		ui.Row(c).Padding(12).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Children(func() {
			ui.Text(c, r.RelPath).FontSize(10).TextColor(colMuted).SingleLine()
			ui.Spacer(c)
			ui.Text(c, "HEAD "+r.Head).FontSize(10).TextColor(colMuted).SingleLine()
		})
	})
}

// repoSwitcher lets the user pick the active repo when several exist.
func (a *appState) repoSwitcher(c *ui.Context) {
	ui.Box(c).Padding(8, 12).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
		ui.ScrollHorizontal(c).FillWidth().Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				for i := range a.repos {
					r := &a.repos[i]
					active := i == a.activeRepo
					label := r.Name
					if n := len(r.Staged) + len(r.Changes); n > 0 {
						label += " ·" + itoa(n)
					}
					e := ui.ButtonBase(c).Key(r.Root).Height(26).Radius(13).Padding(0, 12).Shrink(0).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
							ui.Icon(c, iconFolderGit).Width(12).Height(12).TextColor(colMuted)
							ui.Text(c, label).FontSize(10).SingleLine().Shrink(0)
						})
					})
					if active {
						e.Background(colHover).Border(1, colBorder2)
					} else {
						e.Border(1, colBorder)
					}
					if e.Clicked() && !active {
						a.activeRepo = i
						a.commits = nil
					}
				}
			})
		})
	})
}

func (a *appState) gitCommit(c *ui.Context) {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	if len(r.Staged) == 0 {
		c.Toast("Nothing staged to commit")
		return
	}
	msg := strings.TrimSpace(a.commitMsg)
	if msg == "" {
		c.Toast("Commit message is empty")
		return
	}
	if err := service.CommitStaged(r.Root, msg); err != nil {
		c.Toast("Commit failed: " + err.Error())
		return
	}
	n := len(r.Staged)
	a.commitMsg = ""
	a.refreshActiveRepo()
	word := "files"
	if n == 1 {
		word = "file"
	}
	c.Toast("Committed " + itoa(n) + " " + word)
}

func (a *appState) gitSection(c *ui.Context, label string, count int, staged bool) ui.Element {
	collapsed := &a.stagedCollapsed
	if !staged {
		collapsed = &a.changesCollapsed
	}
	icon := iconChevronRight
	if !*collapsed {
		icon = iconChevronDown
	}
	e := ui.Row(c).Height(32).Padding(0, 8).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if !staged {
		e = e.BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Margin(4, 0, 0, 0)
	}
	e.Children(func() {
		ui.Icon(c, icon).Width(14).Height(14).TextColor(colMuted)
		txt(c, label, 10, colMuted).LetterSpacing(1.0)
		countBadge(c, count)
		ui.Spacer(c)
		r := a.activeRepoRef()
		if staged {
			all := cmdButton(c, iconMinus, 14, false).Tooltip("Unstage all")
			if all.Clicked() && r != nil {
				if err := service.UnstageAll(r.Root); err != nil {
					c.Toast("Git error: " + err.Error())
				} else {
					a.refreshActiveRepo()
				}
			}
		} else {
			stageAll := cmdButton(c, iconPlus, 14, false).Tooltip("Stage all")
			if stageAll.Clicked() && r != nil {
				if err := service.StageAll(r.Root); err != nil {
					c.Toast("Git error: " + err.Error())
				} else {
					a.refreshActiveRepo()
				}
			}
		}
	})
	if e.Clicked() {
		*collapsed = !*collapsed
	}
	return e
}

// gitGraphSection renders the commit graph as a collapsible section.
func (a *appState) gitGraphSection(c *ui.Context) {
	icon := iconChevronRight
	if a.gitGraphOpen {
		icon = iconChevronDown
	}
	e := ui.Row(c).Height(32).Padding(0, 8).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Margin(4, 0, 0, 0)
	e.Children(func() {
		ui.Icon(c, icon).Width(14).Height(14).TextColor(colMuted)
		txt(c, "GIT GRAPH", 10, colMuted).LetterSpacing(1.0)
		countBadge(c, len(a.commits))
		ui.Spacer(c)
		if cmdButton(c, iconOpenInNew, 14, false).Tooltip("Open in pane").Clicked() {
			a.openGraphTab()
		}
	})
	if e.Clicked() {
		a.gitGraphOpen = !a.gitGraphOpen
		if a.gitGraphOpen && len(a.commits) == 0 {
			a.refreshGraph()
		}
	}
	if a.gitGraphOpen {
		if len(a.commits) == 0 {
			ui.Text(c, "Loading…").FontSize(11).TextColor(colMuted).Padding(12)
		} else {
			a.graphCompactView(c)
		}
	}
}

func (a *appState) gitFileRow(c *ui.Context, f *service.GitFile, staged bool) ui.Element {
	e := ui.Row(c).Key(f.Path).FillWidth().Justify(ui.Start).Focusable().Cursor(ui.CursorPointer).Height(40).Padding(0, 12).ClipX().AlignItems(ui.Center).Gap(8)
	hover := e.Hovered()
	if hover {
		e.Background(ui.Hex("#171d25"))
	}
	e.Children(func() {
		fileBadge(c, pathBase(f.Path))
		ui.Column(c).Gap(1).MinWidth(0).Grow(1).Children(func() {
			txt(c, pathBase(f.Path), 11, colText)
			txt(c, pathDir(f.Path), 9, colMuted)
		})
		actions := ui.Row(c).Gap(2).Shrink(0)
		if !hover {
			actions.Opacity(0)
		}
		actions.Children(func() {
			r := a.activeRepoRef()
			if staged {
				un := cmdButton(c, iconMinus, 14, false).Tooltip("Unstage")
				if un.Clicked() && r != nil {
					if err := service.Unstage(r.Root, f.Path); err != nil {
						c.Toast("Git error: " + err.Error())
					} else {
						a.refreshActiveRepo()
					}
				}
			} else {
				st := cmdButton(c, iconPlus, 14, false).Tooltip("Stage")
				if st.Clicked() && r != nil {
					if err := service.Stage(r.Root, f.Path); err != nil {
						c.Toast("Git error: " + err.Error())
					} else {
						a.refreshActiveRepo()
					}
				}
				discardIcon := iconUndo
				if f.Status == "U" || f.Status == "?" {
					discardIcon = iconTrash
				}
				di := cmdButton(c, discardIcon, 14, false).Tooltip("Discard")
				if di.Clicked() && r != nil {
					if err := service.Discard(r.Root, f.Path, f.Status == "U"); err != nil {
						c.Toast("Git error: " + err.Error())
					} else {
						a.refreshActiveRepo()
					}
				}
			}
		})
		txt(c, f.Status, 10, statusColor(f.Status))
	})
	if e.Clicked() {
		a.openWorkingDiff(f.Path, staged)
	}
	return e
}

// stashSection renders the STASHES section, collapsible like the others,
// with its create form and list in the panel's normal scroll.
func (a *appState) stashSection(c *ui.Context, r *service.Repo) {
	icon := iconChevronRight
	if !a.stashCollapsed {
		icon = iconChevronDown
	}
	e := ui.Row(c).Height(32).Padding(0, 8).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Margin(4, 0, 0, 0)
	e.Children(func() {
		ui.Icon(c, icon).Width(14).Height(14).TextColor(colMuted)
		txt(c, "STASHES", 10, colMuted).LetterSpacing(1.0)
		countBadge(c, len(r.Stashes))
		ui.Spacer(c)
	})
	if e.Clicked() {
		a.stashCollapsed = !a.stashCollapsed
	}

	if !a.stashCollapsed {
		// Create-stash form.
		ui.Box(c).Padding(6, 12).Children(func() {
			ui.Row(c).Height(28).Radius(6).Border(1, colBorder).Background(colInput).AlignItems(ui.Center).Padding(0, 8).Gap(6).Children(func() {
				ui.Icon(c, iconArchive).Width(12).Height(12).TextColor(colMuted)
				ui.TextInputBase(c, &a.stashMsg).Placeholder("Stash message…").FontSize(10).Grow(1)
				track := cmdButton(c, iconPlus, 12, false).Tooltip("Stash (include untracked)")
				if track.Clicked() {
					a.gitStashPushUntracked(c)
				}
			})
		})

		for i := range r.Stashes {
			a.stashRow(c, r, &r.Stashes[i])
		}
	}
}

func (a *appState) stashRow(c *ui.Context, r *service.Repo, s *service.Stash) {
	e := ui.Row(c).Key(s.Ref).FillWidth().Justify(ui.Start).Cursor(ui.CursorPointer).Height(38).Padding(0, 12).ClipX().AlignItems(ui.Center).Gap(8)
	hover := e.Hovered()
	if hover {
		e.Background(ui.Hex("#171d25"))
	}
	e.Children(func() {
		ui.Icon(c, iconArchive).Width(14).Height(14).TextColor(colYellow)
		ui.Column(c).Gap(1).MinWidth(0).Grow(1).Children(func() {
			txt(c, s.Message, 11, colText)
			meta := s.Ref
			if s.Branch != "" {
				meta += " · " + s.Branch
			}
			meta += " · " + relativeTime(s.When)
			txt(c, meta, 9, colMuted)
		})
		actions := ui.Row(c).Gap(2).Shrink(0)
		if !hover {
			actions.Opacity(0)
		}
		actions.Children(func() {
			idx := s.Index
			apply := cmdButton(c, iconRotate, 14, false).Tooltip("Apply")
			if apply.Clicked() {
				a.gitStashApply(c, idx)
			}
			pop := cmdButton(c, iconExternalLink, 14, false).Tooltip("Pop")
			if pop.Clicked() {
				a.gitStashPop(c, idx)
			}
			drop := cmdButton(c, iconTrash, 14, false).Tooltip("Drop")
			if drop.Clicked() {
				a.gitStashDrop(c, idx)
			}
		})
	})
	if e.Clicked() {
		a.openStashDiff(s.Index)
	}
}

func (a *appState) gitStashPush(c *ui.Context) {
	a.gitStashPushOpt(c, false)
}

func (a *appState) gitStashPushUntracked(c *ui.Context) {
	a.gitStashPushOpt(c, true)
}

func (a *appState) gitStashPushOpt(c *ui.Context, untracked bool) {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	if len(r.Changes)+len(r.Staged) == 0 {
		c.Toast("Nothing to stash")
		return
	}
	if err := service.StashPush(r.Root, a.stashMsg, untracked); err != nil {
		c.Toast("Stash failed: " + err.Error())
		return
	}
	a.stashMsg = ""
	a.refreshActiveRepo()
	c.Toast("Stashed changes")
}

func (a *appState) gitStashApply(c *ui.Context, index int) {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	if err := service.StashApply(r.Root, index); err != nil {
		c.Toast("Apply failed: " + err.Error())
		return
	}
	a.refreshActiveRepo()
	c.Toast("Applied stash@{" + itoa(index) + "}")
}

func (a *appState) gitStashPop(c *ui.Context, index int) {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	if err := service.StashPop(r.Root, index); err != nil {
		c.Toast("Pop failed: " + err.Error())
		return
	}
	a.refreshActiveRepo()
	c.Toast("Popped stash@{" + itoa(index) + "}")
}

func (a *appState) gitStashDrop(c *ui.Context, index int) {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	if err := service.StashDrop(r.Root, index); err != nil {
		c.Toast("Drop failed: " + err.Error())
		return
	}
	a.refreshActiveRepo()
	c.Toast("Dropped stash@{" + itoa(index) + "}")
}

// gitRemoteOp runs a network git command off the UI thread, showing its
// progress in the panel while it runs.
func (a *appState) gitRemoteOp(verb string, fn func(root string) (string, error)) {
	r := a.activeRepoRef()
	if r == nil || a.win == nil {
		return
	}
	root := r.Root
	win := a.win
	a.gitBusy = verb + "…"
	a.gitMsg = ""
	a.gitErr = false
	a.gitJob.Run(func(ctx context.Context) error {
		out, err := fn(root)
		msg := strings.TrimSpace(out)
		if err == nil {
			msg = verb + " done"
		} else if msg == "" {
			msg = verb + " failed"
		} else if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		win.Update(func() {
			a.gitBusy = ""
			a.gitMsg = msg
			a.gitErr = err != nil
			a.refreshActiveRepo()
		})
		return err
	}, nil)
}

func (a *appState) gitFetch(c *ui.Context) {
	a.gitRemoteOp("Fetch", service.Fetch)
}

func (a *appState) gitPull(c *ui.Context) {
	a.gitRemoteOp("Pull", service.Pull)
}

func (a *appState) gitPush(c *ui.Context) {
	a.gitRemoteOp("Push", service.Push)
}
