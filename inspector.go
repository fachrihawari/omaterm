package main

import (
	"context"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"omaterm/service"
)

// The right inspector: Info / Files / Git panels behind a tab strip.

func (a *appState) inspector(c *ui.Context) ui.Element {
	return ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(40).AlignItems(ui.End).Padding(0, 4).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
			a.inspectorTabButton(c, 0, iconInfo, "Info", 0)
			a.inspectorTabButton(c, 1, iconFolder, "Files", 0)
			a.inspectorTabButton(c, 2, iconGitBranch, "Git", a.gitCount())
			ui.Spacer(c)
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
	e := ui.ButtonBase(c).Height(32).Padding(0, 12).Gap(6).Shrink(0).Label(label)
	e.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			// The tab shown reads as chosen: its text in full and bold,
			// its icon in the accent, as its underline.
			col, iconCol := pal.TextMuted, pal.TextFaint
			if active {
				col, iconCol = pal.Text, pal.Accent
			} else if e.Hovered() {
				col = pal.TextSoft
			}
			ui.Icon(c, icon).Width(14).Height(14).TextColor(iconCol)
			t := ui.Text(c, label).FontSize(12.5).TextColor(col).SingleLine().Shrink(0)
			if active {
				t.FontWeight(600)
			}
			if badge > 0 {
				countBadge(c, badge)
			}
		})
	})
	if active {
		e.BorderWidth(0, 0, 2, 0).BorderColor(pal.Accent)
	}
	if e.Clicked() {
		a.showInspectorTab(idx)
	}
	return e
}

// showInspectorTab switches the inspector to Info (0), Files (1) or Git
// (2), loading what that tab shows.
func (a *appState) showInspectorTab(idx int) {
	if !a.inspectorShown {
		a.setInspector(true)
	}
	wasTab := a.inspectorTab
	a.inspectorTab = idx
	if idx == 0 {
		a.startProcessRefresh()
	} else {
		a.stopProcessRefresh()
	}
	// The files as they are now: one an agent made was missing until a
	// restart.
	if idx == 1 && wasTab != 1 {
		a.rebuildFileTree()
	}
	// Entering the Git tab re-reads status, so switching to it never
	// shows a stale tree from before the user left.
	if idx == 2 && wasTab != 2 {
		a.refreshActiveRepo()
	}
	// Load history once so the GIT GRAPH count is real, not a
	// misleading 0 that only fixes itself when the section opens.
	if idx == 2 && !a.graphLoaded {
		a.refreshGraph()
	}
}

// INFO

