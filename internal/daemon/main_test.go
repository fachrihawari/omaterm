package daemon

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"omaterm/internal/devurl"
)

// TestMain gives the tests an empty config directory: a daemon with no
// state file of its own reads state.json and web.json there, and must not
// read the developer's, nor serve web access on their port. The proxy
// takes a free port, not 443.
func TestMain(m *testing.M) {
	// The Dev URLs tests run the test binary as the dev server a process
	// runs: it answers with the PORT it was told.
	if os.Getenv("OMATERM_TEST_HTTP") != "" {
		port := os.Getenv("PORT")
		http.ListenAndServe("127.0.0.1:"+port, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "port=%s host=%s proto=%s", port, r.Host, r.Header.Get("X-Forwarded-Proto"))
		}))
		os.Exit(1)
	}
	defaultProxyAddr = "127.0.0.1:0"
	cfg, err := os.MkdirTemp("", "omaterm-test-config-")
	if err != nil {
		panic(err)
	}
	// The Dev URLs checks look at a machine with nothing on it, not at the
	// developer's browsers.
	defaultSetupEnv = func(caDir string) devurl.Env {
		return devurl.Env{OS: "linux", Home: filepath.Join(cfg, "home"), Root: filepath.Join(cfg, "root"), CADir: caDir,
			Geteuid: func() int { return 1000 }}
	}
	os.Setenv("XDG_CONFIG_HOME", cfg)
	code := m.Run()
	os.RemoveAll(cfg)
	os.Exit(code)
}
