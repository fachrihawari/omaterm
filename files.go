package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/ospath"
)

// newEntry is a file or folder being named in the Files tab, in the
// folder it goes in.
type newEntry struct {
	dir     string // absolute
	folder  bool
	name    string
	focused bool
}

// startNewEntry shows a field for a new file or folder's name in dir,
// opening the folder so the field shows there.
func (a *appState) startNewEntry(dir string, folder bool) {
	if a.workspace == "" {
		return
	}
	if n := a.fileNodeAt(dir); n != nil && n.Dir {
		n.Open = true
		if !n.Loaded {
			loadFileNode(n)
		}
	}
	a.newEntry = &newEntry{dir: dir, folder: folder}
}

// fileNodeAt is the tree's node of an absolute path, or nil.
func (a *appState) fileNodeAt(path string) *fileNode {
	var find func(n *fileNode) *fileNode
	find = func(n *fileNode) *fileNode {
		if n.Path == path {
			return n
		}
		for _, ch := range n.Children {
			if f := find(ch); f != nil {
				return f
			}
		}
		return nil
	}
	if a.fileTree == nil {
		return nil
	}
	return find(a.fileTree)
}

// newEntryRow is the field a new file or folder is named in, at depth.
func (a *appState) newEntryRow(c *ui.Context, depth int) {
	e := a.newEntry
	ui.Row(c.Key("new-entry")).FillWidth().Height(28).Padding(0, 8).PaddingStart(8 + 18*float32(depth)).Gap(4).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Width(12)
		if e.folder {
			ui.Icon(c, iconFolder).Width(18).Height(18).TextColor(pal.Syn.Folder)
		} else {
			fileBadge(c, e.name)
		}
		label := "New file name"
		if e.folder {
			label = "New folder name"
		}
		in := ui.TextInputBase(c.Key("name"), &e.name).FontSize(11).Grow(1).MinWidth(0).Label(label).AutoFocus().
			Border(1, pal.Accent).Radius(4).Padding(2, 4)
		focused := in.Focused()
		// Enter makes it, and a mistake leaves the field to fix it; a
		// click away makes it too, or drops it; Escape drops it.
		switch {
		case c.Shortcut(0, ui.KeyEscape):
			a.newEntry = nil
		case in.Submitted():
			if a.createEntry(c, e) {
				a.newEntry = nil
			}
		case e.focused && !focused && (a.win == nil || a.win.IsFocused()):
			a.createEntry(c, e)
			a.newEntry = nil
		case focused:
			e.focused = true
		}
	})
}

// createEntry makes the file or folder e names, which may hold folders of
// its own (src/a.go), and shows it: a file opens in the editor. Nothing
// typed makes nothing; a name out of the project, or one there already,
// says so. It reports whether e is done with.
func (a *appState) createEntry(c *ui.Context, e *newEntry) bool {
	name := strings.TrimSpace(e.name)
	if name == "" {
		return true
	}
	path := filepath.Join(e.dir, filepath.FromSlash(name))
	if filepath.IsAbs(filepath.FromSlash(name)) || !ospath.Contains(runtime.GOOS, a.workspace, path) || ospath.Same(runtime.GOOS, a.workspace, path) {
		c.Toast(name + " isn't a name in this project")
		return false
	}
	rel, _ := filepath.Rel(a.workspace, path)
	rel = filepath.ToSlash(rel)
	if _, err := os.Lstat(path); err == nil {
		c.Toast(rel + " already exists")
		return false
	}
	var err error
	if e.folder {
		err = os.MkdirAll(path, 0o755)
	} else if err = os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		var f *os.File
		if f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			err = f.Close()
		}
	}
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			c.Toast(rel + " already exists")
		} else {
			c.Toast("Can't create " + rel + ": " + errText(err))
		}
		return false
	}
	a.rebuildFileTree()
	// The folders down to it open, so it shows.
	for dir := path; ospath.Contains(runtime.GOOS, a.workspace, dir) && !ospath.Same(runtime.GOOS, a.workspace, dir); dir = filepath.Dir(dir) {
		if n := a.fileNodeAt(dir); n != nil && n.Dir {
			n.Open = true
			if !n.Loaded {
				loadFileNode(n)
			}
		}
	}
	if !e.folder {
		a.openEditor(rel)
	}
	return true
}