func (a *appState) infoPanel(c *ui.Context) ui.Element {
	return ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(12).Gap(16).Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				sectionLabel(c, "PROJECT")
				ui.Row(c).Radius(8).Border(1, pal.Border).Background(pal.Raised).Padding(12).Gap(8).AlignItems(ui.Center).ClipX().Children(func() {
					ui.Box(c).Width(28).Height(28).Radius(6).Border(1, pal.BorderStrong).Background(pal.Input).Shrink(0).Children(func() {
						ui.Column(c).Fill().Center().Children(func() {
							ui.Icon(c, iconFolderGit).Width(16).Height(16).TextColor(pal.Accent)
						})
					})
					ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
						if p := a.project(); p != nil {
							// The branch chip shares the name's line, so a
							// long branch can only squeeze the name, never
							// the path below it.
							ui.Row(c).Gap(6).AlignItems(ui.Center).ClipX().Children(func() {
								// The name keeps a few letters; the branch
								// takes the rest of the room before it
								// ellipsizes, not just under half the line.
								ui.Text(c, p.Name).FontSize(12).TextColor(pal.Text).
									SingleLine().Ellipsis("…").Shrink(0.25).MinWidth(min(96, float32(len(p.Name))*8))
								ui.Spacer(c)
								if p.Branch != "" {
									pillShrink(c, p.Branch).Shrink(1).MinWidth(0).MaxWidthPercent(100)
								}
							})
							ui.Text(c, p.Path).FontSize(10).TextColor(pal.TextMuted).
								SingleLine().Ellipsis("…").Grow(1).MinWidth(0).Tooltip(p.Path)
						} else {
							txt(c, "No project open", 12, pal.Text)
							txt(c, keyLabels("app.openProject")+" to open a folder", 10, pal.TextMuted)
						}
					})
				})
			})

			// RUNNING and PORTS: with no project open there are none to list.
			// RUNNING is what the project's terminals run, as the system
			// sees it; the sidebar's Processes are the ones the project
			// defines, so the two never share a name.
			if a.project() != nil {
				ui.Column(c).Gap(8).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Children(func() {
						sectionLabel(c, "RUNNING")
						ui.Spacer(c)
						countBadge(c, len(a.processes))
						if cmdButton(c, iconRefresh, iconBtn, false).Tooltip("Refresh").Clicked() {
							a.refreshProcesses()
						}
					})
					if a.processErr != "" {
						ui.Text(c, a.processErr).FontSize(10).TextColor(pal.Bad).Padding(4, 0)
					}
					ui.Column(c).Radius(8).Border(1, pal.Border).Children(func() {
						if a.processes == nil {
							ui.Box(c).Padding(16).Children(func() {
								ui.Text(c, "Loading…").FontSize(11).TextColor(pal.TextMuted)
							})
						} else if len(a.processes) == 0 {
							ui.Box(c).Padding(20).Children(func() {
								ui.Column(c).AlignItems(ui.Center).Gap(4).Children(func() {
									ui.Icon(c, iconTerminal).Width(20).Height(20).TextColor(pal.TextFaint)
									if a.processWarn != "" {
										// The home folder: every process is in it.
										ui.Text(c, "Not listed for this folder").FontSize(11).TextColor(pal.TextMuted)
										ui.Text(c, "Open a project folder to see what runs in it").FontSize(10).TextColor(pal.TextFaint)
										return
									}
									ui.Text(c, "Nothing running in this project's terminals").FontSize(11).TextColor(pal.TextMuted)
									ui.Text(c, "Commands you run in a terminal show here").FontSize(10).TextColor(pal.TextFaint)
								})
							})
						}
						for i, p := range a.processes {
							row := ui.Row(c).Padding(10, 12).Gap(8).AlignItems(ui.Center).ClipX()
							if i > 0 {
								row = row.BorderWidth(1, 0, 0, 0).BorderColor(pal.Border)
							}
							proc := p
							row.Children(func() {
								dot(c, dotGreen, 6)
								// A long name gives way, so the row's numbers
								// and its kill button stay in it.
								ui.Text(c, proc.Name).FontSize(11).TextColor(pal.Text).SingleLine().Ellipsis("…").
									Shrink(1).MinWidth(min(48, float32(len(proc.Name))*7)).Tooltip(proc.Name)
								mutedTxt2(c, itoa(proc.Pid), 11).Shrink(2).MinWidth(0).Ellipsis("…")
								// Started by one of the project's processes:
								// named for it.
								if owner := a.processOfRoot(proc.Root); owner != "" {
									pill(c, owner)
								}
								ui.Spacer(c)
								mutedTxt2(c, proc.CPU+" · "+proc.Mem, 11)
								if proc.Pid != 1 && proc.Pid != os.Getpid() {
									if cmdButton(c, iconTrash, iconBtn, false).Tooltip("Kill process").Clicked() {
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
						if cmdButton(c, iconRefresh, iconBtn, false).Tooltip("Refresh").Clicked() {
							a.refreshProcesses()
						}
					})
					ui.Column(c).Radius(8).Border(1, pal.Border).Children(func() {
						if a.processes == nil {
							ui.Box(c).Padding(16).Children(func() {
								ui.Text(c, "Loading…").FontSize(11).TextColor(pal.TextMuted)
							})
						} else if len(a.ports) == 0 {
							ui.Box(c).Padding(20).Children(func() {
								ui.Column(c).AlignItems(ui.Center).Gap(4).Children(func() {
									ui.Icon(c, iconGlobe).Width(20).Height(20).TextColor(pal.TextFaint)
									ui.Text(c, "No listening ports").FontSize(11).TextColor(pal.TextMuted)
									ui.Text(c, "Start a dev server to see its port here").FontSize(10).TextColor(pal.TextFaint)
								})
							})
						}
						for i, p := range a.ports {
							row := ui.Row(c).Padding(10, 12).Gap(8).AlignItems(ui.Center).ClipX()
							if i > 0 {
								row = row.BorderWidth(1, 0, 0, 0).BorderColor(pal.Border)
							}
							port := p
							row.Children(func() {
								ui.Icon(c, iconGlobe).Width(14).Height(14).TextColor(pal.Accent)
								txt(c, itoa(port.Port), 11, pal.Text)
								mutedTxt2(c, port.Name, 11)
								ui.Spacer(c)
								if proc, ok := a.processOnPortIn(port.Port); ok && proc.Link != "" {
									// Served at its Dev URL: the row names that, and
									// opens it.
									pill(c, procLinkHost(proc.Link)).Tooltip("Dev URL of the process " + proc.Name)
									if port.Open {
										if cmdButton(c, iconExternalLink, iconBtn, false).Label("Open " + proc.Link).
											Tooltip("Open " + proc.Link).Clicked() {
											a.openURL(proc.Link)
										}
									}
								} else {
									if ok {
										pill(c, proc.Name).Tooltip("Port of the process " + proc.Name)
									}
									if port.Open {
										// It only said the port; it opens its page now, where
										// links open (Settings: a browser pane, or yours).
										url := "http://localhost:" + itoa(port.Port)
										if cmdButton(c, iconExternalLink, iconBtn, false).Label("Open " + url).
											Tooltip("Open " + url).Clicked() {
											a.openURL(url)
										}
									}
								}
							})
						}
					})
				})
			}

			a.updateSection(c)
		})
	})
}

