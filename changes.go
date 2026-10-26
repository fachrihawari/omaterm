package main

// The Git tab's Last turn view: what the focused pane's agent changed in
// its last turn, against a snapshot taken as the agent began working.
// Each file opens to its diff, whose changes can be kept or reverted, one
// at a time. It follows the focused pane, into a worktree too.

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"omaterm/internal/gitsnap"
	"omaterm/internal/session"
	"omaterm/service"
)

// changeSet is the Last turn view's state.
type changeSet struct {
	// lastTurn is the Git tab's switch: the turn's changes, not all.
	lastTurn bool

	// What the list is of: the pane, its worktree, the turn's revision,
	// and its files.
	pane string
	root string
	// notRepo tells a pane outside any git repository.
	notRepo  bool
	rev      string
	files    []gitsnap.File
	err      string
	loading  bool
	loadedAt time.Time
	stamp    string
	key      string // pane, base and turn the list is of

	open map[string]*changeFile // by path
	job  service.Job
}

// changeFile is a file opened in the list: its texts, as a diff.
type changeFile struct {
	file    gitsnap.File
	text    gitsnap.Text
	ds      *editor.DiffState
	loading bool
	err     string
}

// changesPane is the pane the panel follows: the focused one.
func (a *appState) changesPane() (session.Info, bool) {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) || a.tabs[a.activeTab].Kind != tabTerminal {
		return session.Info{}, false
	}
	id := a.activeSplitLeaf()
	info, ok := a.status[id]
	if !ok || info.WorkDir() == "" {
		return session.Info{}, false
	}
	return info, true
}

// refreshChanges lists the files the turn changed, off the UI thread,
// when what the list is of changed or it is due.
func (a *appState) refreshChanges(force bool) {
	cs := &a.changes
	info, ok := a.changesPane()
	if !ok || info.Turn == nil {
		cs.files, cs.err, cs.pane = nil, "", ""
		return
	}
	turn := info.Turn.Tree
	key := info.ID + "|" + turn
	stamp := service.GitStamp(firstNonEmpty(cs.root, service.RepoRoot(info.WorkDir())))
	every := 30 * time.Second
	if info.Status == session.Working {
		every = 4 * time.Second
	}
	if !force && key == cs.key && stamp == cs.stamp && nowFunc().Sub(cs.loadedAt) < every {
		return
	}
	if key != cs.key {
		cs.open = map[string]*changeFile{}
	}
	cs.key, cs.stamp, cs.loading, cs.loadedAt = key, stamp, true, nowFunc()
	dir, pane := info.WorkDir(), info.ID
	cs.job.Run(func(job context.Context) error {
		ctx, cancel := context.WithTimeout(job, 20*time.Second)
		defer cancel()
		r, err := gitsnap.Open(ctx, dir)
		notRepo := err != nil
		var rev string
		var files []gitsnap.File
		if err == nil {
			if rev, _, err = r.Base(ctx, gitsnap.BaseTurn, turn); err == nil {
				files, err = r.Changes(ctx, rev, "")
			}
		}
		if job.Err() != nil {
			// A newer run took over: what this one failed with is its
			// cancelling ("not in a git repository", "signal: killed").
			return nil
		}
		a.post(func() {
			if cs.key != key {
				return
			}
			cs.loading, cs.pane = false, pane
			cs.err, cs.notRepo = "", notRepo
			if notRepo {
				cs.files = nil
				return
			}
			if err != nil {
				cs.err, cs.files = err.Error(), nil
				return
			}
			cs.root, cs.rev, cs.files = r.Root, rev, files
			for path, f := range cs.open {
				f.loading = false
				a.loadChangeFile(path, f.file)
			}
		})
		return nil
	}, nil)
}

// loadChangeFile reads a file's texts, off the UI thread, and makes its
// diff.
func (a *appState) loadChangeFile(path string, f gitsnap.File) {
	cs := &a.changes
	cf := cs.open[path]
	if cf == nil {
		cf = &changeFile{}
		cs.open[path] = cf
	}
	if cf.loading {
		return
	}
	cf.file, cf.loading = f, true
	root, rev, key := cs.root, cs.rev, cs.key
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := gitsnap.Open(ctx, root)
		var t gitsnap.Text
		if err == nil {
			t, err = r.FileText(ctx, rev, f.Path, f.OldPath)
		}
		a.post(func() {
			if cs.key != key || cs.open[path] != cf {
				return
			}
			cf.loading = false
			if err != nil {
				cf.err = err.Error()
				return
			}
			if cf.ds != nil && cf.text.Hash == t.Hash && cf.text.Old == t.Old {
				return // unchanged: the diff keeps its scroll and what was kept
			}
			cf.text, cf.err = t, ""
			cf.ds = &editor.DiffState{
				Old: editor.NewBuffer(t.Old), New: editor.NewBuffer(t.New),
				Layout: editor.DiffUnified, Inline: editor.DiffInlineWord,
				FitContent: true, Theme: omaEditorTheme(), Font: a.editorFont(a.fontSize - 1),
			}
			if t.Large {
				cf.ds = nil
			}
		})
	}()
}

