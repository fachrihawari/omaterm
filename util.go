package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// readFileAbs reads root/rel from disk.
func readFileAbs(root, rel string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return string(data), err
}

// nowFunc is time.Now, replaced by the screenshot suite so relative
// labels ("3h ago") are stable in its goldens.
var nowFunc = time.Now

// copyText puts text on the clipboard. It is a variable because the
// clipboard waits on the app's main loop, which a test has none of: a
// test replaces it and reads what would have been copied.
var copyText = mygo.Clipboard.WriteText

// relativeTime renders t as "3h ago" style text.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := nowFunc().Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return itoa(m) + "m ago"
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + "h ago"
	case d < 30*24*time.Hour:
		return itoa(int(d.Hours()/24)) + "d ago"
	case d < 365*24*time.Hour:
		return itoa(int(d.Hours()/(24*30))) + "mo ago"
	default:
		return itoa(int(d.Hours()/(24*365))) + "y ago"
	}
}

// pathDir returns the directory part of a slash path, "" for top-level.
func pathDir(p string) string {
	d := filepath.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	if filepath.IsAbs(d) {
		return shortDir(d)
	}
	return d
}

// pathBase returns the last element of a path.
func pathBase(p string) string {
	return filepath.Base(p)
}

// stripFrontMatter drops a leading YAML front matter block (--- ... ---)
// so the markdown preview does not read its keys as a heading and a rule.
func stripFrontMatter(s string) string {
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return s
	}
	rest := s[strings.IndexByte(s, '\n')+1:]
	for {
		i := strings.IndexByte(rest, '\n')
		if i < 0 {
			return s // unterminated: leave it as written
		}
		line := strings.TrimSuffix(rest[:i], "\r")
		rest = rest[i+1:]
		if line == "---" || line == "..." {
			return rest
		}
	}
}
