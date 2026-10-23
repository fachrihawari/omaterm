// Package clilink keeps a link to the omaterm executable in omaterm's bin
// directory, which panes have on their PATH and agents' hooks run: it
// stays where it is as updates replace the app.
package clilink

import (
	"os"
	"path/filepath"
)

// Link makes bin/omaterm point at exe, unless it already does.
func Link(exe, bin string) error {
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	link := filepath.Join(bin, "omaterm")
	if cur, err := os.Readlink(link); err == nil && cur == exe {
		return nil
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	tmp := link + ".tmp"
	os.Remove(tmp)
	if err := os.Symlink(exe, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// Target is where bin/omaterm points, "" for nowhere.
func Target(bin string) string {
	t, _ := os.Readlink(filepath.Join(bin, "omaterm"))
	return t
}