// worktreeBusy tells whether a pane in the worktree has an agent at work,
// which a revert must not write under.
func (a *appState) worktreeBusy(root string) bool {
	for _, i := range a.status {
		if i.Status == session.Working && i.Agent != "" && within(i.WorkDir(), root) {
			return true
		}
	}
	return false
}

// revertHunk puts a change's old lines back in the file on disk, if the
// file is still what the diff shows.
func (a *appState) revertHunk(c *ui.Context, cf *changeFile, i int) {
	cs := &a.changes
	if a.worktreeBusy(cs.root) {
		toast(c, "An agent is working here: revert once it stops")
		return
	}
	cf.ds.Revert(i)
	text := cf.ds.New.String()
	r := gitsnap.Repo{Root: cs.root}
	if err := r.Write(cf.file.Path, cf.text.Hash, text); err != nil {
		toast(c, "Could not revert: "+err.Error())
		a.loadChangeFile(cf.file.Path, cf.file)
		return
	}
	cf.text.New, cf.text.Hash = text, gitsnap.Hash([]byte(text))
	a.refreshChanges(true)
}

// turnSwitch is the Git tab's switch between all changes and the last
// turn's, shown while the focused pane's agent has one.
func (a *appState) turnSwitch(c *ui.Context) {
	cs := &a.changes
	ui.Row(c.Key("turn-seg")).Radius(8).Padding(2).Background(pal.Chip).Shrink(0).Children(func() {
		for _, b := range []struct {
			turn  bool
			label string
			tip   string
		}{
			{false, "All changes", "Everything not committed"},
			{true, "Last turn", "What the agent changed in its last turn"},
		} {
			seg := ui.ButtonBase(c.Key(b.label)).Grow(1).Height(26).Radius(6).Center().Tooltip(b.tip)
			col := pal.TextMuted
			if cs.lastTurn == b.turn {
				raised(seg.Background(pal.Raised))
				col = pal.Text
			}
			seg.Children(func() {
				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Text(c, b.label).FontSize(12).FontWeight(500).TextColor(col).SingleLine()
					if b.turn && len(cs.files) > 0 {
						countBadge(c, len(cs.files))
					}
				})
			})
			if seg.Clicked() && cs.lastTurn != b.turn {
				cs.lastTurn = b.turn
				if b.turn {
					a.refreshChanges(true)
				}
			}
		}
	})
}

// turnChanges is the Last turn view: the files the turn changed, each
// opening to its diff.
func (a *appState) turnChanges(c *ui.Context, info session.Info) {
	cs := &a.changes
	if cs.open == nil {
		cs.open = map[string]*changeFile{}
	}
	a.refreshChanges(false)
	c.After(time.Second) // follow the files as they change
	ui.Column(c).Grow(1).MinHeight(0).Padding(0, 10).Gap(8).Children(func() {
		// The summary: how much changed, since when.
		added, removed := 0, 0
		for _, f := range cs.files {
			added += f.Added
			removed += f.Removed
		}
		ui.Row(c.Key("sum")).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, plural(len(cs.files), "file")).FontSize(12).TextColor(pal.TextMuted).SingleLine()
			if added > 0 {
				ui.Text(c, "+"+strconv.Itoa(added)).FontSize(12).TextColor(pal.Ok).SingleLine()
			}
			if removed > 0 {
				ui.Text(c, "−"+strconv.Itoa(removed)).FontSize(12).TextColor(pal.Bad).SingleLine()
			}
			since := "since turn " + ago(nowFunc().Sub(time.UnixMilli(info.Turn.At))) + " ago"
			ui.Text(c, "· "+since).FontSize(12).TextColor(pal.TextFaint).SingleLine().Shrink(1).MinWidth(0).Ellipsis("…")
			ui.Spacer(c)
			if cmdButton(c, iconRefresh, 13, false).Tooltip("Refresh").Clicked() {
				a.refreshChanges(true)
			}
		})
		if cs.notRepo {
			ui.Text(c, "This pane's folder is not in a git repository.").FontSize(12).TextColor(pal.TextMuted).Padding(8, 2)
			return
		}
		if cs.err != "" {
			ui.Text(c, cs.err).FontSize(12).TextColor(pal.Bad)
			return
		}
		if len(cs.files) == 0 && !cs.loading {
			ui.Text(c, "No changes.").FontSize(12).TextColor(pal.TextMuted).Padding(8, 2)
			return
		}
		ui.Scroll(c.Key("files")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Gap(2).Padding(0, 0, 10, 0).Children(func() {
				for _, f := range cs.files {
					a.changeRow(c, f)
				}
			})
		})
	})
}

