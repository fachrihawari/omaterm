package paths

import (
	"os"
	"path/filepath"
)

// RemoveLegacy deletes what the first omaterm, built on gpui, left in the
// state folder dir and nothing reads now: its workspace files and history/,
// every shell's history, encrypted next to the key that opens it. It
// returns what it deleted, nothing once they are gone. Its config.toml
// stays: the user may have written in it.
func RemoveLegacy(dir string) []string {
	var gone []string
	for _, name := range []string{"workspace-v1.json", "workspace-recovery-v1.json", "history"} {
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); err != nil {
			continue
		}
		if os.RemoveAll(path) == nil {
			gone = append(gone, path)
		}
	}
	return gone
}
