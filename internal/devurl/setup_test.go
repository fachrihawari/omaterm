package devurl

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTools puts scripts for the system's tools first, and alone, on the
// PATH: each logs how it was called, and certutil keeps which databases
// have the authority. No real tool is run.
type fakeTools struct {
	dir, log string
}

func newFakeTools(t *testing.T, names ...string) *fakeTools {
	t.Helper()
	f := &fakeTools{dir: t.TempDir(), log: filepath.Join(t.TempDir(), "calls.log")}
	state := t.TempDir()
	for _, name := range names {
		body := "#!/bin/sh\necho \"" + name + " $*\" >> '" + f.log + "'\n"
		if name == "certutil" {
			body += `m='` + state + `'/$(echo "$2" | /usr/bin/tr '/:' '__')
case "$3" in
-A) : > "$m" ;;
-L) [ -e "$m" ] ;;
-D) /bin/rm -f "$m" ;;
esac
`
		}
		if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", f.dir)
	return f
}

func (f *fakeTools) calls() []string {
	b, _ := os.ReadFile(f.log)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (f *fakeTools) called(prefix string) bool {
	for _, c := range f.calls() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// testEnv is a Linux machine in a temporary root: a home, an empty system
// trust pool and the sysctl at its default.
func testEnv(t *testing.T) Env {
	t.Helper()
	root := t.TempDir()
	e := Env{OS: "linux", Home: filepath.Join(root, "home"), Root: filepath.Join(root, "sys"), CADir: filepath.Join(root, "home", "state", "devurl"),
		Geteuid:    func() int { return 0 },
		SystemPool: func() (*x509.CertPool, error) { return x509.NewCertPool(), nil },
		LookupHost: func(string) ([]string, error) { return []string{"127.0.0.1", "::1"}, nil },
		Out:        &bytes.Buffer{}}
	mkdir(t, e.Home)
	putFile(t, e.path(sysctlProc), "1024\n")
	return e
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func putFile(t *testing.T, path, body string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// layout makes the folder a distribution keeps its anchors in.
func layout(t *testing.T, e Env, kind string) {
	t.Helper()
	dirs := map[string]string{"fedora": "/etc/pki/ca-trust/source/anchors", "debian": "/usr/local/share/ca-certificates",
		"suse": "/usr/share/pki/trust/anchors", "p11kit": "/etc/ca-certificates/trust-source/anchors"}
	mkdir(t, e.path(dirs[kind]))
}

func ids(plan []Change) []string {
	var out []string
	for _, c := range plan {
		out = append(out, c.ID)
	}
	return out
}

func makeNSS(t *testing.T, e Env) (chrome, firefox string) {
	t.Helper()
	chrome = filepath.Join(e.Home, ".pki", "nssdb")
	firefox = filepath.Join(e.Home, ".mozilla", "firefox", "abc.default")
	putFile(t, filepath.Join(chrome, "cert9.db"), "")
	putFile(t, filepath.Join(firefox, "cert9.db"), "")
	return
}

func TestPlanPerDistro(t *testing.T) {
	for _, tt := range []struct {
		kind, tool string
	}{
		{"fedora", "update-ca-trust"}, {"debian", "update-ca-certificates"}, {"suse", "update-ca-certificates"}, {"p11kit", "trust anchor"},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			newFakeTools(t, "certutil")
			e := testEnv(t)
			layout(t, e, tt.kind)
			chrome, firefox := makeNSS(t, e)
			plan, err := Plan(e)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"ca", "nss:" + chrome, "nss:" + firefox, "trust", "sysctl"}
			if got := ids(plan); strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("plan %v, want %v", got, want)
			}
			if !strings.Contains(plan[3].Text, tt.tool) || !plan[3].Admin || plan[3].Blocked != "" {
				t.Errorf("trust: %+v", plan[3])
			}
			if plan[0].Admin || plan[1].Admin || plan[2].Admin || !plan[4].Admin {
				t.Errorf("who needs the password: %+v", plan)
			}
			for _, part := range []string{"ports 443 and up", "/etc/sysctl.d/50-omaterm.conf", "net.ipv4.ip_unprivileged_port_start = 443", "443–1023"} {
				if !strings.Contains(plan[4].Text, part) {
					t.Errorf("sysctl text lacks %q: %s", part, plan[4].Text)
				}
			}
			if !strings.Contains(plan[1].Text, "Chrome and Chromium (~/.pki/nssdb)") || !strings.Contains(plan[2].Text, "Firefox profile abc.default") {
				t.Errorf("nss texts: %q, %q", plan[1].Text, plan[2].Text)
			}
		})
	}
}

func TestPlanBlockedWhereItCannotBeDone(t *testing.T) {
	newFakeTools(t) // no certutil
	e := testEnv(t)
	makeNSS(t, e)
	plan, err := Plan(e) // and no anchor folder
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range plan {
		switch c.ID {
		case "ca", "sysctl":
			if c.Blocked != "" {
				t.Errorf("%s blocked: %s", c.ID, c.Blocked)
			}
		default:
			if c.Blocked == "" {
				t.Errorf("%s isn't blocked", c.ID)
			}
		}
	}
}

func TestPlanLeavesOutWhatIsDone(t *testing.T) {
	newFakeTools(t, "certutil")
	e := testEnv(t)
	layout(t, e, "debian")
	makeNSS(t, e)
	ca, err := LoadOrCreate(e.CADir)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Certificate())
	e.SystemPool = func() (*x509.CertPool, error) { return pool, nil }
	putFile(t, e.path(sysctlProc), "443\n")
	plan, _ := Plan(e)
	if got := ids(plan); len(got) != 2 || !strings.HasPrefix(got[0], "nss:") {
		t.Fatalf("only the databases are left to do: %v", got)
	}
	if err := ApplyUser(e, plan); err != nil {
		t.Fatal(err)
	}
	if plan, _ = Plan(e); len(plan) != 0 {
		t.Fatalf("still to do: %+v", plan)
	}
	// A lower start than 443 is left alone too.
	putFile(t, e.path(sysctlProc), "80\n")
	if plan, _ = Plan(e); len(plan) != 0 {
		t.Fatalf("still to do: %+v", plan)
	}
}

