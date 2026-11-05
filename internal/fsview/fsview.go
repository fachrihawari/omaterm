// Package fsview reads a project's folder for a viewer that may not reach
// past it: its folders' entries and its files, by paths relative to the
// folder, refused when they lead outside it.
package fsview

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"omaterm/service"
)

// ErrBadPath is a path that leads outside the root, through .. or a
// symbolic link, or into its git directory.
var ErrBadPath = errors.New("not a path in the folder")

// MaxEntries is the most entries List returns of one folder.
const MaxEntries = 2000

// MaxText is the most bytes of a text file Read returns: past it, a file
// is "large".
const MaxText = 512 << 10

// Entry is one of a folder's entries.
type Entry struct {
	Name  string `json:"name"`
	Dir   bool   `json:"dir,omitempty"`
	Muted bool   `json:"muted,omitempty"` // ignored by git, or node_modules
	Size  int64  `json:"size,omitempty"`
}

// Dir is a folder's entries, its folders first; More tells there were more
// than MaxEntries.
type Dir struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
	More    bool    `json:"more,omitempty"`
}

// File is a file as a viewer shows it. Kind is "text", with its Text;
// "image", which the viewer loads apart; "binary"; or "large", text past
// MaxText.
type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

// imageExts are the pictures the app and the page both show.
var imageExts = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
	".bmp":  true,
	".svg":  true,
}

// IsImage reports whether path names a picture, by its extension.
func IsImage(path string) bool {
	return imageExts[strings.ToLower(filepath.Ext(path))]
}

// Local returns the file of root that the slash-separated rel names, ""
// or "." being root itself. It refuses an absolute path, one through ..,
// one into .git, and one whose links lead outside root.
func Local(root, rel string) (string, error) {
	if filepath.Separator == '\\' {
		rel = strings.ReplaceAll(rel, `\`, "/")
	}
	clean := path.Clean("/" + rel)[1:]
	if path.IsAbs(rel) || filepath.IsAbs(filepath.FromSlash(rel)) || slices.Contains(strings.Split(rel, "/"), "..") {
		return "", ErrBadPath
	}
	if slices.ContainsFunc(strings.Split(clean, "/"), func(s string) bool { return strings.EqualFold(s, ".git") }) {
		return "", ErrBadPath
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.FromSlash(clean))
	// The file itself, its links resolved, must be root's: a link in it
	// may point anywhere.
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	if r, err := filepath.Rel(realRoot, real); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", ErrBadPath
	}
	return full, nil
}

// List returns the entries of root's folder rel, its folders first and
// each by name, as the desktop's Files tab orders them. It leaves out
// .git, and mutes what git ignores and node_modules: they are there, but
// seldom what one looks for.
func List(root, rel string) (Dir, error) {
	dir, err := Local(root, rel)
	if err != nil {
		return Dir{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Dir{}, err
	}
	prefix := strings.Trim(path.Clean("/" + rel)[1:], "/")
	ignored := service.Ignored(root)
	muted := func(p string) bool {
		for p != "" && p != "." {
			if path.Base(p) == "node_modules" || ignored[p] {
				return true
			}
			p = path.Dir(p)
		}
		return false
	}
	out := Dir{Path: prefix, Entries: []Entry{}}
	for _, e := range entries {
		name := e.Name()
		full := filepath.Join(dir, name)
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(full); err == nil {
				isDir = st.IsDir()
			}
		}
		if isDir && (name == ".git" || service.Protected(full)) {
			continue
		}
		if len(out.Entries) == MaxEntries {
			out.More = true
			break
		}
		en := Entry{Name: name, Dir: isDir, Muted: muted(path.Join(prefix, name))}
		if !isDir {
			if info, err := e.Info(); err == nil {
				en.Size = info.Size()
			}
		}
		out.Entries = append(out.Entries, en)
	}
	slices.SortStableFunc(out.Entries, func(x, y Entry) int {
		if x.Dir != y.Dir {
			if x.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(x.Name, y.Name)
	})
	return out, nil
}

// Read returns root's file rel as a viewer shows it: a picture is only
// named, and a binary or large file has no text.
func Read(root, rel string) (File, error) {
	full, err := Local(root, rel)
	if err != nil {
		return File{}, err
	}
	st, err := os.Stat(full)
	if err != nil {
		return File{}, err
	}
	if st.IsDir() {
		return File{}, ErrBadPath
	}
	f := File{Path: strings.Trim(path.Clean("/" + rel)[1:], "/"), Size: st.Size()}
	switch {
	case IsImage(full):
		f.Kind = "image"
		return f, nil
	case st.Size() > MaxText:
		f.Kind = "large"
		return f, nil
	}
	fh, err := os.Open(full)
	if err != nil {
		return File{}, err
	}
	defer fh.Close()
	data, err := io.ReadAll(io.LimitReader(fh, MaxText+1))
	if err != nil {
		return File{}, err
	}
	switch {
	case len(data) > MaxText:
		f.Kind = "large" // it grew since
	case service.IsBinary(data):
		f.Kind = "binary"
	default:
		f.Kind, f.Text = "text", string(data)
	}
	return f, nil
}
