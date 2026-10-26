//go:build !unix

package paths

import "os"

// ownPrivateDir tells whether dir is a folder; there are no Unix owners
// to check here.
func ownPrivateDir(dir string) bool {
	st, err := os.Lstat(dir)
	return err == nil && st.IsDir()
}
