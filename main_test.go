package main

import (
	"os"
	"testing"
)

// TestMain pins what tests see of the machine: no Omarchy theme, so every
// test and screenshot paints with the built-in dark theme, and an empty
// config directory, so the developer's projects, settings and window
// layout never leak into a test.
func TestMain(m *testing.M) {
	os.Setenv("OMATERM_OMARCHY_DIR", os.DevNull+"-omarchy")
	cfg, err := os.MkdirTemp("", "omaterm-test-config-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", cfg)
	code := m.Run()
	os.RemoveAll(cfg)
	os.Exit(code)
}
