//go:build windows

package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
	"time"
)

const (
	// retryTimeout is how long a rename keeps trying while another
	// program holds the file: a second covers the scan of a fresh temp
	// file and the close of a viewer, and a save that waits longer has
	// failed anyway.
	retryTimeout = time.Second
	// retryPause is short: the holder is usually a scan or a close in
	// progress, not a program that keeps the file for long.
	retryPause = 20 * time.Millisecond
	// errorSharingViolation is ERROR_SHARING_VIOLATION, which syscall
	// doesn't name: the file is open elsewhere.
	errorSharingViolation = syscall.Errno(32)
)

// rename is os.Rename, retried while the file being replaced is open
// elsewhere.
func rename(oldpath, newpath string) error {
	deadline := time.Now().Add(retryTimeout)
	for {
		err := os.Rename(oldpath, newpath)
		if err == nil || !retryable(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(retryPause)
	}
}

// retryable tells whether Windows refused the rename because the file was
// in use, rather than because it can't be replaced at all.
func retryable(err error) bool {
	return errors.Is(err, fs.ErrPermission) ||
		errors.Is(err, errorSharingViolation) ||
		errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
