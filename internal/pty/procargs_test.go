package pty

import (
	"encoding/binary"
	"slices"
	"testing"
)

func TestParseProcArgs(t *testing.T) {
	raw := func(argc uint32, rest string) []byte {
		b := binary.LittleEndian.AppendUint32(nil, argc)
		return append(b, rest...)
	}
	for _, tc := range []struct {
		name      string
		in        []byte
		args, env []string
	}{
		{"args and env",
			raw(3, "/opt/homebrew/bin/node\x00\x00\x00\x00node\x00/usr/local/bin/codex\x00--yolo\x00PWD=/Users/me/app\x00HOME=/Users/me\x00\x00\x00junk"),
			[]string{"node", "/usr/local/bin/codex", "--yolo"}, []string{"PWD=/Users/me/app", "HOME=/Users/me"}},
		{"an empty argument", raw(3, "/bin/sh\x00sh\x00\x00-c\x00\x00"),
			[]string{"sh", "", "-c"}, nil},
		{"argc past the end", raw(5, "/bin/sleep\x00sleep\x005"), []string{"sleep", "5"}, nil},
		{"too short", []byte{1, 0}, nil, nil},
		{"no path end", raw(1, "/bin/sleep"), nil, nil},
	} {
		args, env := parseProcArgs(tc.in, binary.LittleEndian)
		if !slices.Equal(args, tc.args) || !slices.Equal(env, tc.env) {
			t.Errorf("%s: got %q %q, want %q %q", tc.name, args, env, tc.args, tc.env)
		}
	}
}
