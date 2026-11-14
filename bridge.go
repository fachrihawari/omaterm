package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"omaterm/internal/clilink"
	"omaterm/internal/daemon"
	"omaterm/internal/paths"
)

// runBridge is `omaterm __bridge`, run on another computer by the app
// over SSH: it connects this process's standard input and output to that
// computer's session daemon, starting the daemon when none runs.
func runBridge() error {
	// Panes on this computer find the omaterm command here: the bridge
	// may be the first omaterm to run, and the app's own bin directory is
	// not made by the installer.
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		// Link even when one is there: an update moves the executable,
		// and the panes need the one that answers now.
		if err := clilink.Link(exe, paths.BinDir()); err != nil {
			fmt.Fprintln(os.Stderr, "omaterm __bridge: linking the command:", err)
		}
	}
	// The app reads this marker to know the host already runs this build:
	// a hand-installed build of another architecture writes it too, on its
	// first connection.
	if err := os.MkdirAll(paths.StateDir(), 0o700); err == nil {
		if err := os.WriteFile(filepath.Join(paths.StateDir(), "build"), []byte(daemon.Build(appVersion())), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "omaterm __bridge: writing the build marker:", err)
		}
	}
	socket := paths.DaemonSocketPath()
	conn, err := net.DialTimeout("unix", socket, 2*time.Second)
	if err != nil {
		// The daemon this bridge starts must know it is a remote one: it
		// owns no sessions, and its app's display and SSH agent are not
		// the ones of the computer at the other end.
		os.Setenv("OMATERM_REMOTE", "1")
		if err := daemon.StartProcess(); err != nil {
			return fmt.Errorf("start the session daemon: %w", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			conn, err = net.DialTimeout("unix", socket, 2*time.Second)
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("the session daemon did not start: %w", err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	defer conn.Close()

	stdinDone := make(chan struct{})
	go func() {
		io.Copy(conn, os.Stdin)
		close(stdinDone)
		// The app has nothing more to say: let the daemon answer what it
		// asked before, then see the end.
		if c, ok := conn.(*net.UnixConn); ok {
			c.CloseWrite()
		} else {
			conn.Close()
		}
	}()
	stdoutDone := make(chan struct{})
	go func() {
		io.Copy(os.Stdout, conn)
		close(stdoutDone)
	}()
	select {
	case <-stdoutDone:
	case <-stdinDone:
		// The daemon may still be writing an answer: give it a moment.
		select {
		case <-stdoutDone:
		case <-time.After(2 * time.Second):
		}
	}
	return nil
}
