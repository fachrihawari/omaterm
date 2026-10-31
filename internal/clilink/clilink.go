// Package clilink keeps a link to the omaterm executable in omaterm's bin
// directory, which panes have on their PATH and agents' hooks run: it
// stays where it is as updates replace the app.
package clilink

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// Link makes bin/omaterm point at exe, unless it already does. On Windows
// the command is bin\omaterm.exe instead, since cmd and pwsh run only
// executables.
func Link(exe, bin string) error {
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if runtime.GOOS == "windows" {
		return linkExe(exe, filepath.Join(bin, "omaterm.exe"))
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

// linkExe makes link the same file as exe. A hard link needs no privilege,
// unlike a symlink on Windows; where the volumes differ, a copy stands in.
// An update replaces the executable, so the link is then on the old file
// and gets made again.
func linkExe(exe, link string) error {
	if same(exe, link) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	tmp := link + ".tmp"
	os.Remove(tmp)
	if err := os.Link(exe, tmp); err != nil {
		if err := copyFile(exe, tmp); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	return os.Rename(tmp, link)
}

// same tells whether a and b are the same file.
func same(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

// copyFile copies from to to, executable.
func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Target is where bin/omaterm points, "" for nowhere. A hard link has no
// target to read back on Windows, so there it is the file's own path.
func Target(bin string) string {
	if runtime.GOOS == "windows" {
		link := filepath.Join(bin, "omaterm.exe")
		if _, err := os.Stat(link); err == nil {
			return link
		}
		return ""
	}
	t, _ := os.Readlink(filepath.Join(bin, "omaterm"))
	return t
}
