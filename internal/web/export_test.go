package web

import "time"

// SetSweepEvery checks open WebSockets' devices every d, until undone.
func SetSweepEvery(d time.Duration) (undo func()) {
	was := sweepEvery
	sweepEvery = d
	return func() { sweepEvery = was }
}

// SetPingEvery pings open WebSockets every d, until undone.
func SetPingEvery(d time.Duration) (undo func()) {
	was := pingEvery
	pingEvery = d
	return func() { pingEvery = was }
}
