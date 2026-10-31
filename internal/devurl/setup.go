// The setup that makes https://<name>.localhost open without a warning: the
// store locations and commands per system follow mkcert's truststore
// (https://github.com/FiloSottile/mkcert, BSD-3-Clause; see
// THIRD_PARTY_NOTICES.md), written again for omaterm's one authority.

package devurl

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// nssName is what the authority is called in a certificate database.
	nssName = "omaterm development CA"
	// nssTrust marks it a certificate authority trusted for websites.
	nssTrust = "C,,"

	sysctlFile = "/etc/sysctl.d/50-omaterm.conf"
	sysctlProc = "/proc/sys/net/ipv4/ip_unprivileged_port_start"
	sysctlKey  = "net.ipv4.ip_unprivileged_port_start"
	// firstPort is the lowest port a program may listen on once set up: 443,
	// the one https uses.
	firstPort = 443
)

// ErrUnsupported is what setup says on a system it has no way for.
var ErrUnsupported = errors.New("Dev URLs setup isn't available on this system yet (Linux and macOS only)")

// Env is what setup reads and runs against, so that tests can aim it at a
// temporary root and fake tools on the PATH. DefaultEnv is the real one.
type Env struct {
	OS string
	// Home is the user's home folder, where the browsers keep their
	// certificate databases.
	Home string
	// Root is put before the system paths setup reads and writes (/etc,
	// /proc, /usr): "" for the real ones.
	Root string
	// CADir is where the authority is kept.
	CADir string
	// Geteuid is the user the process runs as: the system step needs root.
	Geteuid func() int
	// SystemPool is the certificates the system trusts.
	SystemPool func() (*x509.CertPool, error)
	// LookupHost resolves a name, as a program's resolver does.
	LookupHost func(host string) ([]string, error)
	// Listen binds an address, as net.Listen: on macOS the port check
	// binds 443 to tell whether the proxy may.
	Listen func(network, addr string) (net.Listener, error)
	// Out is where progress is told.
	Out io.Writer
}

// DefaultEnv is the real system, with the authority kept in caDir.
func DefaultEnv(caDir string) Env {
	home, _ := os.UserHomeDir()
	return Env{OS: runtime.GOOS, Home: home, CADir: caDir, Geteuid: os.Geteuid,
		SystemPool: x509.SystemCertPool, LookupHost: net.LookupHost, Listen: net.Listen, Out: os.Stdout}
}

func (e Env) path(p string) string { return filepath.Join(e.Root, p) }

// CAPath is the authority's certificate file.
func (e Env) CAPath() string { return filepath.Join(e.CADir, caFile) }

func (e Env) say(format string, args ...any) {
	if e.Out != nil {
		fmt.Fprintf(e.Out, format+"\n", args...)
	}
}

// short is p with the home folder written as ~.
func (e Env) short(p string) string {
	if e.Home != "" && (p == e.Home || strings.HasPrefix(p, e.Home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(p, e.Home)
	}
	return p
}

// Change is one thing setup does, in the words the user is shown before
// they say yes.
type Change struct {
	// ID tells it: "ca", "nss:<dir>", "trust" or "sysctl".
	ID   string `json:"id"`
	Text string `json:"text"`
	// Admin is whether it needs the administrator's password.
	Admin bool `json:"admin"`
	// Blocked is why it can't be done here, when it can't.
	Blocked string `json:"blocked,omitempty"`
}

// Plan lists what setup would do on this system, leaving out what is
// already done.
func Plan(e Env) ([]Change, error) {
	if e.OS != "linux" && e.OS != "darwin" {
		return nil, ErrUnsupported
	}
	var plan []Change
	ca, caErr := load(e.CADir)
	if caErr != nil {
		plan = append(plan, Change{ID: "ca", Text: "Make omaterm's certificate authority in " + e.short(e.CADir)})
	}
	_, haveCertutil := exec.LookPath("certutil")
	for _, db := range NSSDBs(e) {
		if caErr == nil && haveCertutil == nil && db.has(e) {
			continue
		}
		c := Change{ID: "nss:" + db.Dir, Text: fmt.Sprintf("Trust it in %s (%s)", db.Label, e.short(db.Dir))}
		if haveCertutil != nil {
			c.Blocked = e.certutilMissing()
		}
		plan = append(plan, c)
	}
	st, stErr := e.trustStore()
	if caErr != nil || !e.systemTrusts(ca) {
		c := Change{ID: "trust", Admin: true}
		if stErr != nil {
			c.Text, c.Blocked = "Trust it system-wide", stErr.Error()
		} else {
			c.Text = "Trust it system-wide (" + st.tool + ")"
		}
		plan = append(plan, c)
	}
	if e.OS == "linux" && e.portStart() > firstPort {
		plan = append(plan, Change{ID: "sysctl", Admin: true, Text: fmt.Sprintf(
			"Let programs listen on ports %d and up: %s sets %s = %d. Any program you run can then use ports %d–1023.",
			firstPort, sysctlFile, sysctlKey, firstPort, firstPort)})
	}
	return plan, nil
}

// certutilMissing says certutil isn't there, and where it comes from.
func (e Env) certutilMissing() string {
	if e.OS == "darwin" {
		return "certutil isn't installed (brew install nss)"
	}
	return "certutil isn't installed (the nss package; libnss3-tools on Debian)"
}

// ApplyUser does the changes of plan that need no password: making the
// authority and trusting it in the browsers' databases. It goes on after a
// failure, and returns what failed.
func ApplyUser(e Env, plan []Change) error {
	var errs []error
	for _, c := range plan {
		if c.Admin || c.Blocked != "" {
			continue
		}
		switch {
		case c.ID == "ca":
			if _, err := LoadOrCreate(e.CADir); err != nil {
				errs = append(errs, fmt.Errorf("making the certificate authority: %w", err))
			} else {
				e.say("✓ %s", c.Text)
			}
		case strings.HasPrefix(c.ID, "nss:"):
			dir := strings.TrimPrefix(c.ID, "nss:")
			if err := nssAdd(e, dir); err != nil {
				errs = append(errs, fmt.Errorf("trusting it in %s: %w", e.short(dir), err))
			} else {
				e.say("✓ %s", c.Text)
			}
		}
	}
	return errors.Join(errs...)
}

// portStart is the lowest port a program may listen on without privilege,
// 1024 when the system doesn't say.
func (e Env) portStart() int {
	b, err := os.ReadFile(e.path(sysctlProc))
	if err != nil {
		return 1024
	}
	var n int
	if _, err := fmt.Sscan(strings.TrimSpace(string(b)), &n); err != nil {
		return 1024
	}
	return n
}

// systemTrusts tells whether a certificate the authority signs verifies
// against the certificates the system trusts.
func (e Env) systemTrusts(ca *CA) bool {
	if ca == nil || e.SystemPool == nil {
		return false
	}
	pool, err := e.SystemPool()
	if err != nil || pool == nil {
		return false
	}
	leaf, err := ca.Leaf("check.localhost")
	if err != nil {
		return false
	}
	_, err = leaf.Leaf.Verify(x509.VerifyOptions{DNSName: "check.localhost", Roots: pool})
	return err == nil
}

// run runs a command, and says what it printed when it fails.
func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
