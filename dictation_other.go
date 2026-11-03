//go:build !linux && !darwin

package main

// platformRecorder: no desktop voice input here yet.
var platformRecorder func(*appState) (recording, error)
