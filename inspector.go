package main

import (
	"github.com/egoist/mygo/ui"
)

// The right inspector: Info / Files / Git panels behind a tab strip.

func (a *appState) inspector(c *ui.Context) ui.Element {
	return ui.Column(c).FillHeight().Background(colPanel).BorderWidth(0, 0, 0, 1).BorderColor(colBorder).Children(func() {
		// Tab strip.
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
	}
	return e
}

// INFO

func (a *appState) infoPanel(c *ui.Context) ui.Element {
	return ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(12).Gap(16).Children(func() {
			// Project card.
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

			// Processes.
			ui.Column(c).Gap(8).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					sectionLabel(c, "PROCESSES")
					ui.Spacer(c)
					countBadge(c, len(a.processes))
				})
				ui.Column(c).Radius(8).Border(1, colBorder).Children(func() {
					for i, p := range a.processes {
						row := ui.Row(c).Padding(10, 12).Gap(8).AlignItems(ui.Center).ClipX()
						if i > 0 {
							row = row.BorderWidth(1, 0, 0, 0).BorderColor(colBorder)
						}
						row.Children(func() {
							dot(c, dotGreen, 6)
							txt(c, p.Name, 11, colText)
							mutedTxt2(c, itoa(p.Pid), 11)
							ui.Spacer(c)
							mutedTxt2(c, p.CPU+" · "+p.Mem, 11)
						})
					}
				})
			})

			// Ports.
			ui.Column(c).Gap(8).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					sectionLabel(c, "PORTS")
					ui.Spacer(c)
					countBadge(c, len(a.ports))
				})
				ui.Column(c).Radius(8).Border(1, colBorder).Children(func() {
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
		// Search box, the mock's .selection field without a second border.
		ui.Box(c).Padding(8).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
			ui.Row(c).Height(32).Radius(6).Border(1, colBorder).Background(colInput).AlignItems(ui.Center).Padding(0, 8).Gap(8).Children(func() {
				ui.Icon(c, iconSearch).Width(14).Height(14).TextColor(colMuted)
				ui.TextInputBase(c, &a.fileSearch).Placeholder("Search files").FontSize(11).Grow(1)
			})
		})

		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(4, 0).Children(func() {
				for _, n := range a.fileTree.Children {
					a.fileNodeView(c, n, 0)
				}
			})
		})
	})
}

func (a *appState) fileNodeView(c *ui.Context, n *fileNode, depth int) {
	if a.fileSearch != "" && !n.Dir && !contains(n.Name, a.fileSearch) {
		return
	}
	e := ui.ButtonBase(c).Key(n.Name).FillWidth().Height(28).Padding(0, 8).PaddingStart(8 + 16*float32(depth)).Gap(6)
	e.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
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
		} else if n.Action == "diff" {
			a.activeView = tabDiff
		} else if n.Action == "editor" {
			a.activeView = tabEditor
		}
	}
	if n.Dir && n.Open {
		for _, ch := range n.Children {
			a.fileNodeView(c, ch, depth+1)
		}
	}
}

func contains(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
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
		// Branch + commit box.
		ui.Column(c).Padding(12).Gap(10).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
			ui.Row(c).Height(24).AlignItems(ui.Center).Gap(8).ClipX().Children(func() {
				ui.Icon(c, iconGitBranch).Width(16).Height(16).TextColor(colPurple)
				txt(c, "main", 11, colText)
				pill(c, "↑0 ↓0")
				ui.Spacer(c)
				if cmdButton(c, iconRefresh, 14, false).Clicked() {
					c.Toast("Refreshed git status")
				}
				cmdButton(c, iconEllipsis, 14, false)
			})
			// Commit message box, the mock's .selection textarea.
			ui.Box(c).Radius(6).Border(1, colBorder).Background(colInput).Children(func() {
				ui.TextAreaBase(c, &a.commitMsg).FontSize(11).Padding(8).MinHeight(56).FillWidth()
			})
			ui.Row(c).Gap(8).Children(func() {
				commit := ui.PrimaryButton(c, "Commit").Grow(1).Height(32).FontSize(11)
				if commit.Clicked() {
					if len(a.staged) == 0 {
						c.Toast("Nothing staged to commit")
					} else {
						n := len(a.staged)
						a.staged = nil
						a.commitMsg = ""
						word := "files"
						if n == 1 {
							word = "file"
						}
						c.Toast("Committed " + itoa(n) + " staged " + word)
					}
				}
				// Split button for commit options.
				ui.ButtonBase(c).Width(32).Height(32).Radius(6).Border(1, colBorder2).Shrink(0).Children(func() {
					ui.Column(c).Fill().Center().Children(func() {
						ui.Icon(c, iconChevronDown).Width(14).Height(14).TextColor(colMuted)
					})
				})
			})
		})

		// Staged + changes lists.
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Children(func() {
				a.gitSection(c, "STAGED CHANGES", len(a.staged), true)
				for i := range a.staged {
					a.gitFileRow(c, &a.staged[i], true)
				}
				a.gitSection(c, "CHANGES", len(a.changes), false)
				for i := range a.changes {
					a.gitFileRow(c, &a.changes[i], false)
				}
			})
		})

		// Footer.
		ui.Row(c).Padding(12).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Children(func() {
			ui.Text(c, "origin/main").FontSize(10).TextColor(colMuted)
			ui.Spacer(c)
			ui.Text(c, "HEAD 84a2f73").FontSize(10).TextColor(colMuted)
		})
	})
}

