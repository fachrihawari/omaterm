package main

import (
	"os"
	"testing"
)

// TestMain pins what tests see of the machine: no Omarchy theme, so every
// test and screenshot paints with the built-in dark theme, and an empty
// config directory, so the developer's projects, settings and window
// layout never leak into a test. A pane's daemon socket would lead the
// tests to the developer's daemon.
func TestMain(m *testing.M) {
	os.Setenv("OMATERM_OMARCHY_DIR", os.DevNull+"-omarchy")
	os.Unsetenv("OMATERM_DAEMON_SOCKET")
	cfg, err := os.MkdirTemp("", "omaterm-test-config-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", cfg)
	os.Setenv("OMATERM_CONFIG_HOME", cfg)
	code := m.Run()
	os.RemoveAll(cfg)
	os.Exit(code)
}

// setConfigHome gives a test its own config directory dir. macOS ignores
// XDG_CONFIG_HOME, so OMATERM_CONFIG_HOME is set too; XDG_CONFIG_HOME
// stays for the tools that read it, OpenCode's hooks among them.
func setConfigHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("OMATERM_CONFIG_HOME", dir)
}

// socketDir is a short folder for a test's socket: macOS allows 103 bytes
// for its path, and t.TempDir() there, with a long test's name, is longer.
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "oms")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
