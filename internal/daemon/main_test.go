package daemon

import (
	"os"
	"testing"
)

// TestMain gives the tests an empty config directory: a daemon with no
// state file of its own reads state.json and web.json there, and must not
// read the developer's, nor serve web access on their port.
func TestMain(m *testing.M) {
	cfg, err := os.MkdirTemp("", "omaterm-test-config-")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", cfg)
	code := m.Run()
	os.RemoveAll(cfg)
	os.Exit(code)
}
