// Package paths says where omaterm keeps its files: its config (state.json,
// settings.json, ui.json), its state (the omaterm command panes run,
// pane histories) and its runtime files (the control socket).
package paths

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
)

// Name is the app's directory name; OMATERM_DEV=1 names a development
// build's apart, so it runs beside the installed one.
func Name() string {
	if os.Getenv("OMATERM_DEV") == "1" {
		return "omaterm-dev"
	}
	return "omaterm"
}

// Home is the user's home directory.
func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return h
}

// ConfigDir holds state.json, settings.json and ui.json. OMATERM_CONFIG_HOME
// stands in for the OS's config folder, as XDG_CONFIG_HOME does on Linux:
// macOS's ignores XDG_CONFIG_HOME, so without it tests and scripts there
// would use the developer's real config. macOS doesn't follow
// XDG_CONFIG_HOME itself: an app opened from Finder lacks it while a shell
// may set it, and the two would keep different configs.
func ConfigDir() string {
	if d := os.Getenv("OMATERM_CONFIG_HOME"); d != "" {
		return filepath.Join(d, Name())
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(Home(), ".config")
	}
	return filepath.Join(dir, Name())
}

// StateFile is state.json: the projects and their sessions.
func StateFile() string { return filepath.Join(ConfigDir(), "state.json") }

// StateDir holds what the app keeps but nobody edits: the omaterm command
// panes run (bin/), pane histories (sessions/), logs.
func StateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, Name())
	}
	return filepath.Join(Home(), ".local", "state", Name())
}

// BinDir holds a link to the omaterm executable, which panes have on their
// PATH and agents' hooks run.
func BinDir() string { return filepath.Join(StateDir(), "bin") }

// RuntimeDir holds the control socket: $XDG_RUNTIME_DIR, only its user's.
func RuntimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, Name())
	}
	return ConfigDir()
}

// DaemonSocketPath is the session daemon's socket, beside the app's:
// $OMATERM_DAEMON_SOCKET, else the runtime directory's. Panes are given the
// variable, so a shell whose own XDG_RUNTIME_DIR differs from the app's
// still finds the daemon the app uses.
func DaemonSocketPath() string {
	if p := os.Getenv("OMATERM_DAEMON_SOCKET"); p != "" {
		return p
	}
	p := filepath.Join(RuntimeDir(), Name()+"-daemon.sock")
	if len(p) > 100 {
		h := fnv.New32a()
		h.Write([]byte(RuntimeDir()))
		p = filepath.Join(privateTempDir(), Name()+"d-"+strconv.FormatUint(uint64(h.Sum32()), 36)+".sock")
	}
	return p
}

// SocketPath is the control socket: $OMATERM_SOCKET, else the runtime
// directory's. A path too long for a Unix socket goes to a folder of
// this user's in /tmp, named for its directory.
func SocketPath() string {
	if p := os.Getenv("OMATERM_SOCKET"); p != "" {
		return p
	}
	p := filepath.Join(RuntimeDir(), Name()+".sock")
	if len(p) > 100 {
		h := fnv.New32a()
		h.Write([]byte(RuntimeDir()))
		p = filepath.Join(privateTempDir(), Name()+"-"+strconv.FormatUint(uint64(h.Sum32()), 36)+".sock")
	}
	return p
}