func (a *appState) gitSection(c *ui.Context, label string, count int, staged bool) ui.Element {
	e := ui.Row(c).Height(32).Padding(0, 8).Gap(4).AlignItems(ui.Center)
	if !staged {
		e = e.BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Margin(4, 0, 0, 0)
	}
	e.Children(func() {
		ui.Icon(c, iconChevronDown).Width(14).Height(14).TextColor(colMuted)
		txt(c, label, 10, colMuted).LetterSpacing(1.0)
		countBadge(c, count)
		ui.Spacer(c)
		if staged {
			all := cmdButton(c, iconMinus, 14, false).Tooltip("Unstage all")
			if all.Clicked() {
				a.changes = append(a.changes, a.staged...)
				a.staged = nil
				c.Toast("Unstaged all changes")
			}
		} else {
			stageAll := cmdButton(c, iconPlus, 14, false).Tooltip("Stage all")
			if stageAll.Clicked() {
				a.staged = append(a.staged, a.changes...)
				a.changes = nil
				c.Toast("Staged all changes")
			}
			discardAll := cmdButton(c, iconUndo, 14, false).Tooltip("Discard all")
			if discardAll.Clicked() {
				a.changes = nil
				c.Toast("Discarded all unstaged changes")
			}
		}
		cmdButton(c, iconEllipsis, 14, false)
	})
	return e
}

func (a *appState) gitFileRow(c *ui.Context, f *gitFile, staged bool) ui.Element {
	e := ui.ButtonBase(c).Key(f.Name).FillWidth().Height(40).Padding(0, 12).ClipX()
	hover := e.Hovered()
	if hover {
		e.Background(ui.Hex("#171d25"))
	}
	e.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			fileBadge(c, f.Name)
			ui.Column(c).Gap(1).MinWidth(0).Grow(1).Children(func() {
				txt(c, f.Name, 11, colText)
				txt(c, f.Dir, 9, colMuted)
			})
			// The mock's .git-actions stay in layout but are transparent
			// until the row is hovered.
			actions := ui.Row(c).Gap(2).Shrink(0)
			if !hover {
				actions.Opacity(0)
			}
			actions.Children(func() {
				if staged {
					un := cmdButton(c, iconMinus, 14, false).Tooltip("Unstage")
					if un.Clicked() {
						a.unstage(f.Name)
					}
					cmdButton(c, iconOpenInNew, 14, false)
				} else {
					st := cmdButton(c, iconPlus, 14, false).Tooltip("Stage")
					if st.Clicked() {
						a.stage(f.Name)
					}
					// Untracked files discard with a trash icon, as the mock.
					discardIcon := iconUndo
					if f.Status == "U" || f.Status == "?" {
						discardIcon = iconTrash
					}
					di := cmdButton(c, discardIcon, 14, false).Tooltip("Discard")
					if di.Clicked() {
						a.discard(f.Name)
					}
				}
			})
			txt(c, f.Status, 10, statusColor(f.Status))
		})
	})
	if e.Clicked() {
		a.diffStaged = staged
		a.activeView = tabDiff
	}
	return e
}

func (a *appState) stage(name string) {
	for i, f := range a.changes {
		if f.Name == name {
			a.changes = append(a.changes[:i], a.changes[i+1:]...)
			a.staged = append(a.staged, f)
			return
		}
	}
}

func (a *appState) unstage(name string) {
	for i, f := range a.staged {
		if f.Name == name {
			a.staged = append(a.staged[:i], a.staged[i+1:]...)
			a.changes = append(a.changes, f)
			return
		}
	}
}

func (a *appState) discard(name string) {
	for i, f := range a.changes {
		if f.Name == name {
			a.changes = append(a.changes[:i], a.changes[i+1:]...)
			return
		}
	}
}
