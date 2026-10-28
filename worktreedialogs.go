package main

// The dialogs of sessions in a worktree: New Agent in Worktree, Merge.

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/agent"
	"omaterm/internal/model"
	"omaterm/internal/proto"
	"omaterm/internal/worktree"
	"omaterm/service"
)

// worktreeAgents are the agents a worktree session starts, those that
// resume their conversation after a restart, then "shell" for none.
var worktreeAgents = []string{"claude", "codex", "opencode", "pi", "shell"}

// worktreeNew is the New Agent in Worktree dialog.
type worktreeNew struct {
	open          bool
	project, name string // the project's ID and name
	dir           string // its folder
	branch        string
	base          string
	bases         []string // its branches, nil until listed
	agent         string   // of worktreeAgents
	env           bool     // copy the .env files
	busy          bool     // making the worktree
	err           string
}

// openWorktreeDialog asks for a branch to start an agent on in a worktree
// of project pi.
func (a *appState) openWorktreeDialog(pi int) {
	if pi < 0 || pi >= len(a.projects) {
		return
	}
	p := a.projects[pi]
	if service.RepoRoot(p.Path) == "" {
		a.toast(nil, p.Name+" isn't in a git repository, which a worktree needs")
		return
	}
	st := &worktreeNew{open: true, project: p.ID, name: p.Name, dir: p.Path, agent: a.wtAgent, env: a.wtEnv}
	if !slices.Contains(worktreeAgents, st.agent) {
		st.agent = defaultWorktreeAgent()
	}
	a.wtNew = st
	list := func() {
		bs, _ := service.ListBranches(p.Path)
		cur := service.CurrentBranch(p.Path)
		a.post(func() {
			st.bases = bs
			if bs == nil {
				st.bases = []string{}
			}
			// Detached, it starts from the branch work goes into.
			for _, b := range []string{cur, "main", "master"} {
				if st.base == "" && slices.Contains(bs, b) {
					st.base = b
				}
			}
			if st.base == "" && len(bs) > 0 {
				st.base = bs[0]
			}
		})
	}
	if a.win == nil {
		list()
		a.drainPosted()
		return
	}
	go list()
}

// defaultWorktreeAgent is the first agent installed, as the window's PATH
// finds them, else Claude Code: one on the login shell's PATH alone runs
// all the same.
func defaultWorktreeAgent() string {
	for _, name := range worktreeAgents[:len(worktreeAgents)-1] {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return "claude"
}

// worktreeDialog is the New Agent in Worktree dialog, while open.
func (a *appState) worktreeDialog(c *ui.Context) {
	st := a.wtNew
	if st == nil {
		return
	}
	if !st.open {
		a.wtNew = nil
		return
	}
	submitted := false
	ui.DialogBase(c, &st.open, func(backdrop, panel ui.Element) {
		dialogFrame(c, backdrop, panel)
		dialogTitle(c, "New Agent in Worktree", false)
		ui.Text(c, "It works on a branch of its own, in "+st.name+"/"+worktree.Folder+", beside your other sessions.").
			FontSize(12.5).TextColor(pal.TextMuted).Wrap()
		dialogField(c, "Branch", func() {
			f := ui.Row(c.Key("wt-branch")).Height(28).Grow(1).Padding(0, 8).Radius(6).AlignItems(ui.Center).
				Background(pal.Input).Border(1, pal.BorderStrong)
			f.Children(func() {
				in := ui.TextInputBase(c, &st.branch).Placeholder("fix-login").FontSize(12.5).Grow(1).MinWidth(0).
					AutoFocus().Label("Branch")
				if in.Focused() {
					f.Border(1, pal.Accent)
				}
				submitted = in.Submitted()
			})
		})
		if name := strings.TrimSpace(st.branch); name != "" {
			if err := worktree.ValidBranch(name); err != nil {
				dialogNote(c, sentence(err.Error()), true)
			} else if slices.Contains(st.bases, name) {
				dialogNote(c, "The branch exists: the agent goes on with it.", false)
			}
		}
		dialogField(c, "From", func() {
			if st.bases == nil {
				ui.Text(c, "…").FontSize(12).TextColor(pal.TextMuted)
				return
			}
			opts := make([][2]string, len(st.bases))
			for i, b := range st.bases {
				opts[i] = [2]string{b, b}
			}
			settingsPopup(c, "wt-base", &st.base, opts)
		})
		dialogField(c, "Agent", func() {
			opts := make([][2]string, len(worktreeAgents))
			for i, name := range worktreeAgents {
				opts[i] = [2]string{name, agent.DisplayName(name)}
			}
			opts[len(opts)-1][1] = "Shell only"
			settingsPopup(c, "wt-agent", &st.agent, opts)
		})
		dialogField(c, "Copy .env files", func() {
			settingsSwitch(c, "wt-env", &st.env, false)
		})
		if st.err != "" {
			ui.Text(c, st.err).FontSize(12).TextColor(pal.Bad).Wrap()
		}
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			if dialogButton(c, "Cancel", false, false) && !st.busy {
				st.open = false
			}
			label := "Create"
			if st.busy {
				label = "Creating…"
			}
			if dialogButton(c, label, true, false) {
				submitted = true
			}
		})
	})
	// Under way, it stays: what it makes is told in it.
	if st.busy {
		st.open = true
	}
	if submitted && !st.busy {
		a.createWorktreeFromDialog(st)
	}
}

