package main

import (
	"path/filepath"

	"omaterm/internal/paths"
	"omaterm/internal/voice"
)

// platformRecorder records with the first of voice.Recorders on the PATH,
// into a file of the state folder's, removed once it is read.
func platformRecorder(*appState) (recording, error) {
	return voice.Record(filepath.Join(paths.StateDir(), "recordings"))
}
