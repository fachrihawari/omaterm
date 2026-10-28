package main

// Sessions in a git worktree of their own (internal/worktree): an agent
// works on a branch without touching the main checkout, and the user
// merges its work, or discards it.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"omaterm/internal/model"
	"omaterm/internal/proto"
	"omaterm/internal/worktree"
)

// worktreeTimeout bounds a worktree's git work: a checkout of a large
// repository, with its hooks, or a merge.
const worktreeTimeout = 5 * time.Minute

// createWorktree makes a session in a worktree of its own for the omaterm
// command: its project is found on the UI thread, the worktree made off
// it, as git may take longer than the UI thread is given, then the session.
func (h *localHost) createWorktree(ctx context.Context, p proto.CreateParams) (any, error) {
	a := h.a
	v, err := h.onUI(func() (any, error) {
		if len(a.projects) == 0 && p.Dir == "" {
			return nil, proto.Errorf(proto.CodeUsage, "no project: give a directory")
		}
		pi := a.projectFor(p.Project, p.Dir)
		if pi < 0 || pi >= len(a.projects) {
			return nil, proto.Errorf(proto.CodeNotFound, "no project "+p.Project)
		}
		if a.projects[pi].ID == "" {
			a.projects[pi].ID = newID()
		}
		return a.projects[pi], nil
	})
	if err != nil {
		return nil, err
	}
	proj := v.(project)
	from := p.Dir
	if from == "" {
		from = proj.Path
	}
	// The command going away doesn't stop git midway through a checkout.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), worktreeTimeout)
	defer cancel()
	wt, err := worktree.Add(ctx, from, p.Worktree.Branch, p.Worktree.Base, !p.Worktree.NoEnv)
	if err != nil {
		return nil, err
	}
	return h.onUI(func() (any, error) {
		p.Project = proj.ID
		return a.createWorktreeSession(wt, p)
	})
}

// createWorktreeSession makes a session in the worktree wt, in the project
// p names, called after its branch unless p names it. A worktree has one
// session: a second would work in the first one's files.
func (a *appState) createWorktreeSession(wt worktree.Info, p proto.CreateParams) (proto.Created, error) {
	if _, t := a.worktreeSession(wt.Dir); t != nil {
		return proto.Created{}, fmt.Errorf("%s has a session already", wt.Branch)
	}
	p.Dir, p.Worktree = wt.Start(), nil
	if p.Name == "" {
		p.Name = wt.Branch
	}
	v, err := a.cliCreate(p)
	if err != nil {
		return proto.Created{}, err
	}
	c := v.(proto.Created)
	if _, tabs, ti, ok := a.findSession(c.Session); ok {
		(*tabs)[ti].Worktree = &model.Worktree{Dir: wt.Dir, Branch: wt.Branch, Base: wt.Base}
	}
	return c, nil
}

// worktreeSession finds the session working in the worktree dir.
func (a *appState) worktreeSession(dir string) (pi int, t *topTab) {
	for pi := range a.projects {
		tabs := a.projectTabsPtr(&a.projects[pi])
		if tabs == nil {
			continue
		}
		for ti := range *tabs {
			if w := (*tabs)[ti].Worktree; w != nil && w.Dir == dir {
				return pi, &(*tabs)[ti]
			}
		}
	}
	return -1, nil
}

// worktreeDirs lists the worktrees of the shown project's sessions.
func (a *appState) worktreeDirs() []string {
	var dirs []string
	for _, t := range a.tabs {
		if t.Worktree != nil && !t.Worktree.Gone() {
			dirs = append(dirs, t.Worktree.Dir)
		}
	}
	return dirs
}

// isWorktreeRoot reports whether root is a worktree of a shown session.
func (a *appState) isWorktreeRoot(root string) bool {
	for _, t := range a.tabs {
		if t.Worktree != nil && t.Worktree.Dir == root {
			return true
		}
	}
	return false
}

// selectRepo shows the i-th repo in the Git panel.
func (a *appState) selectRepo(i int) {
	if i < 0 || i >= len(a.repos) || i == a.activeRepo {
		return
	}
	a.activeRepo = i
	a.commits = nil
	a.graphLoaded = false
}

