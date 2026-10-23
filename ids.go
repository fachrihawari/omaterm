package main

import "crypto/rand"

// newID returns a short random id for a project, session or pane: short
// enough to type as a CLI target, from an alphabet without look-alikes.
func newID() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	var b [7]byte
	rand.Read(b[:])
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:])
}
