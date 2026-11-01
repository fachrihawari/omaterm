package cli

import (
	"fmt"
	"os"

	"omaterm/internal/client"
	"omaterm/internal/proto"
)

// mergeCmd merges a worker's worktree into the branch of the lead running
// the command: a session made from the lead's own branch, which omaterm
// closes after, with its worktree, unless --keep. From the main checkout,
// it waits for the user to approve.
func mergeCmd(c *client.Client, args []string) error {
	f, err := parse(args, map[string]string{"m": "message", "message": "message"},
		map[string]string{"commit": "commit", "squash": "squash", "keep": "keep", "json": "json"})
	if err != nil {
		return err
	}
	switch {
	case len(f.pos) != 1:
		return usageErr("omaterm merge <target> [--squash|--commit] [-m message] [--keep]")
	case f.set["commit"] && f.set["squash"]:
		return usageErr("merge --squash or --commit, not both")
	}
	st, err := state(c)
	if err != nil {
		return err
	}
	t, err := find(st, f.pos[0])
	if err != nil {
		return err
	}
	by, _, err := mine(st)
	if err != nil {
		return err
	}
	if by == "" {
		return usageErr("run omaterm merge in your pane: it merges into your branch")
	}
	if leadIsInMainCheckout(st, by) {
		fmt.Fprintf(os.Stderr, "waiting for the user to approve merging into the main checkout…\n")
	}
	var out proto.Merged
	if err := c.Call("worktree.merge", proto.MergeParams{By: by, Target: t.session, Commit: f.set["commit"],
		Message: f.vals["message"], Keep: f.set["keep"]}, &out); err != nil {
		return err
	}
	if f.set["json"] {
		return printJSON(out)
	}
	fmt.Printf("merged %s into %s (%d %s)\n", out.Branch, out.Into, len(out.Commits), plural(len(out.Commits), "commit", "commits"))
	if out.Removed {
		fmt.Printf("closed its session, and removed its worktree and branch\n")
	}
	return nil
}

// leadIsInMainCheckout tells whether pane works in the main checkout, where
// a merge waits for the user to approve it.
func leadIsInMainCheckout(st proto.State, pane string) bool {
	for _, s := range st.Sessions {
		for _, p := range s.Panes {
			if p.ID == pane {
				return s.Worktree == nil
			}
		}
	}
	return false
}