// updateSection is the app's own way to check for updates: the menu item of
// the updater plugin only exists in a menu bar, which a Linux window has
// none of. It also says when this build cannot update itself, as a
// development build cannot.
func (a *appState) updateSection(c *ui.Context) {
	ui.Column(c).Gap(8).Children(func() {
		sectionLabel(c, "UPDATES")
		ui.Row(c).Radius(8).Border(1, pal.Border).Background(pal.Raised).Padding(12).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconRefresh).Width(14).Height(14).TextColor(pal.TextMuted)
			ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
				label := "omaterm"
				if v := mygo.App.Version(); v != "" {
					label += " " + v
				}
				txt(c, label, 11, pal.TextSoft)
				// A build that cannot update itself, such as a development
				// build or one a package manager installed, says so here
				// rather than only in the window after a click.
				if updatesReady && !mygo.Updater.Enabled() {
					ui.Text(c, "This build cannot update itself").FontSize(10).
						TextColor(pal.TextMuted).SingleLine().Ellipsis("…")
				}
			})
			b := ui.ButtonBase(c).Radius(6).Border(1, pal.BorderStrong).Padding(4, 10).Shrink(0)
			if b.Hovered() {
				b.Background(pal.Hover)
			}
			b.Children(func() {
				ui.Text(c, "Check").FontSize(10).TextColor(pal.TextSoft)
			})
			if b.Clicked() {
				a.checkForUpdates(c)
			}
		})
	})
}

// FILES

