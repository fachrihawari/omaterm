package devurl

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCACreatedOnceAndLoadedAfter(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "devurl")
	first, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "ca-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v, want 0600", info.Mode().Perm())
	}
	if first.CertPath() != filepath.Join(dir, "ca.pem") {
		t.Fatalf("CertPath %q", first.CertPath())
	}
	cert := first.Certificate()
	if !cert.IsCA || cert.NotAfter.Sub(cert.NotBefore) < 9*365*24*time.Hour {
		t.Fatalf("CA %v: IsCA %v, valid %v to %v", cert.Subject, cert.IsCA, cert.NotBefore, cert.NotAfter)
	}
	second, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Certificate().Equal(cert) {
		t.Fatal("the second load made another CA")
	}
}

func TestCARefusesAKeyThatIsNotTheCertificates(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if _, err := LoadOrCreate(a); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(b); err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(filepath.Join(b, "ca-key.pem"))
	if err := os.WriteFile(filepath.Join(a, "ca-key.pem"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(a); err == nil {
		t.Fatal("a key from another CA was accepted")
	}
}

func TestLeafVerifiesForLocalhostNamesOnly(t *testing.T) {
	ca, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Certificate())
	for _, host := range []string{"shop.localhost", "api.shop.localhost", "SHOP.localhost"} {
		hello := &tls.ClientHelloInfo{ServerName: host}
		leaf, err := ca.GetCertificate(hello)
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		_, err = leaf.Leaf.Verify(x509.VerifyOptions{DNSName: hello.ServerName, Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
		if err != nil {
			t.Fatalf("%s does not verify: %v", host, err)
		}
		if len(leaf.Leaf.DNSNames) != 1 || leaf.Leaf.DNSNames[0] != "shop.localhost" && leaf.Leaf.DNSNames[0] != "api.shop.localhost" {
			t.Fatalf("%s: SAN %v", host, leaf.Leaf.DNSNames)
		}
		if leaf.Leaf.NotAfter.Sub(leaf.Leaf.NotBefore) > 367*24*time.Hour {
			t.Fatalf("%s lives %v", host, leaf.Leaf.NotAfter.Sub(leaf.Leaf.NotBefore))
		}
	}
	for _, host := range []string{"example.com", "", "localhost", ".localhost", "a..localhost", "evil.localhost.example.com", "x_y.localhost", "a b.localhost"} {
		if _, err := ca.GetCertificate(&tls.ClientHelloInfo{ServerName: host}); err == nil {
			t.Errorf("a certificate for %q", host)
		}
	}
}

func TestLeafIsKeptAndSafeForConcurrentUse(t *testing.T) {
	ca, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, _ := ca.Leaf("shop.localhost")
	done := make(chan *tls.Certificate, 8)
	for i := 0; i < 8; i++ {
		go func() { l, _ := ca.Leaf("shop.localhost"); done <- l }()
	}
	for i := 0; i < 8; i++ {
		if l := <-done; l != first {
			t.Fatal("a second certificate was made for the same name")
		}
	}
}
