package main

import (
	"context"

	"github.com/egoist/mygo/ui"

	"oterm/service"
)

// Git graph history: lane layout from git log, rendered with Painter
// primitives, embedded in the Git panel or full-pane.

// laneColors cycles through the theme's accent colors.
var laneColors = []ui.Color{
	colBlue, colGreen, colPurple, colYellow, colCyan, colOrange, colLime, colRed,
}

const (
	laneWidth  = 14.0
	graphRowH  = 30.0
	nodeRadius = 4.0
)

// graphLane is one row's lane assignment: the lane the commit node sits on,
// plus the edges to draw vertically through this row.
type graphLane struct {
	NodeLane int
	MaxLane  int
	Edges    []graphEdge
	// FromAbove and Continues say whether the node's lane is joined to the
	// row above and to the row below, so the line reads as continuous
	// rather than as a stub inside each row.
	FromAbove bool
	Continues bool
}

type graphEdge struct {
	FromLane, ToLane int
	Color            ui.Color
	Kind             edgeKind
	// NewLane marks an edge into a lane this row allocated: nothing above
	// painted it, so it must draw its own descent.
	NewLane bool
}

type edgeKind int

const (
	edgeStraight edgeKind = iota // vertical continuation
	edgeMerge                    // node -> parent lane
	edgeFork                     // parent lane -> child (bottom half)
)

// computeLanes assigns each commit a lane and computes the edges of each
// row. commits must be newest-first, each with Parents.
func computeLanes(commits []service.Commit) []graphLane {
	lanes := make([]graphLane, len(commits))
	// active maps a commit hash we're still looking for -> its lane.
	active := map[string]int{}
	free := []int{}
	nextLane := 0
	maxLane := 0

	alloc := func() int {
		if len(free) > 0 {
			l := free[len(free)-1]
			free = free[:len(free)-1]
			return l
		}
		l := nextLane
		nextLane++
		if l > maxLane {
			maxLane = l
		}
		return l
	}

	for i, cm := range commits {
		gl := graphLane{}
		// The lane this commit occupies: the lane of a child expecting it,
		// else a fresh lane.
		lane, expected := active[cm.Hash]
		if !expected {
			lane = alloc()
		} else {
			delete(active, cm.Hash)
		}
		gl.NodeLane = lane
		gl.FromAbove = expected
		nodeCol := laneColors[lane%len(laneColors)]

		// Continue other lanes straight through this row.
		for h, l := range active {
			gl.Edges = append(gl.Edges, graphEdge{
				FromLane: l, ToLane: l,
				Color: laneColors[l%len(laneColors)],
				Kind:  edgeStraight,
			})
			_ = h
		}

		// Connect the node to its parents.
		for pi, p := range cm.Parents {
			if pl, ok := active[p]; ok {
				// Parent already has a lane (merge into an existing line).
				gl.Edges = append(gl.Edges, graphEdge{
					FromLane: lane, ToLane: pl,
					Color: nodeCol, Kind: edgeMerge,
				})
			} else if pi == 0 {
				// First parent keeps the node's lane.
				active[p] = lane
				gl.Continues = true
			} else {
				// Extra parents of a merge get their own lane.
				pl := alloc()
				active[p] = pl
				gl.Edges = append(gl.Edges, graphEdge{
					FromLane: lane, ToLane: pl,
					Color: nodeCol, Kind: edgeMerge, NewLane: true,
				})
			}
		}
		if len(cm.Parents) == 0 {
			// Root commit: free its lane.
			free = append(free, lane)
		}
		gl.MaxLane = maxLane
		lanes[i] = gl
	}
	return lanes
}

// refreshGraph loads the commit log of the active repo.
func (a *appState) refreshGraph() {
	r := a.activeRepoRef()
	if r == nil || a.win == nil {
		return
	}
	root := r.Root
	limit := a.graphLimit
	win := a.win
	a.graphJob.Run(func(ctx context.Context) error {
		commits, err := service.Log(root, limit)
		if err != nil {
			return err
		}
		win.Update(func() { a.commits = commits })
		return nil
	}, nil)
}

// openGraphTab activates/creates the full-pane graph tab.
func (a *appState) openGraphTab() {
	for i, t := range a.tabs {
		if t.Kind == tabGraph {
			a.activeTab = i
			a.activeView = tabGraph
			a.refreshGraph()
			return
		}
	}
	a.tabs = append(a.tabs, topTab{Kind: tabGraph, Label: "Git Graph", Icon: fileIconGraph})
	a.activeTab = len(a.tabs) - 1
	a.activeView = tabGraph
	a.refreshGraph()
}

