package web

import "time"

// SetSweepEvery checks open WebSockets' devices every d, until undone.
func SetSweepEvery(d time.Duration) (undo func()) {
	was := sweepEvery
	sweepEvery = d
	return func() { sweepEvery = was }
}

// SetMaxText caps the pane text the page reads at n bytes, until undone.
func SetMaxText(n int) (undo func()) {
	was := maxText
	maxText = n
	return func() { maxText = was }
}
