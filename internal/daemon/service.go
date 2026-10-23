package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"omaterm/internal/paths"
)

// ServiceName is the systemd user unit that runs the daemon, when
// installed.
func ServiceName() string { return paths.Name() + ".service" }

// UnitPath is where the unit is installed.
func UnitPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(paths.Home(), ".config")
	}
	return filepath.Join(dir, "systemd", "user", ServiceName())
}

// Unit is the systemd user unit running exe's daemon for good: it starts
// with the user's session, and comes back if it ends.
func Unit(exe string) string {
	dev := ""
	if paths.Name() != "omaterm" {
		dev = "Environment=OMATERM_DEV=1\n"
	}
	return `[Unit]
Description=omaterm session daemon (keeps terminal sessions running)

[Service]
ExecStart=` + unitQuote(exe) + ` daemon serve
Environment=OMATERM_SERVICE=1
` + dev + `Restart=always
KillMode=mixed

[Install]
WantedBy=default.target
`
}

// unitQuote quotes a path for ExecStart: in double quotes, with systemd's
// specifiers (%) and the quote's own specials escaped.
func unitQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s)
	return `"` + s + `"`
}

// ServiceInstalled tells whether the unit is installed.
func ServiceInstalled() bool {
	_, err := os.Stat(UnitPath())
	return err == nil
}

// systemctl runs systemctl --user.
func systemctl(args ...string) error {
	return exec.Command("systemctl", append([]string{"--user"}, args...)...).Run()
}

// Build names this executable's build: its version and a hash of the
// executable itself, so two builds of one version differ. The app
// compares it with its daemon's to offer restarting sessions in a newer
// daemon.
func Build(version string) string {
	buildOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		f, err := os.Open(exe)
		if err != nil {
			return
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err == nil {
			buildHash = hex.EncodeToString(h.Sum(nil))[:12]
		}
	})
	if buildHash == "" {
		return version
	}
	return version + "+" + buildHash
}

var (
	buildOnce sync.Once
	buildHash string
)

// VersionOf is a build's version, without its hash.
func VersionOf(build string) string {
	v, _, _ := strings.Cut(build, "+")
	return v
}