// graphView is the full-pane graph with a commit list.
func (a *appState) graphView(c *ui.Context) ui.Element {
	return ui.Column(c).Fill().Background(colCodeBg).Children(func() {
		ui.Row(c).Height(40).AlignItems(ui.Center).Padding(0, 12).Gap(8).Background(colPanel).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
			ui.Icon(c, iconGitGraph).Width(16).Height(16).TextColor(colPurple)
			ui.Text(c, "Git Graph").FontSize(12)
			if r := a.activeRepoRef(); r != nil {
				ui.Text(c, r.Name+" · "+r.Branch).FontSize(10).TextColor(colMuted)
			}
			ui.Spacer(c)
			if cmdButton(c, iconRefresh, iconBtn, false).Tooltip("Reload history").Clicked() {
				a.refreshGraph()
			}
		})

		if len(a.commits) == 0 {
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, "No commits").FontSize(12).TextColor(colMuted)
			})
			return
		}

		lanes := computeLanes(a.commits)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(6, 0).Children(func() {
				for i := range a.commits {
					a.graphRow(c, &a.commits[i], lanes[i])
				}
			})
		})
	})
}

// graphCompactView renders the same rows sized for the inspector panel.
func (a *appState) graphCompactView(c *ui.Context) ui.Element {
	if len(a.commits) == 0 {
		return ui.Column(c).Padding(12).Children(func() {
			ui.Text(c, "No history").FontSize(11).TextColor(colMuted)
		})
	}
	lanes := computeLanes(a.commits)
	return ui.Column(c).Children(func() {
		for i := range a.commits {
			a.graphRowCompact(c, &a.commits[i], lanes[i])
		}
	})
}

func (a *appState) graphRow(c *ui.Context, cm *service.Commit, lane graphLane) ui.Element {
	e := ui.Row(c).Key(cm.Hash).FillWidth().Height(graphRowH).AlignItems(ui.Center).Gap(10).Cursor(ui.CursorPointer).Padding(0, 12)
	if e.Hovered() {
		e.Background(ui.Hex("#171d25"))
	}
	e.Children(func() {
		a.graphCanvas(c, lane, graphRowH)
		ui.Text(c, cm.Short).Font(monoFont).FontSize(11).TextColor(colMuted2).Width(58).SingleLine().Shrink(0)
		ui.Text(c, cm.Subject).FontSize(12).TextColor(colText2).SingleLine().Grow(1).MinWidth(0)
		for _, ref := range cm.Refs {
			pill(c, ref)
		}
		ui.Text(c, cm.Author).FontSize(10).TextColor(colMuted).Width(120).SingleLine().Shrink(0)
		ui.Text(c, relativeTime(cm.Time)).FontSize(10).TextColor(colMuted2).Width(56).SingleLine().Shrink(0)
	})
	if e.Clicked() {
		if a.expandedCommit == cm.Hash {
			a.expandedCommit = ""
		} else {
			a.expandedCommit = cm.Hash
		}
	}
	// Show modified files when expanded.
	if a.expandedCommit == cm.Hash {
		a.commitFileList(c, cm)
	}
	return e
}

func (a *appState) graphRowCompact(c *ui.Context, cm *service.Commit, lane graphLane) ui.Element {
	expanded := a.expandedCommit == cm.Hash
	e := ui.Row(c).Key(cm.Hash).FillWidth().Height(26).AlignItems(ui.Center).Gap(8).Cursor(ui.CursorPointer).Padding(0, 10)
	if e.Hovered() {
		e.Background(ui.Hex("#171d25"))
	}
	e.Children(func() {
		a.graphCanvas(c, lane, 26)
		ui.Text(c, cm.Subject).FontSize(11).TextColor(colText2).SingleLine().Grow(1).MinWidth(0)
		ui.Text(c, relativeTime(cm.Time)).FontSize(9).TextColor(colMuted2).SingleLine().Shrink(0)
	})
	if e.Clicked() {
		if expanded {
			a.expandedCommit = ""
		} else {
			a.expandedCommit = cm.Hash
		}
	}
	// Show modified files when expanded.
	if expanded {
		a.commitFileList(c, cm)
	}
	return e
}