// createWorktreeFromDialog makes the worktree the dialog asks for, then its
// session; the dialog says what went wrong, if anything. A branch whose
// worktree has a session already goes to that session.
func (a *appState) createWorktreeFromDialog(st *worktreeNew) {
	branch := strings.TrimSpace(st.branch)
	if err := worktree.ValidBranch(branch); err != nil {
		st.err = sentence(err.Error())
		return
	}
	st.busy, st.err = true, ""
	a.wtAgent, a.wtEnv = st.agent, st.env
	var wt worktree.Info
	a.worktreeJob(func(ctx context.Context) (err error) {
		wt, err = worktree.Add(ctx, st.dir, branch, st.base, st.env)
		return err
	}, func(err error) {
		st.busy = false
		if err == nil {
			if pi, t := a.worktreeSession(wt.Dir); t != nil {
				a.focusSession(pi, t.ID)
			} else {
				var command []string
				if st.agent != "shell" {
					command = []string{st.agent}
				}
				_, err = a.createWorktreeSession(wt, proto.CreateParams{Project: st.project, Command: command, Focus: true})
			}
		}
		if err != nil {
			st.err = "Couldn't make the worktree: " + err.Error()
			return
		}
		if a.wtNew == st {
			a.wtNew = nil
		}
	})
}

// worktreeJob runs git work off the UI thread, then done on it; with no
// window (tests), at once.
func (a *appState) worktreeJob(work func(ctx context.Context) error, done func(error)) {
	run := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), worktreeTimeout)
		defer cancel()
		return work(ctx)
	}
	if a.win == nil {
		done(run())
		return
	}
	go func() {
		err := run()
		a.post(func() { done(err) })
	}()
}

// dialogField is a row of a dialog: a label, then its control.
func dialogField(c *ui.Context, label string, control func()) {
	ui.Row(c.Key("field-" + label)).FillWidth().Gap(10).AlignItems(ui.Center).MinHeight(28).Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(pal.Text).Width(130).Shrink(0)
		ui.Row(c).Grow(1).MinWidth(0).Justify(ui.End).AlignItems(ui.Center).Children(control)
	})
}

// dialogNote is a line under a dialog's field: a hint, or what is wrong.
func dialogNote(c *ui.Context, text string, bad bool) {
	color := pal.TextFaint
	if bad {
		color = pal.Bad
	}
	ui.Text(c, text).FontSize(11.5).TextColor(color).Wrap().Margin(-4, 0, 0, 140)
}

