package main

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// A file path as a program prints it in a terminal, and Ctrl+click opens
// it: src/a.go:12:3, ~/x.go, file:///tmp/a.go, with a line and a column
// after it or not.

// fileLinkAt is the line and column a path may end in.
var fileLinkAt = regexp.MustCompile(`:(\d+)(?::(\d+))?$`)

// parseFileLink splits link into the path, its line and its column (0
// for none); "" for no path at all.
func parseFileLink(link string) (path string, line, col int) {
	path = link
	for _, prefix := range []string{"file://localhost", "file://"} {
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
			if p, err := url.PathUnescape(path); err == nil {
				path = p
			}
			break
		}
	}
	if m := fileLinkAt.FindStringSubmatch(path); m != nil && len(m[0]) < len(path) {
		path = path[:len(path)-len(m[0])]
		line, _ = strconv.Atoi(m[1])
		if m[2] != "" {
			col, _ = strconv.Atoi(m[2])
		}
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[1:])
		}
	}
	return path, line, col
}
