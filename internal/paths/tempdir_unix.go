//go:build unix

package paths

import (
	"os"
	"syscall"
)

// ownPrivateDir tells whether dir is a folder, not a link, of this user's
// that no one else may open, making it so if it is merely too open.
func ownPrivateDir(dir string) bool {
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(sys.Uid) != os.Getuid() {
		return false
	}
	if st.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700) == nil
	}
	return true
}
