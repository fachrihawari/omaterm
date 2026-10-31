package pty

import (
	"strings"
)

// commandLine joins a program and its arguments into the one command line
// Windows starts a program with, which the program splits itself. Most
// split it as the C runtime does, which escapeArg writes for; cmd reads
// its line as it is, so what follows its /c or /k goes in raw, between the
// quotes its /s strips. args[0] is the program's name.
func commandLine(path string, args []string) string {
	parts := []string{escapeArg(path)}
	rest := args
	if len(rest) > 0 {
		rest = rest[1:]
	}
	if cmdName(path) {
		for i, a := range rest {
			if !strings.EqualFold(a, "/c") && !strings.EqualFold(a, "/k") {
				continue
			}
			flags := rest[:i]
			if !hasFlag(flags, "/s") {
				parts = append(parts, "/s")
			}
			for _, f := range rest[:i+1] {
				parts = append(parts, escapeArg(f))
			}
			return strings.Join(parts, " ") + ` "` + strings.Join(rest[i+1:], " ") + `"`
		}
	}
	for _, a := range rest {
		parts = append(parts, escapeArg(a))
	}
	return strings.Join(parts, " ")
}

// cmdName tells cmd.exe's path.
func cmdName(path string) bool {
	if i := strings.LastIndexAny(path, `\/`); i >= 0 {
		path = path[i+1:]
	}
	return strings.EqualFold(path, "cmd.exe") || strings.EqualFold(path, "cmd")
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if strings.EqualFold(a, flag) {
			return true
		}
	}
	return false
}

// escapeArg quotes an argument as the C runtime splits a command line:
// syscall.EscapeArg's rules, which only Windows builds have.
func escapeArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			slashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, slashes+1))
			slashes = 0
		default:
			slashes = 0
		}
		b.WriteByte(s[i])
	}
	b.WriteString(strings.Repeat(`\`, slashes))
	b.WriteByte('"')
	return b.String()
}

// splitCommandLine splits a command line as the C runtime does, which most
// Windows programs follow: blanks separate arguments, except inside double
// quotes; 2n backslashes before a quote are n and the quote counts, 2n+1
// are n and a literal quote; other backslashes are themselves.
func splitCommandLine(s string) []string {
	var args []string
	var b strings.Builder
	in, quoted := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			n := 0
			for i < len(s) && s[i] == '\\' {
				n++
				i++
			}
			if i < len(s) && s[i] == '"' {
				b.WriteString(strings.Repeat(`\`, n/2))
				if n%2 == 1 {
					b.WriteByte('"')
				} else {
					quoted = !quoted
				}
			} else {
				b.WriteString(strings.Repeat(`\`, n))
				i--
			}
			in = true
		case c == '"':
			// "" inside quotes is a literal quote, as the C runtime reads it.
			if quoted && i+1 < len(s) && s[i+1] == '"' {
				b.WriteByte('"')
				i++
			} else {
				quoted = !quoted
			}
			in = true
		case (c == ' ' || c == '\t') && !quoted:
			if in {
				args = append(args, b.String())
				b.Reset()
				in = false
			}
		default:
			b.WriteByte(c)
			in = true
		}
	}
	if in {
		args = append(args, b.String())
	}
	return args
}
