package devurl

import (
	"fmt"
	"net"
	"os/exec"
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
	// with none has no browser that keeps its own.
	if dbs := NSSDBs(e); len(dbs) > 0 {
		c := Check{Name: "Trusted by Chrome and Firefox"}
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
	if e.LookupHost == nil {
		c.Detail = "can't tell"
		return c
	}
	addrs, err := e.LookupHost("omaterm-check.localhost")
	if err != nil {
		c.Detail = err.Error()
		return c
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip == nil || !ip.IsLoopback() {
			c.Detail = "resolves to " + a + ", not this machine"
			return c
		}
	}
	if len(addrs) == 0 {
		c.Detail = "resolves to nothing"
		return c
	}
	c.OK = true
	return c
}

func (e Env) portCheck(proxy ProxyState) Check {
	c := Check{Name: "Port 443 can be used"}
	switch {
	case e.OS == "darwin":
		c.OK = true
	case proxy.Running && strings.HasSuffix(proxy.Addr, ":443"):
		c.OK, c.Detail = true, "the proxy listens on it"
	case e.portStart() <= firstPort:
		c.OK = true
	default:
		c.Detail = fmt.Sprintf("programs can't listen below %d (%s)", e.portStart(), sysctlKey)
	}
	return c
}
