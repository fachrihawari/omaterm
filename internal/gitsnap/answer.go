package gitsnap

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"omaterm/internal/proto"
	"omaterm/internal/session"
)

// Answer answers the socket's git methods for a pane: its changed files
// (git.changes) or its patch (git.diff), against p's base.
func Answer(ctx context.Context, method string, p proto.GitParams, info session.Info) (any, error) {
	base := p.Base
	if base == "" {
		base = BaseHead
	}
	turn := ""
	if info.Turn != nil {
		turn = info.Turn.Tree
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r, err := Open(ctx, info.WorkDir())
	if err != nil {
		return nil, proto.Errorf(proto.CodeNotFound, "pane "+p.Pane+" is not in a git repository")
	}
	rev, _, err := r.Base(ctx, base, turn)
	if err != nil {
		return nil, err
	}
	if method == "git.changes" {
		return r.Changes(ctx, rev, "")
	}
	paths := make([]string, 0, len(p.Paths))
	for _, path := range p.Paths {
		rel, err := filepath.Rel(r.Root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil, proto.Errorf(proto.CodeUsage, path+" is outside the repository")
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	patch, err := r.Patch(ctx, rev, "", paths...)
	return proto.ReadResult{Text: patch}, err
}