// changeRow is a changed file: its status, path and line counts; opened,
// its diff.
func (a *appState) changeRow(c *ui.Context, f gitsnap.File) {
	cs := &a.changes
	cf := cs.open[f.Path]
	row := ui.Row(c.Key("f-"+f.Path)).Height(fileRowH).Padding(0, 6).Gap(8).Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if cf != nil || row.Hovered() {
		row.Background(pal.Hover)
	}
	letter := "M"
	switch f.Status {
	case "added":
		letter = "A"
	case "deleted":
		letter = "D"
	case "renamed":
		letter = "R"
	}
	row.Children(func() {
		fileLabel(c, f.Path)
		if f.Binary {
			ui.Text(c, "binary").FontSize(11).TextColor(pal.TextFaint)
		} else {
			if f.Added > 0 {
				ui.Text(c, "+"+strconv.Itoa(f.Added)).FontSize(11.5).TextColor(pal.Ok).SingleLine()
			}
			if f.Removed > 0 {
				ui.Text(c, "−"+strconv.Itoa(f.Removed)).FontSize(11.5).TextColor(pal.Bad).SingleLine()
			}
		}
		statusLetter(c, letter)
	})
	if row.Clicked() {
		if cf != nil {
			delete(cs.open, f.Path)
		} else {
			a.loadChangeFile(f.Path, f)
		}
		return
	}
	if cf == nil {
		return
	}
	switch {
	case cf.err != "":
		ui.Text(c, cf.err).FontSize(11.5).TextColor(pal.Bad).Padding(4, 8)
	case cf.text.Binary:
		ui.Text(c, "Binary file").FontSize(11.5).TextColor(pal.TextMuted).Padding(4, 8)
	case cf.text.Large:
		ui.Text(c, "Too large to show: omaterm diff prints it").FontSize(11.5).TextColor(pal.TextMuted).Padding(4, 8)
	case cf.ds != nil:
		ds := cf.ds
		ds.HunkActions = !a.worktreeBusy(cs.root)
		ds.RenderHunkActions = func(c *ui.Context, bar ui.Element, h *editor.DiffHunkActions) {
			bar.Row().Gap(4).Children(func() {
				keep := ui.ButtonBase(c.Key("keep")).Height(20).Padding(0, 8).Radius(5)
				face(keep, pal.Raised).Children(func() {
					ui.Text(c, "Keep").FontSize(11).TextColor(pal.Text)
				})
				if keep.Clicked() {
					h.Keep()
				}
				rev := ui.ButtonBase(c.Key("revert")).Height(20).Padding(0, 8).Radius(5).Background(pal.Accent)
				rev.Children(func() {
					ui.Text(c, "Revert").FontSize(11).TextColor(pal.AccentText)
				})
				if rev.Clicked() {
					a.revertHunk(c, cf, h.Index)
				}
			})
		}
		ui.Box(c.Key("d-"+f.Path)).Radius(8).Border(1, pal.Border).Clip().Margin(2, 0, 6, 0).Children(func() {
			editor.DiffView(c, ds).FillWidth()
		})
	default:
		ui.Text(c, "Loading…").FontSize(11.5).TextColor(pal.TextFaint).Padding(4, 8)
	}
}

// turnSnapshots takes a snapshot of a pane's worktree as its agent starts
// a turn (from idle or done, not from waiting, which goes on with the
// same turn), so the turn's changes can be shown and reverted. One runs
// at a time a worktree.
type turnSnapshots struct {
	mu      sync.Mutex
	running map[string]bool
}

func (a *appState) onStatus(info session.Info, prev session.Status) {
	if info.Agent == "" || info.Status != session.Working || prev == session.Waiting || prev == session.Working {
		return
	}
	if !a.gitSnapshots.Load() {
		return
	}
	dir := info.WorkDir()
	at := time.Now()
	s := &a.snaps
	s.mu.Lock()
	if s.running == nil {
		s.running = map[string]bool{}
	}
	if s.running[dir] {
		s.mu.Unlock()
		return
	}
	s.running[dir] = true
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.running, dir)
			s.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		r, err := gitsnap.Open(ctx, dir)
		if err != nil {
			return
		}
		tree, err := r.Snapshot(ctx)
		if err != nil {
			return
		}
		if p := a.panes.Get(info.ID); p != nil {
			p.SetTurn(&session.Turn{Tree: tree, Root: r.Root, At: at.UnixMilli()})
		}
	}()
}
