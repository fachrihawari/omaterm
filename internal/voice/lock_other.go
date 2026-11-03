//go:build !unix

package voice

import (
	"os"
)

// tryLock takes path as a lock file, created only if it isn't there: ok
// is false while another holds it.
func tryLock(path string) (unlock func(), ok bool) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if os.IsExist(err) {
		return nil, false
	}
	if err != nil {
		return func() {}, true
	}
	f.Close()
	return func() { os.Remove(path) }, true
}