// sentence makes a message a sentence: its first letter capital.
func sentence(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// worktreeMerge is the Merge dialog of a session's worktree.
type worktreeMerge struct {
	open    bool
	session string // the session's ID
	w       model.Worktree
	sum     *worktree.Summary // nil until read
	sumErr  string
	how     string // "squash" or "merge commit"
	msg     string
	remove  bool // the worktree and branch, once merged
	busy    bool
	err     string
}

// atWork tells, in a toast, that an agent works in the worktree, which
// what changes it waits for: its files would change under it.
func (a *appState) atWork(w *model.Worktree) bool {
	if !a.worktreeBusy(w.Dir) {
		return false
	}
	a.toast(nil, "An agent is working in "+w.Branch+": wait until it stops")
	return true
}

// openMergeDialog asks how to merge a session's worktree into its base.
func (a *appState) openMergeDialog(t topTab) {
	if a.atWork(t.Worktree) {
		return
	}
	st := &worktreeMerge{open: true, session: t.ID, w: *t.Worktree, how: "squash", remove: true}
	if !a.mergeSquash {
		st.how = "merge commit"
	}
	a.wtMerge = st
	var s worktree.Summary
	a.worktreeJob(func(ctx context.Context) (err error) {
		s, err = worktree.Summarize(ctx, worktreeOf(&st.w))
		return err
	}, func(err error) {
		if err != nil {
			st.sumErr = err.Error()
			return
		}
		st.sum = &s
		// One commit says it all; several make the body.
		switch n := len(s.Commits); {
		case n == 1:
			st.msg = s.Commits[0]
		case n > 1:
			st.msg = st.w.Branch + "\n"
			for _, c := range s.Commits {
				st.msg += "\n* " + c
			}
		default:
			st.msg = st.w.Branch
		}
	})
}

// mergeSummary words what a merge brings: its commits, its files and
// lines, and its files not committed yet.
func mergeSummary(s worktree.Summary) string {
	var parts []string
	if n := len(s.Commits); n > 0 {
		parts = append(parts, plural(n, "commit"))
	}
	if s.Files > 0 {
		parts = append(parts, plural(s.Files, "file")+" changed (+"+itoa(s.Added)+" −"+itoa(s.Removed)+")")
	}
	if s.Uncommitted > 0 {
		parts = append(parts, plural(s.Uncommitted, "uncommitted file"))
	}
	if len(parts) == 0 {
		return "Nothing to merge yet."
	}
	return strings.Join(parts, ", ")
}

// mergeDialog is the Merge dialog, while open.
func (a *appState) mergeDialog(c *ui.Context) {
	st := a.wtMerge
	if st == nil {
		return
	}
	if !st.open {
		a.wtMerge = nil
		return
	}
	squash := st.how == "squash"
	// A message for the squashed commit, or for the files not committed,
	// which a merge commit commits first.
	needMsg := squash || st.sum != nil && st.sum.Uncommitted > 0
	merge := false
	ui.DialogBase(c, &st.open, func(backdrop, panel ui.Element) {
		dialogFrame(c, backdrop, panel)
		dialogTitle(c, "Merge "+st.w.Branch+" into "+st.w.Base, false)
		switch {
		case st.sumErr != "":
			ui.Text(c, st.sumErr).FontSize(12).TextColor(pal.Bad).Wrap()
		case st.sum == nil:
			ui.Text(c, "Reading the worktree…").FontSize(12.5).TextColor(pal.TextMuted)
		default:
			ui.Text(c, mergeSummary(*st.sum)).FontSize(12.5).TextColor(pal.TextMuted).Wrap()
		}
		dialogField(c, "Merge as", func() {
			settingsSegmented(c, "merge-how", &st.how, []string{"Squash", "Merge Commit"})
		})
		if needMsg {
			label := "Commit message"
			if !squash {
				label = "Commit its uncommitted files as"
			}
			ui.Text(c, label).FontSize(12.5).TextColor(pal.Text)
			ui.Box(c).FillWidth().Radius(6).Border(1, pal.BorderStrong).Background(pal.Input).Children(func() {
				ui.TextAreaBase(c, &st.msg).FontSize(12).Padding(8).MinHeight(64).MaxHeight(160).FillWidth().Label("Message")
			})
		}
		dialogField(c, "Remove the worktree", func() {
			settingsSwitch(c, "merge-remove", &st.remove, false)
		})
		if st.err != "" {
			ui.Text(c, st.err).FontSize(12).TextColor(pal.Bad).Wrap()
		}
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			if dialogButton(c, "Cancel", false, false) && !st.busy {
				st.open = false
			}
			label := "Merge"
			if st.busy {
				label = "Merging…"
			}
			if dialogButton(c, label, true, false) {
				merge = true
			}
		})
	})
	if st.busy {
		st.open = true
	}
	if merge && !st.busy && st.sum != nil {
		a.mergeFromDialog(st, needMsg)
	}
}