// commitFileList renders the files of an expanded commit as a VS Code-like
// "Files Changed" list: a status letter, a file badge, the name and its
// directory, each in a fixed column so rows line up.
func (a *appState) commitFileList(c *ui.Context, cm *service.Commit) {
	r := a.activeRepoRef()
	if r == nil {
		return
	}
	files, err := service.CommitFiles(r.Root, cm.Hash)
	if err != nil || len(files) == 0 {
		return
	}
	add, del, _ := service.CommitStats(r.Root, cm.Hash)

	ui.Column(c).Padding(0, 0, 4, 22).Children(func() {
		// Header: how much changed.
		ui.Row(c).Height(22).PaddingStart(26).PaddingEnd(10).Gap(8).AlignItems(ui.Center).Children(func() {
			n := len(files)
			word := "files changed"
			if n == 1 {
				word = "file changed"
			}
			txt(c, itoa(n)+" "+word, 9, colMuted2)
			if add > 0 || del > 0 {
				ui.Spacer(c)
				txt(c, "+"+itoa(add), 9, colGreen)
				txt(c, "-"+itoa(del), 9, colRed)
			}
		})
		for _, f := range files {
			a.commitFileRow(c, cm.Hash, f)
		}
	})
}

// commitFileRow is one file of an expanded commit.
func (a *appState) commitFileRow(c *ui.Context, hash string, f service.CommitFile) {
	row := ui.Row(c).Key(hash + ":" + f.Path).FillWidth().Height(24).
		PaddingStart(26).PaddingEnd(10).Gap(6).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).ClipX()
	if row.Hovered() {
		row.Background(ui.Hex("#171d25"))
	}
	row.Children(func() {
		// Status column first, fixed width, so names never shift.
		ui.Box(c).Width(12).Shrink(0).Children(func() {
			txt(c, f.Status, 10, statusColor(f.Status)).FontWeight(700)
		})
		fileBadge(c, pathBase(f.Path))
		ui.Text(c, pathBase(f.Path)).FontSize(11).TextColor(colText2).SingleLine().Grow(1).MinWidth(0)
		if dir := pathDir(f.Path); dir != "" {
			txt(c, truncTxtPath(dir, 28), 9, colMuted2)
		}
	})
	if row.Clicked() {
		a.openCommitFileDiff(hash, f.Path)
	}
}

// truncTxtPath shortens a directory path to max characters, keeping its
// tail, which is the part that identifies it.
func truncTxtPath(s string, max int) string {
	if len(s) <= max || max < 2 {
		return s
	}
	return "…" + s[len(s)-(max-1):]
}

// graphCanvas draws the lanes of one row.
func (a *appState) graphCanvas(c *ui.Context, lane graphLane, rowH float32) ui.Element {
	w := float32(lane.MaxLane+1) * laneWidth
	if w < laneWidth {
		w = laneWidth
	}
	return ui.Box(c).Width(w).Height(rowH).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		laneX := func(l int) float32 { return r.X + float32(l)*laneWidth + laneWidth/2 }
		midY := r.Y + r.H/2

		for _, e := range lane.Edges {
			switch e.Kind {
			case edgeStraight:
				// Full-height vertical line.
				p.Fill(ui.Rect{X: laneX(e.FromLane) - 1, Y: r.Y, W: 2, H: r.H}, e.Color, 0)
			case edgeMerge:
				// Vertical from the node down to midY, a step to the target
				// lane, then down. The descent is only painted for a lane
				// this row created; an existing lane was already painted
				// full-height by the straight pass.
				x1, x2 := laneX(e.FromLane), laneX(e.ToLane)
				if x1 == x2 {
					if e.NewLane {
						p.Fill(ui.Rect{X: x1 - 1, Y: midY, W: 2, H: r.H - (midY - r.Y)}, e.Color, 0)
					}
					break
				}
				xLo, xHi := x1, x2
				if xLo > xHi {
					xLo, xHi = xHi, xLo
				}
				p.Fill(ui.Rect{X: xLo, Y: midY - 1, W: xHi - xLo, H: 2}, e.Color, 0)
				if e.NewLane {
					// The new lane takes its own color from the row below,
					// so its descent must match or the line seams there.
					dc := laneColors[e.ToLane%len(laneColors)]
					p.Fill(ui.Rect{X: x2 - 1, Y: midY, W: 2, H: r.H - (midY - r.Y)}, dc, 0)
				}
			}
		}

		// Node: circle on the node's lane, joined to the rows above and
		// below when the lane runs on through them.
		nx := laneX(lane.NodeLane)
		nodeCol := laneColors[lane.NodeLane%len(laneColors)]
		if lane.FromAbove {
			p.Fill(ui.Rect{X: nx - 1, Y: r.Y, W: 2, H: r.H / 2}, nodeCol, 0)
		}
		if lane.Continues {
			p.Fill(ui.Rect{X: nx - 1, Y: midY, W: 2, H: r.H / 2}, nodeCol, 0)
		}
		p.Fill(ui.Rect{X: nx - nodeRadius, Y: midY - nodeRadius, W: nodeRadius * 2, H: nodeRadius * 2}, nodeCol, nodeRadius)
	})
}
