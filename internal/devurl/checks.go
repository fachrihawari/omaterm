package devurl

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// Check is one thing Dev URLs needs, and whether it is so.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// ProxyState is how the proxy is, which only the daemon knows.
type ProxyState struct {
	Running bool
	Addr    string
	Error   string
}

// Checks tell what of Dev URLs works: the authority, the proxy, the trust
// of the system and of the browsers, names and the port.
func Checks(e Env, proxy ProxyState) []Check {
	ca, caErr := load(e.CADir)
	var checks []Check

	if caErr != nil {
		checks = append(checks, Check{Name: "Certificate authority made", Detail: "not yet"})
	} else {
		checks = append(checks, Check{Name: "Certificate authority made", OK: true, Detail: e.short(e.CAPath())})
	}

	if proxy.Running {
		checks = append(checks, Check{Name: "Proxy serving", OK: true, Detail: proxy.Addr})
	} else {
		detail := proxy.Error
		if detail == "" {
			detail = "not running"
		}
		checks = append(checks, Check{Name: "Proxy serving", Detail: detail})
	}

	trusted := Check{Name: "Trusted by the system"}
	switch {
	case caErr != nil:
		trusted.Detail = "no authority yet"
	case e.systemTrusts(ca):
		trusted.OK = true
	default:
		trusted.Detail = "not in the system's store"
	}
	checks = append(checks, trusted)

	// The browsers' databases are checked where there are any: a machine
	// with none has no browser that keeps its own. On macOS only Firefox
	// keeps one: Chrome and Safari trust what the System keychain does.
	if dbs := NSSDBs(e); len(dbs) > 0 {
		c := Check{Name: "Trusted by Chrome and Firefox"}
		if e.OS == "darwin" {
			c.Name = "Trusted by Firefox"
		}
		var missing []string
		_, certutilErr := exec.LookPath("certutil")
		for _, db := range dbs {
			if caErr != nil || certutilErr != nil || !db.has(e) {
				missing = append(missing, db.Label)
			}
		}
		switch {
		case certutilErr != nil:
			c.Detail = "certutil isn't installed"
		case len(missing) == 0:
			c.OK = true
		default:
			c.Detail = "not in " + strings.Join(missing, ", ")
		}
		checks = append(checks, c)
	}

	checks = append(checks, e.resolveCheck(), e.portCheck(proxy))
	return checks
}

func (e Env) resolveCheck() Check {
	c := Check{Name: "*.localhost names resolve"}
	problem := e.resolveProblem()
	switch {
	case problem == "":
		c.OK = true
	case e.OS == "darwin":
		// macOS's resolver may not answer for *.localhost, but the browsers
		// resolve those names to this machine themselves.
		c.OK, c.Detail = true, "browsers resolve them themselves (the system's resolver: "+problem+")"
	default:
		c.Detail = problem
	}
	return c
}

// resolveProblem is what is wrong with how a *.localhost name resolves, ""
// when it is this machine.
func (e Env) resolveProblem() string {
	if e.LookupHost == nil {
		return "can't tell"
	}
	addrs, err := e.LookupHost("omaterm-check.localhost")
	if err != nil {
		return err.Error()
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip == nil || !ip.IsLoopback() {
			return "resolves to " + a + ", not this machine"
		}
	}
	if len(addrs) == 0 {
		return "resolves to nothing"
	}
	return ""
}

func (e Env) portCheck(proxy ProxyState) Check {
	c := Check{Name: "Port 443 can be used"}
	switch {
	case proxy.Running && strings.HasSuffix(proxy.Addr, ":443"):
		c.OK, c.Detail = true, "the proxy listens on it"
	case e.OS == "darwin":
		// Nothing tells beforehand whether macOS lets a user have the port:
		// it is bound as the proxy binds it, and let go.
		if err := e.tryPort(); err != nil {
			c.Detail = err.Error()
		} else {
			c.OK = true
		}
	case e.portStart() <= firstPort:
		c.OK = true
	default:
		c.Detail = fmt.Sprintf("programs can't listen below %d (%s)", e.portStart(), sysctlKey)
	}
	return c
}

// tryPort binds port 443 as the proxy does, on 127.0.0.1 or else on every
// address, and closes it.
func (e Env) tryPort() error {
	if e.Listen == nil {
		return errors.New("can't tell")
	}
	ln, err := listenTCP(e.OS, net.JoinHostPort("127.0.0.1", strconv.Itoa(firstPort)), e.Listen)
	if err != nil {
		return err
	}
	return ln.Close()
}
