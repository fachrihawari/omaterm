package main

// Sessions in a git worktree of their own (internal/worktree): an agent
// works on a branch without touching the main checkout, and the user
// merges its work, or discards it.

import (
	"context"
	"fmt"
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
