package web

import "time"

// SetSweepEvery checks open WebSockets' devices every d, until undone.
func SetSweepEvery(d time.Duration) (undo func()) {
	was := sweepEvery
	sweepEvery = d
	return func() { sweepEvery = was }
}
