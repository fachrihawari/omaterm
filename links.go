package main

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"omaterm/internal/ospath"
)

// A file path as a program prints it in a terminal, and Ctrl+click opens
// it: src/a.go:12:3, ~/x.go, file:///tmp/a.go, with a line and a column
// after it or not.

// fileLinkAt is the line and column a path may end in.
var fileLinkAt = regexp.MustCompile(`:(\d+)(?::(\d+))?$`)

// parseFileLink splits link into the path, its line and its column (0
// for none); "" for no path at all.
func parseFileLink(link string) (path string, line, col int) {
	return parseFileLinkOS(runtime.GOOS, link)
}

// parseFileLinkOS is parseFileLink as one OS writes paths. On Windows a
// file URL comes back as /C:/a.go, whose first / is the URL's: the path
// is C:/a.go.
func parseFileLinkOS(goos, link string) (path string, line, col int) {
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
	if goos == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' &&
		('a' <= path[1] && path[1] <= 'z' || 'A' <= path[1] && path[1] <= 'Z') {
		path = path[1:]
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

// termLinkPath is a clicked path taken from dir when it is relative. On
// Windows a rooted /src/a.go, as Git Bash prints it, is the pane's drive's
// \src\a.go rather than a folder under the pane's own; elsewhere it stays
// the root's.
func termLinkPath(goos, dir, path string) string {
	if dir == "" || path == "" || ospath.IsAbs(goos, path) {
		return path
	}
	if goos == "windows" && (path[0] == '/' || path[0] == '\\') {
		if vol := ospath.Volume(goos, dir); vol != "" {
			return vol + path
		}
	}
	return filepath.Join(dir, path)
}
