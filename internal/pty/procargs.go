package pty

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// parseProcArgs splits macOS's kern.procargs2 into a process's arguments
// and environment. It is in a file of its own so that its test runs on
// every system. The layout: argc, the executable's path, NULs padding it,
// then argc arguments and the environment, each ending with a NUL, the
// environment ended by an empty string.
func parseProcArgs(b []byte, order binary.ByteOrder) (args, env []string) {
	if len(b) < 4 {
		return nil, nil
	}
	argc := int(order.Uint32(b))
	b = b[4:]
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return nil, nil
	}
	b = bytes.TrimLeft(b[i:], "\x00")
	parts := strings.Split(string(b), "\x00")
	if argc > len(parts) {
		argc = len(parts)
	}
	args = parts[:argc]
	for _, s := range parts[argc:] {
		if s == "" {
			break
		}
		env = append(env, s)
	}
	return args, env
}