func (a *appState) filesPanel(c *ui.Context) ui.Element {
	return ui.Column(c).Grow(1).Children(func() {
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
	e := ui.Row(c).Key(n.Path).FillWidth().Justify(ui.Start).Focusable().Cursor(ui.CursorPointer).Height(28).Padding(0, 8).PaddingStart(8 + 18*float32(depth)).Gap(4)
	if e.Hovered() {
		e.Background(pal.Hover)
	}
	e.Children(func() {
		row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4)
		if n.Muted {
			row.Opacity(0.5)
		}
		row.Children(func() {
			// Disclosure column: always present, so folder and file
			// badges line up on one grid at every depth.
			if n.Dir {
				ci := iconChevronRight
				if n.Open {
					ci = iconChevronDown
				}
				ui.Icon(c, ci).Width(12).Height(12).TextColor(pal.TextFaint)
			} else {
				ui.Box(c).Width(12)
			}
			if n.Dir {
				fi := iconFolder
				if n.Open {
					fi = iconFolderOpen
				}
				ui.Icon(c, fi).Width(18).Height(18).TextColor(pal.Syn.Folder)
			} else {
				fileBadge(c, n.Name)
			}
			ui.Text(c, n.Name).FontSize(11).TextColor(pal.TextSoft).SingleLine()
			ui.Spacer(c)
			if n.Status != "" {
				ui.Text(c, n.Status).FontSize(10).TextColor(statusColor(n.Status))
			}
		})
	})
	if e.Clicked() {
		if n.Dir {
			n.Open = !n.Open
			if n.Open && !n.Loaded {
				loadFileNode(n)
			}
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

// fileBadge renders the little "TS"/"{ }" emblem or a file icon. Every
// branch is 18 wide so badges line up across rows and panels.
func fileBadge(c *ui.Context, name string) ui.Element { return fileBadgeOn(c, name, false) }

// fileBadgeOn is fileBadge on a list row, in the accent's text color on
// the row the list shows chosen.
func fileBadgeOn(c *ui.Context, name string, selected bool) ui.Element {
	col := func(cl ui.Color) ui.Color { return ink(selected, cl, false) }
	switch {
	case hasSuffix(name, ".tsx") || hasSuffix(name, ".ts"):
		return ui.Text(c, "TS").FontSize(10).FontWeight(700).TextColor(col(pal.Syn.FileTS)).Width(18)
	case hasSuffix(name, ".go"):
		return ui.Text(c, "GO").FontSize(10).FontWeight(700).TextColor(col(pal.ANSI[6])).Width(18)
	case hasSuffix(name, ".json"):
		return ui.Text(c, "{ }").FontSize(10).FontWeight(700).TextColor(col(pal.Syn.FileJSON)).Width(18)
	case hasSuffix(name, ".md"):
		return ui.Icon(c, iconFileText).Width(18).Height(18).TextColor(col(pal.Accent))
	case isImagePath(name):
		return ui.Icon(c, iconImage).Width(18).Height(18).TextColor(col(pal.Ok))
	default:
		return ui.Icon(c, iconFileText).Width(18).Height(18).TextColor(col(pal.TextMuted))
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

		// While the focused pane's agent has a turn, or its session a
		// worktree, a switch shows the turn's changes alone, or the
		// branch's.
		if info, ok := a.changesPane(); ok && (info.Turn != nil || a.focusedWorktree() != nil) {
			ui.Box(c).Padding(10, 10, 8, 10).Shrink(0).Children(func() {
				a.turnSwitch(c, info)
			})
			if a.changesView(info) != changesAll {
				a.turnChanges(c, info)
				return
			}
		}

		r := a.activeRepoRef()
		if r == nil {
			ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
				ui.Icon(c, iconGitBranch).Width(22).Height(22).TextColor(pal.TextFaint)
				ui.Text(c, "No git repository found").FontSize(12).TextColor(pal.TextMuted)
				if a.repoErr != "" {
					ui.Text(c, a.repoErr).FontSize(10).TextColor(pal.TextFaint)
				}
				b := ui.ButtonBase(c).Radius(6).Border(1, pal.BorderStrong).Padding(5, 12).Children(func() {
					ui.Text(c, "Search again").FontSize(11).TextColor(pal.TextSoft)
				})
				if b.Clicked() {
					a.refreshRepos()
				}
			})
			return
		}

		// Branch + commit box.
		ui.Column(c).Padding(12).Gap(10).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
			// The branch the repo is on, as git last said: a cached one
			// stayed "main" after a switch in a terminal, and the picker
			// then wouldn't switch back.
			if r != nil {
				if r.Branch != "" {
					a.currentBranch = r.Branch
				}
				if a.branchesRepo != r.Root {
					a.loadBranches(r.Root)
				}
			}
			ui.Row(c).Height(24).AlignItems(ui.Center).Gap(8).ClipX().Children(func() {
				ui.Icon(c, iconGitBranch).Width(16).Height(16).TextColor(pal.ANSI[5])
				// The branch trigger is the only child allowed to give up
				// width: a long name ellipsizes instead of pushing the
				// remote actions past the panel's edge.
				trigger := ui.ButtonBase(c).Radius(4).Padding(2, 6).Border(1, pal.BorderStrong).Shrink(1).MinWidth(0).Children(func() {
					ui.Text(c, a.currentBranch).FontSize(11).TextColor(pal.Text).
						SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
				})
				trigger.Tooltip(a.currentBranch)
				if trigger.Clicked() {
					a.openBranchOverlay()
				}
				pill(c, "↑"+itoa(r.Ahead)+" ↓"+itoa(r.Behind))
				ui.Spacer(c)
				remote := r.HasRemote
				if b := cmdButton(c, iconDownload, iconBtn, false).Tooltip("Fetch").Label("Fetch"); !remote {
					b.Disabled(true) // painted at half opacity
				} else if b.Clicked() {
					a.gitFetch(c)
				}
				if b := cmdButton(c, iconArrowDown, iconBtn, false).Tooltip("Pull").Label("Pull"); !remote {
					b.Disabled(true) // painted at half opacity
				} else if b.Clicked() {
					a.gitPull(c)
				}
				if b := cmdButton(c, iconArrowUp, iconBtn, false).Tooltip("Push").Label("Push"); !remote {
					b.Disabled(true) // painted at half opacity
				} else if b.Clicked() {
					a.gitPush(c)
				}
			})
			ui.Box(c).Radius(6).Border(1, pal.Border).Background(pal.Input).Children(func() {
				ui.TextAreaBase(c, &a.commitMsg).Placeholder("Commit message…").FontSize(11).Padding(8).MinHeight(56).FillWidth()
			})
			ui.Row(c).Gap(8).Children(func() {
				commit := ui.PrimaryButton(c, "Commit").Grow(1).Height(32).FontSize(11)
				ready := a.commitReady()
				if !ready {
					// Muted, not faded out (a disabled element paints at half
					// opacity, which left its label unreadable): it still
					// says what it is for, and does nothing until ready.
					commit.Background(pal.Chip).TextColor(pal.TextMuted).Cursor(ui.CursorDefault)
				}
				if commit.Clicked() && ready {
					a.gitCommit(c)
				}
				// Stashing lives in the STASHES section, where its message
				// field is; an icon here read as a second trash can.
			})
			if a.gitBusy != "" || a.gitMsg != "" {
				msg := a.gitMsg
				if a.gitBusy != "" {
					msg = a.gitBusy
				}
				col := pal.TextMuted
				if a.gitBusy == "" && a.gitErr {
					col = pal.Bad
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
		ui.Row(c).Padding(12).BorderWidth(1, 0, 0, 0).BorderColor(pal.Border).Children(func() {
			// Where the repository is in the project: its name when it is
			// the project's own folder, which "." would only hint at.
			where := r.RelPath
			if where == "" || where == "." {
				where = r.Name
			}
			ui.Text(c, where).FontSize(10).TextColor(pal.TextMuted).SingleLine()
			ui.Spacer(c)
			ui.Text(c, "HEAD "+r.Head).FontSize(10).TextColor(pal.TextMuted).SingleLine()
		})
	})
}

// repoSwitcher lets the user pick the active repo when several exist.
func (a *appState) repoSwitcher(c *ui.Context) {
	ui.Box(c).Padding(8, 12).BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
		ui.ScrollHorizontal(c).FillWidth().Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				for i := range a.repos {
					r := &a.repos[i]
					active := i == a.activeRepo
					label, icon := r.Name, iconFolderGit
					if a.isWorktreeRoot(r.Root) {
						label, icon = r.Branch, iconGitBranch
					}
					if n := len(r.Staged) + len(r.Changes); n > 0 {
						label += " ·" + itoa(n)
					}
					e := ui.ButtonBase(c).Key(r.Root).Height(26).Radius(13).Padding(0, 12).Shrink(0).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
							ui.Icon(c, icon).Width(12).Height(12).TextColor(pal.TextMuted)
							ui.Text(c, label).FontSize(10).SingleLine().Shrink(0)
						})
					})
					if active {
						e.Background(pal.Hover).Border(1, pal.BorderStrong)
					} else {
						e.Border(1, pal.Border)
					}
					if e.Clicked() && !active {
						a.selectRepo(i)
					}
				}
			})
		})
	})
}

