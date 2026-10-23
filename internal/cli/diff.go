package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"omaterm/internal/client"
	"omaterm/internal/proto"
)

// diffCmd prints a pane's changes as a patch: uncommitted ones, those of
// its agent's last turn (--turn), or since the default branch (--branch).
func diffCmd(c *client.Client, args []string) error {
	f, err := parse(args, nil, map[string]string{"turn": "turn", "branch": "branch"})
	if err != nil {
		return err
	}
	st, err := state(c)
	if err != nil {
		return err
	}
	pos := f.pos
	var t target
	if len(pos) > 0 {
		if t2, err := find(st, pos[0]); err == nil {
			t, pos = t2, pos[1:]
		}
	}
	if t.pane == "" {
		if t, err = findOrCurrent(st, nil); err != nil {
			return usageErr("omaterm diff [target] [--turn|--branch] [path…]")
		}
	}
	base := "head"
	switch {
	case f.set["turn"] && f.set["branch"]:
		return usageErr("--turn or --branch, not both")
	case f.set["turn"]:
		base = "turn"
	case f.set["branch"]:
		base = "branch"
	}
	var paths []string
	for _, p := range append(pos, f.rest...) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		paths = append(paths, abs)
	}
	var out proto.ReadResult
	if err := c.Call("git.diff", proto.GitParams{Pane: t.pane, Base: base, Paths: paths}, &out); err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, out.Text)
	return nil
}
