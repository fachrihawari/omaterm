package daemon

import "os"

// lock opens path; Windows runs no daemon, so it locks nothing.
func lock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
}