// mergeFromDialog merges the worktree as the dialog says, then closes its
// session and removes it, or, kept, brings the merge back into it, so
// that it merges again with only what is new.
func (a *appState) mergeFromDialog(st *worktreeMerge, needMsg bool) {
	msg := strings.TrimSpace(st.msg)
	if needMsg && msg == "" {
		st.err = "Write the commit's message"
		return
	}
	if msg == "" {
		msg = "Work on " + st.w.Branch
	}
	if a.worktreeBusy(st.w.Dir) {
		st.err = "An agent is working in it: wait until it stops"
		return
	}
	squash, remove, w := st.how == "squash", st.remove, st.w
	st.busy, st.err = true, ""
	a.mergeSquash = squash
	a.worktreeJob(func(ctx context.Context) error {
		if err := worktree.Merge(ctx, worktreeOf(&w), msg, squash); err != nil {
			return err
		}
		if squash && !remove {
			return worktree.Update(ctx, worktreeOf(&w), msg)
		}
		return nil
	}, func(err error) {
		st.busy = false
		var conflict *worktree.ConflictError
		switch {
		case errors.As(err, &conflict):
			st.err = sentence(conflict.Error()) + ". Choose Update from " + w.Base + " to resolve the conflict in the worktree."
			return
		case err != nil:
			st.err = "Couldn't merge: " + err.Error()
			return
		}
		if a.wtMerge == st {
			a.wtMerge = nil
		}
		a.toast(nil, "Merged "+w.Branch+" into "+w.Base)
		if remove {
			a.cliCloseSession(st.session)
			a.removeWorktree(w)
			return
		}
		a.refreshRepos()
	})
}

// updateWorktree brings the base's work into a session's worktree. A
// conflict stays there, for the user or the agent to resolve: the Git
// panel shows it.
func (a *appState) updateWorktree(t topTab) {
	w := *t.Worktree
	if a.atWork(&w) {
		return
	}
	a.worktreeJob(func(ctx context.Context) error {
		return worktree.Update(ctx, worktreeOf(&w), "Work on "+w.Branch)
	}, func(err error) {
		var conflict *worktree.ConflictError
		switch {
		case errors.As(err, &conflict):
			if pi, _, _, ok := a.findSession(t.ID); ok {
				a.focusSession(pi, t.ID)
				a.setInspector(true)
				a.showInspectorTab(2)
			}
			a.toast(nil, sentence(conflict.Error())+". Resolve the conflict in the Git panel, or ask the agent to.")
		case err != nil:
			a.toast(nil, "Couldn't update "+w.Branch+": "+err.Error())
		default:
			a.toast(nil, "Updated "+w.Branch+" from "+w.Base)
		}
		a.refreshRepos()
	})
}