func TestPlanOnMacOSAndWindows(t *testing.T) {
	newFakeTools(t)
	e := testEnv(t)
	e.OS = "darwin"
	plan, err := Plan(e)
	if err != nil || strings.Join(ids(plan), "|") != "ca|trust" || !strings.Contains(plan[1].Text, "System keychain") {
		t.Fatalf("%+v %v", plan, err)
	}
	e.OS = "windows"
	if _, err := Plan(e); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("windows: %v", err)
	}
	if err := ApplySystem(e, "x"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("windows system step: %v", err)
	}
}

func TestApplyUserTrustsEachDatabase(t *testing.T) {
	tools := newFakeTools(t, "certutil")
	e := testEnv(t)
	chrome, firefox := makeNSS(t, e)
	plan, _ := Plan(e)
	if err := ApplyUser(e, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.CAPath()); err != nil {
		t.Fatalf("no authority made: %v", err)
	}
	for _, dir := range []string{chrome, firefox} {
		want := "certutil -d sql:" + dir + ` -A -n omaterm development CA -t C,, -i ` + e.CAPath()
		if !tools.called(want) {
			t.Errorf("no call %q in %v", want, tools.calls())
		}
	}
	// Admin changes are left for the other step.
	if tools.called("trust") || tools.called("sysctl") {
		t.Errorf("the user step ran admin tools: %v", tools.calls())
	}
}

func TestApplyUserGoesOnAfterAFailureAndSaysIt(t *testing.T) {
	newFakeTools(t) // no certutil: the databases fail
	e := testEnv(t)
	makeNSS(t, e)
	err := ApplyUser(e, []Change{{ID: "ca"}, {ID: "nss:" + filepath.Join(e.Home, ".pki", "nssdb")}})
	if err == nil || !strings.Contains(err.Error(), "~/.pki/nssdb") {
		t.Fatalf("error %v", err)
	}
	if _, statErr := os.Stat(e.CAPath()); statErr != nil {
		t.Fatalf("the authority wasn't made: %v", statErr)
	}
}

