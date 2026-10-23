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

func TestKeyBytes(t *testing.T) {
	for k, want := range map[string]string{"enter": "\r", "ctrl+c": "\x03", "alt+x": "\x1bx", "up": "\x1b[A", "q": "q"} {
		got, err := keyBytes(k)
		if err != nil || string(got) != want {
			t.Errorf("%s: %q %v", k, got, err)
		}
	}
	if _, err := keyBytes("nosuchkey"); err == nil {
		t.Error("unknown key")
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

func TestExitCodes(t *testing.T) {
	t.Setenv("OMATERM_SOCKET", t.TempDir()+"/none.sock")
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