// followWorktreeRepo shows, in the Git panel, the worktree of the session
// the user goes to, and the repo shown before when they go to one in the
// project's checkout: committing, and resolving an update's conflicts,
// happen in the worktree. It follows the session as it changes only, so a
// repo picked meanwhile stays.
func (a *appState) followWorktreeRepo() {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) || a.tabs[a.activeTab].Kind != tabTerminal {
		return // an editor or a diff, opened from the panel
	}
	t := &a.tabs[a.activeTab]
	if t.ID == a.repoFollow {
		return
	}
	a.repoFollow = t.ID
	cur := a.activeRepoRef()
	if t.Worktree == nil {
		if cur != nil && a.isWorktreeRoot(cur.Root) {
			a.selectRepo(min(a.repoMain, len(a.repos)-1))
		}
		return
	}
	for i := range a.repos {
		if a.repos[i].Root == t.Worktree.Dir {
			if cur != nil && !a.isWorktreeRoot(cur.Root) {
				a.repoMain = a.activeRepo
			}
			a.selectRepo(i)
			return
		}
	}
	// Made since the repos were found: search again, once.
	if a.repoAsked != t.Worktree.Dir && !t.Worktree.Gone() {
		a.repoAsked = t.Worktree.Dir
		a.refreshRepos()
	}
}

// worktreeOf is a session's worktree, as internal/worktree has it.
func worktreeOf(w *model.Worktree) worktree.Info { return worktree.Of(w.Dir, w.Branch, w.Base) }

// unmergedText says what of a worktree's work its base hasn't.
func unmergedText(w *model.Worktree, s worktree.Summary) string {
	var parts []string
	if n := len(s.Commits); n > 0 {
		parts = append(parts, plural(n, "commit"))
	}
	if s.Uncommitted > 0 {
		parts = append(parts, plural(s.Uncommitted, "uncommitted file"))
	}
	if len(parts) == 0 {
		parts = append(parts, plural(s.Files, "changed file"))
	}
	return w.Branch + " has " + strings.Join(parts, " and ") + " not in " + w.Base + "."
}

// closeWorktreeAsking closes a session in a worktree: with its worktree
// when nothing in it is missing from its base, else as the user chooses,
// keeping the worktree to go on later or discarding it. That is asked
// whatever the settings say: closing would lose work.
func (a *appState) closeWorktreeAsking(t topTab) {
	w, panes := *t.Worktree, t.Split.termIDs()
	var s worktree.Summary
	a.worktreeJob(func(ctx context.Context) (err error) {
		s, err = worktree.Summarize(ctx, worktreeOf(&w))
		return err
	}, func(err error) {
		closeIt := func() { a.cliCloseSession(t.ID) }
		discard := func() {
			closeIt()
			a.removeWorktree(w)
		}
		switch {
		case err != nil: // it can't tell: the worktree stays
			a.confirmCloseRunning(panes, "Close Session", closeIt)
		case !s.Unmerged():
			a.confirmCloseRunning(panes, "Close Session", discard)
		default:
			text := unmergedText(&w, s) + " Keep its worktree to go on later, or discard it."
			if what := a.runningProgram(panes); what != "" {
				text = what + " is still running. Closing ends it. " + text
			}
			a.closeAsk = &closeAsk{open: true, title: "Close Session", text: text,
				alt: "Keep Worktree", altRun: closeIt, confirm: "Discard", run: discard}
		}
	})
}

// discardWorktreeAsking deletes a session's worktree and branch, and
// closes the session, once the user confirms what goes with them.
func (a *appState) discardWorktreeAsking(t topTab) {
	w, panes := *t.Worktree, t.Split.termIDs()
	var s worktree.Summary
	a.worktreeJob(func(ctx context.Context) (err error) {
		s, err = worktree.Summarize(ctx, worktreeOf(&w))
		return err
	}, func(err error) {
		text := "Its folder and the branch " + w.Branch + " are deleted."
		if err == nil && s.Unmerged() {
			text += " " + unmergedText(&w, s)
		}
		if what := a.runningProgram(panes); what != "" {
			text += " " + what + " is still running: it ends."
		}
		a.askDanger("Discard "+w.Branch+"?", text, "Discard", func() {
			a.cliCloseSession(t.ID)
			a.removeWorktree(w)
		})
	})
}

// removeWorktree deletes a worktree and its branch, its session closed.
func (a *appState) removeWorktree(w model.Worktree) {
	a.worktreeJob(func(ctx context.Context) error {
		return worktree.Remove(ctx, worktreeOf(&w))
	}, func(err error) {
		if err != nil {
			a.toast(nil, "Couldn't remove "+w.Branch+"'s worktree: "+err.Error())
			return
		}
		a.refreshRepos()
	})
}