func newCA(t *testing.T, e Env) []byte {
	t.Helper()
	if _, err := LoadOrCreate(e.CADir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(e.CAPath())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestApplySystemPerDistro(t *testing.T) {
	for _, tt := range []struct {
		kind, anchor string
		calls        []string
	}{
		{"fedora", "/etc/pki/ca-trust/source/anchors/omaterm-dev-ca.pem", []string{"update-ca-trust extract"}},
		{"debian", "/usr/local/share/ca-certificates/omaterm-dev-ca.crt", []string{"update-ca-certificates"}},
		{"suse", "/usr/share/pki/trust/anchors/omaterm-dev-ca.pem", []string{"update-ca-certificates"}},
		{"p11kit", "", []string{"trust anchor --store /", "update-ca-trust"}},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			tools := newFakeTools(t, "trust", "update-ca-trust", "update-ca-certificates", "sysctl")
			e := testEnv(t)
			layout(t, e, tt.kind)
			caPEM := newCA(t, e)
			if err := ApplySystem(e, e.CAPath()); err != nil {
				t.Fatal(err)
			}
			if tt.anchor != "" {
				got, err := os.ReadFile(e.path(tt.anchor))
				if err != nil || !bytes.Equal(got, caPEM) {
					t.Errorf("anchor %s: %v", tt.anchor, err)
				}
			}
			for _, want := range tt.calls {
				if !tools.called(want) {
					t.Errorf("no call %q in %v", want, tools.calls())
				}
			}
			sysctl, err := os.ReadFile(e.path(sysctlFile))
			if err != nil || !strings.Contains(string(sysctl), "net.ipv4.ip_unprivileged_port_start = 443\n") {
				t.Errorf("sysctl file %q: %v", sysctl, err)
			}
			if !tools.called("sysctl -p " + e.path(sysctlFile)) {
				t.Errorf("sysctl not applied: %v", tools.calls())
			}
		})
	}
}

func TestApplySystemLeavesAnOpenerPortStartAlone(t *testing.T) {
	tools := newFakeTools(t, "update-ca-certificates", "sysctl")
	e := testEnv(t)
	layout(t, e, "debian")
	newCA(t, e)
	putFile(t, e.path(sysctlProc), "80\n")
	if err := ApplySystem(e, e.CAPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.path(sysctlFile)); err == nil || tools.called("sysctl") {
		t.Fatalf("the sysctl was set though ports from 80 are open: %v", tools.calls())
	}
}

func TestApplySystemRefusals(t *testing.T) {
	tools := newFakeTools(t, "update-ca-certificates", "sysctl")
	e := testEnv(t)
	layout(t, e, "debian")
	good := newCA(t, e)

	other := func(mutate func(*x509.Certificate)) []byte {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{SerialNumber: bigOne(), Subject: pkix.Name{CommonName: "omaterm development CA x"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
			KeyUsage: x509.KeyUsageCertSign, PermittedDNSDomainsCritical: true, PermittedDNSDomains: []string{"localhost"}}
		mutate(tmpl)
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	ca, _ := load(e.CADir)
	leaf, _ := ca.Leaf("shop.localhost")
	cases := map[string][]byte{
		"a leaf":                 pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}),
		"not a certificate":      []byte("hello"),
		"two certificates":       append(append([]byte{}, good...), good...),
		"a CA with another name": other(func(c *x509.Certificate) { c.Subject.CommonName = "Some Other CA" }),
		"a CA for any name":      other(func(c *x509.Certificate) { c.PermittedDNSDomains, c.PermittedDNSDomainsCritical = nil, false }),
		"a CA for .com":          other(func(c *x509.Certificate) { c.PermittedDNSDomains = []string{"com"} }),
		"an expired CA":          other(func(c *x509.Certificate) { c.NotAfter = time.Now().Add(-time.Minute) }),
		"a CA that isn't one":    other(func(c *x509.Certificate) { c.IsCA = false }),
	}
	for name, data := range cases {
		path := filepath.Join(t.TempDir(), "x.pem")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ApplySystem(e, path); err == nil {
			t.Errorf("%s was installed", name)
		}
	}
	if err := ApplySystem(e, filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Error("a missing file was installed")
	}
	// Not root.
	e.Geteuid = func() int { return 1000 }
	if err := ApplySystem(e, e.CAPath()); err == nil || !strings.Contains(err.Error(), "root") {
		t.Errorf("without root: %v", err)
	}
	if err := RemoveSystem(e, e.CAPath()); err == nil || !strings.Contains(err.Error(), "root") {
		t.Errorf("remove without root: %v", err)
	}
	if _, err := os.Stat(e.path(sysctlFile)); err == nil || tools.called("update-ca-certificates") || tools.called("sysctl") {
		t.Fatalf("a refused step changed the system: %v", tools.calls())
	}
}

