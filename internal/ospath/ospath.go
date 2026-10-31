// Package ospath compares and names paths the way an OS does: Windows
// ignores case, separates with \ (and takes / too), and counts a drive as
// part of a path, where every other OS keeps case and separates with /.
//
// The functions take the OS as an argument, as shellcmd does, so that
// Linux and macOS tests can ask about Windows paths; call sites pass
// runtime.GOOS.
package ospath

import "strings"

// Same reports whether a and b name the same file or folder: cleaned of
// "." and "..", compared without case on Windows.
func Same(goos, a, b string) bool {
	av, arest := split(goos, a)
	bv, brest := split(goos, b)
	ap, bp := components(goos, arest), components(goos, brest)
	if !sameName(goos, av, bv) || len(ap) != len(bp) {
		return false
	}
	for i := range ap {
		if !sameName(goos, ap[i], bp[i]) {
			return false
		}
	}
	return true
}

// Contains reports whether path is dir or inside it, as filepath.Rel with
// no ".." would: "/home/a" holds "/home/a/b" and not "/home/ab".
func Contains(goos, dir, path string) bool {
	if dir == "" || path == "" {
		return false
	}
	dv, drest := split(goos, dir)
	pv, prest := split(goos, path)
	dp, pp := components(goos, drest), components(goos, prest)
	if !sameName(goos, dv, pv) || len(pp) < len(dp) {
		return false
	}
	for i := range dp {
		if !sameName(goos, dp[i], pp[i]) {
			return false
		}
	}
	return true
}

// ShortHome writes dir with home as "~": "~/src" on Unix, `~\src` on
// Windows, in either case.
func ShortHome(goos, home, dir string) string {
	if home == "" || dir == "" {
		return dir
	}
	if Same(goos, home, dir) {
		return "~"
	}
	hv, hrest := split(goos, home)
	dv, drest := split(goos, dir)
	hp, dp := components(goos, hrest), components(goos, drest)
	if !sameName(goos, hv, dv) || len(dp) <= len(hp) {
		return dir
	}
	for i := range hp {
		if !sameName(goos, hp[i], dp[i]) {
			return dir
		}
	}
	sep := string(separator(goos))
	return "~" + sep + strings.Join(dp[len(hp):], sep)
}

// Volume is the volume a path names: "C:" for a drive and \\host\share
// for a UNC path on Windows, and "" everywhere else.
func Volume(goos, p string) string {
	vol, _ := split(goos, p)
	return vol
}

// IsAbs reports whether p is absolute as goos means it: on Windows one
// rooted in a drive or a UNC share, not merely one that starts with \,
// which would be relative to the current drive.
func IsAbs(goos, p string) bool {
	if goos != "windows" {
		return strings.HasPrefix(p, "/")
	}
	vol, rest := split(goos, p)
	return vol != "" && rest != "" && isSep(goos, rest[0])
}

// Base is filepath.Base as goos reads it: a drive's root is named after
// the drive, "D:", where filepath.Base's "\" says nothing.
func Base(goos, p string) string {
	if p == "" {
		return "."
	}
	vol, rest := split(goos, p)
	parts := components(goos, rest)
	if len(parts) == 0 {
		if vol != "" {
			return vol
		}
		return p
	}
	return parts[len(parts)-1]
}

// split separates a path's volume ("C:" or \\host\share on Windows) from
// the rest.
func split(goos, p string) (vol, rest string) {
	if goos != "windows" {
		return "", p
	}
	if len(p) >= 2 && p[1] == ':' && isLetter(p[0]) {
		return p[:2], p[2:]
	}
	if len(p) >= 2 && isSep(goos, p[0]) && isSep(goos, p[1]) {
		// A UNC path's volume is \\host\share.
		s := p[2:]
		i := strings.IndexAny(s, `\/`)
		if i < 0 {
			return p, ""
		}
		j := strings.IndexAny(s[i+1:], `\/`)
		if j < 0 {
			return p, ""
		}
		return p[:2+i+1+j], s[i+1+j:]
	}
	return "", p
}

// components breaks path into its names, resolving "." and ".." as
// filepath.Clean does.
func components(goos, path string) []string {
	fields := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || goos == "windows" && r == '\\'
	})
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		switch f {
		case ".":
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, f)
		}
	}
	return parts
}

func sameName(goos, a, b string) bool {
	if goos == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func separator(goos string) byte {
	if goos == "windows" {
		return '\\'
	}
	return '/'
}

func isSep(goos string, c byte) bool {
	return c == '/' || goos == "windows" && c == '\\'
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