// commitReady reports whether the Commit button can do anything: there
// are staged files, no remote operation is running, and a message exists.
func (a *appState) commitReady() bool {
	r := a.activeRepoRef()
	return r != nil && len(r.Staged) > 0 && a.gitBusy == "" &&
		strings.TrimSpace(a.commitMsg) != ""
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
	n := len(r.Staged)
	a.gitOp("Commit", func(root string) (string, error) { return "", service.CommitStaged(root, msg) }, func() {
		if a.commitMsg == msg {
			a.commitMsg = ""
		}
		a.gitMsg = "Committed " + plural(n, "file")
	})
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
		e = e.BorderWidth(1, 0, 0, 0).BorderColor(pal.Border).Margin(4, 0, 0, 0)
	}
	e.Children(func() {
		ui.Icon(c, icon).Width(14).Height(14).TextColor(pal.TextMuted)
		txt(c, label, 10, pal.TextMuted).LetterSpacing(1.0)
		countBadge(c, count)
		ui.Spacer(c)
		r := a.activeRepoRef()
		if staged {
			all := cmdButton(c, iconMinus, iconBtn, false).Tooltip("Unstage all")
			if all.Clicked() && r != nil {
				if err := service.UnstageAll(r.Root); err != nil {
					c.Toast("Git error: " + err.Error())
				} else {
					a.refreshActiveRepo()
				}
			}
		} else {
			stageAll := cmdButton(c, iconPlus, iconBtn, false).Tooltip("Stage all")
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
	e := ui.Row(c).Height(32).Padding(0, 8).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer).BorderWidth(1, 0, 0, 0).BorderColor(pal.Border).Margin(4, 0, 0, 0)
	e.Children(func() {
		ui.Icon(c, icon).Width(14).Height(14).TextColor(pal.TextMuted)
		txt(c, "GIT GRAPH", 10, pal.TextMuted).LetterSpacing(1.0)
		// No count until the history is in: a dash read as a broken badge.
		if a.graphLoaded {
			countBadge(c, len(a.commits))
		}
		ui.Spacer(c)
		if cmdButton(c, iconOpenInNew, iconBtn, false).Tooltip("Open in pane").Clicked() {
			a.openGraphTab()
		}
	})
	if e.Clicked() {
		a.gitGraphOpen = !a.gitGraphOpen
		if a.gitGraphOpen && !a.graphLoaded {
			a.refreshGraph()
		}
	}
	if a.gitGraphOpen {
		if !a.graphLoaded {
			ui.Text(c, "Loading…").FontSize(11).TextColor(pal.TextMuted).Padding(12)
		} else if len(a.commits) == 0 {
			ui.Text(c, "No commits").FontSize(11).TextColor(pal.TextMuted).Padding(12)
		} else {
			a.graphCompactView(c)
		}
	}
}

// fileRowH is the height of a changed file's row, in the Git tab and in
// Changes alike.
const fileRowH = 30

// fileLabel is a changed file as every list shows it: its badge, its
// name, and its folder faint after it on the same line.
func fileLabel(c *ui.Context, path string) {
	fileBadge(c, pathBase(path))
	ui.Row(c).Gap(6).Grow(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
		name := pathBase(path)
		ui.Text(c, name).FontSize(12).TextColor(pal.Text).SingleLine().Ellipsis("…").
			Shrink(0.5).MinWidth(min(60, float32(len(name))*7))
		if dir := pathDir(path); dir != "" {
			ui.Text(c, dir).FontSize(11).TextColor(pal.TextFaint).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		}
	})
}

