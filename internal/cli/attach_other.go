//go:build !windows

package cli

// attachConsole does nothing: a Unix command has its terminal already.
func attachConsole() {}
