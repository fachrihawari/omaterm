package cli

import (
	"testing"

	"omaterm/internal/proto"
)

func TestFind(t *testing.T) {
	st := proto.State{Sessions: []proto.Session{
		{ID: "s7hd2ka", Name: "dev", Focus: "pb2", Panes: []proto.Pane{{ID: "pa1"}, {ID: "pb2"}}},
		{ID: "s7xq9mm", Name: "tests", Panes: []proto.Pane{{ID: "pc3"}}},
	}}
	cases := []struct {
		ref, session, pane string
		err                error
	}{
		{"s7hd2ka", "s7hd2ka", "pb2", nil},
		{"dev", "s7hd2ka", "pb2", nil},
		{"TESTS", "s7xq9mm", "pc3", nil},
		{"pa1", "s7hd2ka", "pa1", nil},
		{"s7h", "s7hd2ka", "pb2", nil},
		{"s7", "", "", errNotFound}, // ambiguous
		{"zz", "", "", errNotFound},
	}
	for _, c := range cases {
		got, err := find(st, c.ref)
		if c.err != nil {
			if err == nil {
				t.Errorf("%q: want an error", c.ref)
			}
			continue
		}
		if err != nil || got.session != c.session || got.pane != c.pane {
			t.Errorf("%q: %+v %v", c.ref, got, err)
		}
	}
}

func TestUnescape(t *testing.T) {
	if got := unescape(`a\nb\x41\e`); got != "a\nbA\x1b" {
		t.Errorf("%q", got)
	}
}

func TestParse(t *testing.T) {
	f, err := parse([]string{"-n", "x", "--down", "pos", "--", "npm", "run", "dev"},
		map[string]string{"n": "name"}, map[string]string{"down": "down"})
	if err != nil || f.vals["name"] != "x" || !f.set["down"] || f.pos[0] != "pos" || len(f.rest) != 3 {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := parse([]string{"--nope"}, nil, nil); err == nil {
		t.Error("unknown flag")
	}
}

func TestCreateParams(t *testing.T) {
	flags := func(args ...string) *flags {
		f, err := parse(args, map[string]string{"worktree": "worktree", "base": "base", "u": "url", "d": "dir"},
			map[string]string{"no-env": "no-env"})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	p, err := createParams("new", flags("--worktree", "fix-login", "--base", "dev", "--no-env", "-d", "/p", "--", "claude"))
	if err != nil || p.Dir != "/p" || len(p.Command) != 1 || p.Worktree == nil ||
		*p.Worktree != (proto.WorktreeParams{Branch: "fix-login", Base: "dev", NoEnv: true}) {
		t.Fatalf("%+v %v", p, err)
	}
	if p, err := createParams("new", flags("--", "claude")); err != nil || p.Worktree != nil || p.Dir == "" {
		t.Fatalf("no worktree: %+v %v", p, err)
	}
	for _, args := range [][]string{{"--base", "dev"}, {"--no-env"}, {"--worktree", "x", "-u", "localhost:3000"}} {
		if _, err := createParams("new", flags(args...)); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
	if _, err := createParams("split", flags("--worktree", "x")); err == nil {
		t.Error("split --worktree: no error")
	}
}

func TestExitCodes(t *testing.T) {
	// Neither the app nor a session daemon answers: not the user's own.
	t.Setenv("OMATERM_SOCKET", t.TempDir()+"/none.sock")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if code := Run([]string{"ls"}); code != 4 {
		t.Errorf("unreachable: %d", code)
	}
	if code := Run([]string{"frobnicate"}); code != 4 && code != 2 {
		t.Errorf("unknown command: %d", code)
	}
	if code := Run([]string{"report", "bogus"}); code != 2 {
		t.Errorf("bad report: %d", code)
	}
}

func TestBrowserOf(t *testing.T) {
	st := proto.State{Sessions: []proto.Session{
		{ID: "s1", Focus: "t1", Panes: []proto.Pane{{ID: "t1"}, {ID: "b1", URL: "http://localhost:3000/"}}},
		{ID: "s2", Focus: "t2", Panes: []proto.Pane{{ID: "t2"}, {ID: "b2", URL: "x"}, {ID: "b3", URL: "y"}}},
		{ID: "s3", Panes: []proto.Pane{{ID: "t3"}}},
	}}
	if got, err := browserOf(st, target{session: "s1"}); err != nil || got != "b1" {
		t.Errorf("session's one browser: %q %v", got, err)
	}
	if _, err := browserOf(st, target{session: "s2"}); err == nil {
		t.Error("several browsers need a name")
	}
	if _, err := browserOf(st, target{session: "s3"}); err == nil {
		t.Error("no browser")
	}
	if _, err := browserOf(st, target{session: "s1", pane: "t1", byPane: true}); err == nil {
		t.Error("a terminal named as a browser")
	}
}
