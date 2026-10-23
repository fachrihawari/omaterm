package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	return `[Unit]
Description=omaterm session daemon (keeps terminal sessions running)

[Service]
ExecStart=` + unitQuote(exe) + ` daemon serve
Environment=OMATERM_SERVICE=1
Restart=always
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
