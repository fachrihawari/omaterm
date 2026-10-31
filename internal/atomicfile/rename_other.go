//go:build !windows

package atomicfile

import "os"

// rename is os.Rename: Unix replaces an open file's name, and a failure is
// the answer, not something to wait out.
func rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}
