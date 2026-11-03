//go:build unix

package voice

import (
	"os"
	"syscall"
)

// tryLock takes an exclusive lock on path, without waiting: ok is false
// while another holds it, in this process or another. The file stays: one
// removed as it is unlocked could be locked twice, through the old and a
// new one.
func tryLock(path string) (unlock func(), ok bool) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, true
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, false
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true
}
