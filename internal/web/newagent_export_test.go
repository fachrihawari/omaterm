package web

import "time"

// SetCreateWait has create wait d for a worktree before answering 202,
// until undone.
func SetCreateWait(d time.Duration) (undo func()) {
	was := createWait
	createWait = d
	return func() { createWait = was }
}

// SweepUploads is sweepUploads, for tests.
var SweepUploads = sweepUploads
