package devurl

import (
	"crypto/x509"
	"errors"
	"strings"
	"testing"
)

func checkNamed(checks []Check, name string) *Check {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}

func TestChecksAllGood(t *testing.T) {
	newFakeTools(t, "certutil")
	e := testEnv(t)
	makeNSS(t, e)
	ca, _ := LoadOrCreate(e.CADir)
	pool := x509.NewCertPool()
	pool.AddCert(ca.Certificate())
	e.SystemPool = func() (*x509.CertPool, error) { return pool, nil }
	putFile(t, e.path(sysctlProc), "443\n")
	plan, _ := Plan(e)
	if err := ApplyUser(e, plan); err != nil {
		t.Fatal(err)
	}
	checks := Checks(e, ProxyState{Running: true, Addr: "127.0.0.1:443"})
	if len(checks) != 6 {
		t.Fatalf("%d checks: %+v", len(checks), checks)
	}
	for _, c := range checks {
		if !c.OK {
			t.Errorf("%s isn't ok: %s", c.Name, c.Detail)
		}
	}
}

func TestChecksEachFailure(t *testing.T) {
	newFakeTools(t, "certutil")
	e := testEnv(t)
	makeNSS(t, e)
	// Nothing is set up: no authority, nothing trusted, a closed port.
	checks := Checks(e, ProxyState{Error: "listen tcp 127.0.0.1:443: permission denied"})
	want := map[string]string{
		"Certificate authority made":    "not yet",
		"Proxy serving":                 "permission denied",
		"Trusted by the system":         "no authority yet",
		"Trusted by Chrome and Firefox": "not in Chrome and Chromium, Firefox profile abc.default",
		"Port 443 can be used":          "below 1024",
	}
	for name, detail := range want {
		c := checkNamed(checks, name)
		if c == nil || c.OK || !strings.Contains(c.Detail, detail) {
			t.Errorf("%s: %+v, want not ok with %q", name, c, detail)
		}
	}
	if c := checkNamed(checks, "*.localhost names resolve"); c == nil || !c.OK {
		t.Errorf("resolve: %+v", c)
	}

	// An authority the system doesn't have, and databases without it.
	newCA(t, e)
	checks = Checks(e, ProxyState{})
	if c := checkNamed(checks, "Certificate authority made"); !c.OK {
		t.Errorf("%+v", c)
	}
	if c := checkNamed(checks, "Trusted by the system"); c.OK || c.Detail != "not in the system's store" {
		t.Errorf("%+v", c)
	}
	if c := checkNamed(checks, "Proxy serving"); c.OK || c.Detail != "not running" {
		t.Errorf("%+v", c)
	}
}

func TestChecksNamesAndNSS(t *testing.T) {
	newFakeTools(t) // no certutil
	e := testEnv(t)
	for name, lookup := range map[string]func(string) ([]string, error){
		"lookup error":   func(string) ([]string, error) { return nil, errors.New("no such host") },
		"not loopback":   func(string) ([]string, error) { return []string{"10.0.0.7"}, nil },
		"nothing":        func(string) ([]string, error) { return nil, nil },
		"one not either": func(string) ([]string, error) { return []string{"127.0.0.1", "192.168.1.1"}, nil },
	} {
		e.LookupHost = lookup
		if c := checkNamed(Checks(e, ProxyState{}), "*.localhost names resolve"); c.OK || c.Detail == "" {
			t.Errorf("%s: %+v", name, c)
		}
	}
	// No databases, no check; databases without certutil, a failing one.
	if checkNamed(Checks(e, ProxyState{}), "Trusted by Chrome and Firefox") != nil {
		t.Error("an NSS check on a machine with no database")
	}
	makeNSS(t, e)
	if c := checkNamed(Checks(e, ProxyState{}), "Trusted by Chrome and Firefox"); c == nil || c.OK || !strings.Contains(c.Detail, "certutil") {
		t.Errorf("%+v", c)
	}
	// Port 443 is fine when the proxy has it, or on macOS.
	if c := checkNamed(Checks(e, ProxyState{Running: true, Addr: "127.0.0.1:443"}), "Port 443 can be used"); !c.OK {
		t.Errorf("%+v", c)
	}
	e.OS = "darwin"
	if c := checkNamed(Checks(e, ProxyState{}), "Port 443 can be used"); !c.OK {
		t.Errorf("%+v", c)
	}
}