func TestUninstallReversesSetup(t *testing.T) {
	tools := newFakeTools(t, "certutil", "update-ca-certificates", "sysctl")
	e := testEnv(t)
	layout(t, e, "debian")
	chrome, firefox := makeNSS(t, e)
	plan, _ := Plan(e)
	if err := ApplyUser(e, plan); err != nil {
		t.Fatal(err)
	}
	if err := ApplySystem(e, e.CAPath()); err != nil {
		t.Fatal(err)
	}
	if !SystemInstalled(e) {
		t.Fatal("setup left no system part")
	}

	if err := RemoveUser(e); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{chrome, firefox} {
		if !tools.called("certutil -d sql:" + dir + " -D -n omaterm development CA") {
			t.Errorf("no removal in %s: %v", dir, tools.calls())
		}
	}
	if err := RemoveSystem(e, e.CAPath()); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/usr/local/share/ca-certificates/omaterm-dev-ca.crt", sysctlFile} {
		if _, err := os.Stat(e.path(p)); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
	if SystemInstalled(e) {
		t.Error("the system part is still there")
	}
	if err := RemoveFiles(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.CADir); err == nil {
		t.Error("the authority's folder is still there")
	}
	// Nothing to remove a second time: the databases don't have it.
	n := len(tools.calls())
	if err := RemoveUser(e); err != nil {
		t.Fatal(err)
	}
	for _, c := range tools.calls()[n:] {
		if strings.Contains(c, " -D ") {
			t.Errorf("removed again: %s", c)
		}
	}
}

func TestRemoveSystemOnP11kitNeedsTheCertificate(t *testing.T) {
	tools := newFakeTools(t, "trust", "update-ca-trust")
	e := testEnv(t)
	layout(t, e, "p11kit")
	newCA(t, e)
	if err := RemoveSystem(e, e.CAPath()); err != nil {
		t.Fatal(err)
	}
	if !tools.called("trust anchor --remove " + e.CAPath()) {
		t.Fatalf("calls %v", tools.calls())
	}
}

func TestMacOSSystemStep(t *testing.T) {
	tools := newFakeTools(t, "security")
	e := testEnv(t)
	e.OS = "darwin"
	newCA(t, e)
	if err := ApplySystem(e, e.CAPath()); err != nil {
		t.Fatal(err)
	}
	if !tools.called("security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ") {
		t.Fatalf("calls %v", tools.calls())
	}
	if _, err := os.Stat(e.path(sysctlFile)); err == nil {
		t.Fatal("a sysctl file on macOS")
	}
	if err := RemoveSystem(e, e.CAPath()); err != nil || !tools.called("security remove-trusted-cert -d "+e.CAPath()) {
		t.Fatalf("%v %v", err, tools.calls())
	}
}

func bigOne() *big.Int { return big.NewInt(1) }
