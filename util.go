package main

import (
	"os"
	"path/filepath"
	"time"
)

// readFileAbs reads root/rel from disk.
func readFileAbs(root, rel string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return string(data), err
}

// relativeTime renders t as "3h ago" style text.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
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
	return d
}

// pathBase returns the last element of a path.
func pathBase(p string) string {
	return filepath.Base(p)
}