// statusLetter is a changed file's git status, at the end of its row.
func statusLetter(c *ui.Context, letter string) {
	ui.Text(c, letter).FontSize(10.5).FontWeight(700).TextColor(statusColor(letter)).
		Width(12).TextAlign(ui.Center).SingleLine().Shrink(0)
}

func (a *appState) gitFileRow(c *ui.Context, f *service.GitFile, staged bool) ui.Element {
	e := ui.Row(c).Key(f.Path).FillWidth().Justify(ui.Start).Focusable().Cursor(ui.CursorPointer).Height(fileRowH).Padding(0, 12).ClipX().AlignItems(ui.Center).Gap(8)
	hover := e.Hovered()
	if hover {
		e.Background(pal.Hover)
	}
	e.Children(func() {
		fileLabel(c, f.Path)
		actions := ui.Row(c).Gap(2).Shrink(0)
		if !hover {
			actions.Opacity(0)
		}
		actions.Children(func() {
			r := a.activeRepoRef()
			if staged {
				un := cmdButton(c, iconMinus, iconBtn, false).Tooltip("Unstage")
				if un.Clicked() && r != nil {
					if err := service.Unstage(r.Root, f.Path); err != nil {
						c.Toast("Git error: " + err.Error())
					} else {
						a.refreshActiveRepo()
					}
				}
			} else {
				stageTip := "Stage"
				if f.Status == "!" {
					stageTip = "Mark Resolved" // staging a conflicted file resolves it
				}
				st := cmdButton(c, iconPlus, iconBtn, false).Tooltip(stageTip)
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
				// A conflict has no version to go back to: git refuses.
				if f.Status != "!" {
					di := cmdButton(c, discardIcon, iconBtn, false).Tooltip("Discard")
					if di.Clicked() && r != nil {
						a.askDiscard(r.Root, f.Path, f.Status == "U")
					}
				}
			}
		})
		statusLetter(c, f.Status)
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
	e := ui.Row(c).Height(32).Padding(0, 8).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer).BorderWidth(1, 0, 0, 0).BorderColor(pal.Border).Margin(4, 0, 0, 0)
	e.Children(func() {
		ui.Icon(c, icon).Width(14).Height(14).TextColor(pal.TextMuted)
		txt(c, "STASHES", 10, pal.TextMuted).LetterSpacing(1.0)
		countBadge(c, len(r.Stashes))
		ui.Spacer(c)
	})
	if e.Clicked() {
		a.stashCollapsed = !a.stashCollapsed
	}

	if !a.stashCollapsed {
		// Create-stash form.
		ui.Box(c).Padding(6, 12).Children(func() {
			ui.Row(c).Height(28).Radius(6).Border(1, pal.Border).Background(pal.Input).AlignItems(ui.Center).Padding(0, 8).Gap(6).Children(func() {
				ui.Icon(c, iconArchive).Width(12).Height(12).TextColor(pal.TextMuted)
				ui.TextInputBase(c, &a.stashMsg).Placeholder("Stash message…").FontSize(10).Grow(1)
				track := cmdButton(c, iconPlus, iconBtn, false).Tooltip("Stash (include untracked)")
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
		e.Background(pal.Hover)
	}
	e.Children(func() {
		ui.Icon(c, iconArchive).Width(14).Height(14).TextColor(pal.Warn)
		ui.Column(c).Gap(1).MinWidth(0).Grow(1).Children(func() {
			txt(c, s.Message, 11, pal.Text)
			meta := s.Ref
			if s.Branch != "" {
				meta += " · " + s.Branch
			}
			meta += " · " + relativeTime(s.When)
			txt(c, meta, 9, pal.TextMuted)
		})
		actions := ui.Row(c).Gap(2).Shrink(0)
		if !hover {
			actions.Opacity(0)
		}
		actions.Children(func() {
			// Each acts on the stash by its commit, not where the list
			// showed it: a drop or pop since moved the others up, and a
			// quick second click dropped the wrong one.
			hash := s.Hash
			apply := cmdButton(c, iconRotate, iconBtn, false).Tooltip("Apply")
			if apply.Clicked() {
				if i, ok := a.stashIndex(c, hash); ok {
					a.gitStashApply(c, i)
				}
			}
			pop := cmdButton(c, iconExternalLink, iconBtn, false).Tooltip("Pop")
			if pop.Clicked() {
				if i, ok := a.stashIndex(c, hash); ok {
					a.gitStashPop(c, i)
				}
			}
			drop := cmdButton(c, iconTrash, iconBtn, false).Tooltip("Drop")
			if drop.Clicked() {
				msg := s.Message
				a.askDanger("Drop Stash?", "“"+msg+"” is deleted for good.", "Drop", func() {
					if i, ok := a.stashIndex(nil, hash); ok {
						a.gitStashDrop(nil, i)
					}
				})
			}
		})
	})
	if e.Clicked() {
		a.openStashDiff(s.Index)
	}
}

// askDiscard discards a file's changes once the user confirms: a click
// on the icon next to Stage threw them away for good.
func (a *appState) askDiscard(root, path string, untracked bool) {
	text := "Your changes to " + path + " are lost for good."
	if untracked {
		text = path + " is not in git yet: it is deleted for good."
	}
	a.askDanger("Discard Changes?", text, "Discard", func() {
		if err := service.Discard(root, path, untracked); err != nil {
			a.toast(nil, "Git error: "+err.Error())
			return
		}
		a.refreshActiveRepo()
	})
}

// stashIndex is where the stash of commit hash is as git lists them
// now; false, told, when it is gone.
func (a *appState) stashIndex(c *ui.Context, hash string) (int, bool) {
	if r := a.activeRepoRef(); r != nil {
		stashes, _ := service.StashList(r.Root)
		for _, s := range stashes {
			if s.Hash == hash {
				return s.Index, true
			}
		}
	}
	a.toast(c, "That stash is gone")
	a.refreshActiveRepo()
	return 0, false
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
		a.toast(c, "Drop failed: "+err.Error())
		return
	}
	a.refreshActiveRepo()
	a.toast(c, "Dropped stash@{"+itoa(index)+"}")
}

// gitRemoteOp runs a network git command off the UI thread, showing its
// progress in the panel while it runs.
func (a *appState) gitRemoteOp(verb string, fn func(root string) (string, error)) {
	a.gitOp(verb, fn, nil)
}

// gitOp runs a git step off the UI thread, showing it under way and then
// its outcome in the Git panel; ok runs after it succeeds. Run on the UI
// thread, a commit froze the window, every terminal with it, for as long
// as the repo's pre-commit hooks ran.
func (a *appState) gitOp(verb string, fn func(root string) (string, error), ok func()) {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	root := r.Root
	if a.win == nil { // tests: at once
		out, err := fn(root)
		a.gitDone(verb, out, err, ok)
		return
	}
	win := a.win
	a.gitBusy = verb + "…"
	a.gitMsg = ""
	a.gitErr = false
	a.gitJob.Run(func(ctx context.Context) error {
		out, err := fn(root)
		win.Update(func() { a.gitDone(verb, out, err, ok) })
		return err
	}, nil)
}

// loadBranches lists root's branches off the UI thread: listed as the
// panel drew, a large repo held up the frame.
func (a *appState) loadBranches(root string) {
	a.branchesRepo = root
	if a.win == nil {
		a.branches, _ = service.ListBranches(root)
		return
	}
	win := a.win
	go func() {
		bs, err := service.ListBranches(root)
		win.Update(func() {
			if err == nil && a.branchesRepo == root {
				a.branches = bs
			}
		})
	}()
}

// gitDone shows how a git step went.
func (a *appState) gitDone(verb, out string, err error, ok func()) {
	msg := strings.TrimSpace(out)
	if err != nil && msg == "" {
		msg = strings.TrimSpace(err.Error())
	}
	switch {
	case err == nil:
		msg = verb + " done"
	case msg == "":
		msg = verb + " failed"
	default:
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:] // a hook's output ends with its verdict
		}
		msg = verb + " failed: " + msg
	}
	a.gitBusy = ""
	a.gitMsg = msg
	a.gitErr = err != nil
	if err == nil && ok != nil {
		ok()
	}
	if ok != nil {
		a.toast(nil, a.gitMsg) // a commit or checkout: told wherever the user is
	}
	a.refreshActiveRepo()
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
