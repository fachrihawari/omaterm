package web

import (
	"crypto/ecdh"
	"time"
)

// SetSweepEvery checks open WebSockets' devices every d, until undone.
func SetSweepEvery(d time.Duration) (undo func()) {
	was := sweepEvery
	sweepEvery = d
	return func() { sweepEvery = was }
}

// AllowPushOrigin lets subscriptions name a fake push service at origin,
// an http one, until undone. Set it before the server starts.
func AllowPushOrigin(origin string) (undo func()) {
	was := pushOrigin
	pushOrigin = origin
	return func() { pushOrigin = was }
}

// DecryptPush opens a push's body as the browser holding key and secret
// does.
func DecryptPush(key *ecdh.PrivateKey, secret, body []byte) ([]byte, error) {
	return decrypt(key, secret, body)
}

// SetMaxText caps the pane text the page reads at n bytes, until undone.
func SetMaxText(n int) (undo func()) {
	was := maxText
	maxText = n
	return func() { maxText = was }
}

// SetPingEvery pings open WebSockets every d, until undone.
func SetPingEvery(d time.Duration) (undo func()) {
	was := pingEvery
	pingEvery = d
	return func() { pingEvery = was }
}
