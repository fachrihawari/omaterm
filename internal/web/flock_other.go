//go:build !unix

package web

// fileLock is a no-op where there is no flock: there the store's mutex
// orders one process's changes only.
func fileLock(string) func() { return func() {} }
