package main

// The Changes panel: what a session's pane changed, against the start of
// its agent's last turn (a snapshot taken as the agent began working),
// HEAD, or where its branch left the default branch. Each file opens to
// its diff, whose changes can be kept or reverted, one at a time. It
// follows the focused pane.

import (
	"context"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"omaterm/internal/gitsnap"
	"omaterm/internal/session"
	"omaterm/service"
)

// changeSet is the Changes panel's state.
type changeSet struct {
	base string // gitsnap.BaseTurn, BaseHead or BaseBranch

	// What the list is of: the pane, its worktree, the base's revision
	// and name, and its files.
	pane string
	root string
	// notRepo tells a pane outside any git repository.
	notRepo  bool
	rev      string
	baseName string
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

// refreshChanges lists the files changed against the base, off the UI
// thread, when what the list is of changed or it is due.
func (a *appState) refreshChanges(force bool) {
	cs := &a.changes
	info, ok := a.changesPane()
	if !ok {
		cs.files, cs.err, cs.pane = nil, "", ""
		return
	}
	turn := ""
	if info.Turn != nil {
		turn = info.Turn.Tree
	}
	if cs.base == "" {
		cs.base = gitsnap.BaseHead
		if turn != "" {
			cs.base = gitsnap.BaseTurn
		}
	}
	key := info.ID + "|" + cs.base + "|" + turn
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
	dir, base, pane := info.WorkDir(), cs.base, info.ID
	cs.job.Run(func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		r, err := gitsnap.Open(ctx, dir)
		notRepo := err != nil
		var rev, name string
		var files []gitsnap.File
		if err == nil {
			if rev, name, err = r.Base(ctx, base, turn); err == nil {
				files, err = r.Changes(ctx, rev, "")
			}
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
			cs.root, cs.rev, cs.baseName, cs.files = r.Root, rev, name, files
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
				FitContent: true, Theme: omaEditorTheme(), Font: ui.Font{Size: a.fontSize - 1},
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

// changesPanel is the inspector's Changes tab.
func (a *appState) changesPanel(c *ui.Context) {
	cs := &a.changes
	if cs.open == nil {
		cs.open = map[string]*changeFile{}
	}
	a.refreshChanges(false)
	c.After(time.Second) // follow the files as they change
	info, ok := a.changesPane()
	ui.Column(c).Grow(1).MinHeight(0).Padding(10, 10, 0, 10).Gap(8).Children(func() {
		// The bases, as a segmented control.
		ui.Row(c.Key("seg")).Radius(8).Padding(2).Background(pal.Chip).Shrink(0).Children(func() {
			for _, b := range []struct{ base, label, tip string }{
				{gitsnap.BaseTurn, "Last Turn", "What the agent changed in its last turn"},
				{gitsnap.BaseHead, "Uncommitted", "Changes since HEAD"},
				{gitsnap.BaseBranch, "Branch", "Changes since the default branch"},
			} {
				enabled := b.base != gitsnap.BaseTurn || ok && info.Turn != nil
				seg := ui.ButtonBase(c.Key(b.base)).Grow(1).Height(26).Radius(6).Center().Disabled(!enabled).Tooltip(b.tip)
				col := pal.TextMuted
				if !enabled {
					// Disabled, it paints at half opacity: dim enough.
					seg.Tooltip("No agent turn yet in this pane")
				}
				if cs.base == b.base {
					raised(seg.Background(pal.Raised))
					col = pal.Text
				}
				seg.Children(func() {
					ui.Text(c, b.label).FontSize(12).FontWeight(500).TextColor(col).SingleLine()
				})
				if enabled && seg.Clicked() && cs.base != b.base {
					cs.base = b.base
					a.refreshChanges(true)
				}
			}
		})
		if !ok {
			ui.Text(c, "Focus a terminal pane in a git repository to see its changes.").FontSize(12).TextColor(pal.TextMuted).Padding(8, 2)
			return
		}
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
			since := "since HEAD"
			switch cs.base {
			case gitsnap.BaseTurn:
				if info.Turn != nil {
					since = "since turn " + ago(nowFunc().Sub(time.UnixMilli(info.Turn.At))) + " ago"
				}
			case gitsnap.BaseBranch:
				if cs.baseName != "" && cs.baseName != "HEAD" {
					since = "since " + cs.baseName
				}
			}
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
	row := ui.Row(c.Key("f-"+f.Path)).Height(28).Padding(0, 6).Gap(8).Radius(6).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	if cf != nil || row.Hovered() {
		row.Background(pal.Hover)
	}
	letter, col := "M", pal.Warn
	switch f.Status {
	case "added":
		letter, col = "A", pal.Ok
	case "deleted":
		letter, col = "D", pal.Bad
	case "renamed":
		letter, col = "R", pal.Info
	}
	row.Children(func() {
		ui.Text(c, letter).FontSize(11).FontWeight(700).TextColor(col).Width(12).Shrink(0)
		dir, base := filepath.Split(f.Path)
		ui.Row(c).Shrink(1).MinWidth(0).Children(func() {
			if dir != "" {
				ui.Text(c, dir).FontSize(12).TextColor(pal.TextFaint).SingleLine().Shrink(1).MinWidth(0).Ellipsis("…")
			}
			ui.Text(c, base).FontSize(12).TextColor(pal.Text).SingleLine().Shrink(0)
		})
		ui.Spacer(c)
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
		ds.HunkActions = cs.base == gitsnap.BaseTurn && !a.worktreeBusy(cs.root)
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
	if !a.settings.GitStatus {
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
