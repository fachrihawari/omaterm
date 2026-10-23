package main

import (
	"os"
	"testing"
)

// TestMain pins what tests see of the machine: no Omarchy theme, so every
// test and screenshot paints with the built-in dark theme, whatever theme
// the developer runs.
func TestMain(m *testing.M) {
	os.Setenv("OMATERM_OMARCHY_DIR", os.DevNull+"-omarchy")
	os.Exit(m.Run())
}
