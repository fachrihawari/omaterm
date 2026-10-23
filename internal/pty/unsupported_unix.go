//go:build darwin || linux

package pty

import "errors"

// ErrUnsupported is Start's error where the app does not own terminals;
// never returned here.
var ErrUnsupported = errors.New("pty: not supported on this system")
