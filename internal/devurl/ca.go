// Package devurl serves a project process at https://<name>.localhost: a
// reverse proxy with a local certificate authority of its own, so that the
// browser, once it trusts that authority, shows the page without a warning.
package devurl

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	caFile  = "ca.pem"
	keyFile = "ca-key.pem"

	caLife   = 10 * 365 * 24 * time.Hour
	leafLife = 365 * 24 * time.Hour
	// leafRenew is how soon before a certificate ends a new one is made.
	leafRenew = 7 * 24 * time.Hour
	// maxLeaves bounds the certificates kept in memory.
	maxLeaves = 256
)

// hostLabelRE is one label of a host name: a DNS label, lowercase.
var hostLabelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// CA is the local certificate authority: it signs a certificate for each
// *.localhost name the proxy is asked for.
type CA struct {
	dir  string
	cert *x509.Certificate
	key  *ecdsa.PrivateKey

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// LoadOrCreate loads the authority kept in dir, or makes one there, the key
// readable by its owner only.
func LoadOrCreate(dir string) (*CA, error) {
	ca, err := load(dir)
	if errors.Is(err, os.ErrNotExist) {
		return create(dir)
	}
	return ca, err
}

func load(dir string) (*CA, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, caFile))
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, keyFile))
	if err != nil {
		return nil, err
	}
	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return nil, fmt.Errorf("%s: the certificate or its key is not PEM", dir)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, caFile), err)
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, keyFile), err)
	}
	if pub, ok := cert.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
		return nil, fmt.Errorf("%s: the key is not the certificate's", dir)
	}
	return &CA{dir: dir, cert: cert, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

func create(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "omaterm development CA " + whoAmI(), Organization: []string{"omaterm"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caLife),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		// The authority signs for .localhost only, so a browser that trusts
		// it trusts it for nothing else, should a client honour the limit.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	// The key goes first and the certificate last: a certificate with no
	// key beside it is how load tells a half-made authority from a made one.
	if err := writeFile(filepath.Join(dir, keyFile), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(dir, caFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return nil, err
	}
	return load(dir)
}

// writeFile writes through a temporary file, so a reader never sees half a
// file.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func serialNumber() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 126))
}

// whoAmI is user@host, which tells a certificate list whose authority it is.
func whoAmI() string {
	user := os.Getenv("USER")
	if user == "" {
		user = "user"
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return user + "@" + host
}

// CertPath is the authority's certificate file, which the system's and the
// browsers' trust stores are given.
func (c *CA) CertPath() string { return filepath.Join(c.dir, caFile) }

// Certificate is the authority's certificate.
func (c *CA) Certificate() *x509.Certificate { return c.cert }

// ValidHost tells whether host is a name the authority signs for: labels of
// a-z, 0-9 and -, ending in ".localhost".
func ValidHost(host string) bool {
	host = strings.ToLower(host)
	rest, ok := strings.CutSuffix(host, ".localhost")
	if !ok || rest == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(rest, ".") {
		if !hostLabelRE.MatchString(label) {
			return false
		}
	}
	return true
}

// Leaf is the certificate for host, made at the first ask and kept: its
// name is host and nothing else.
func (c *CA) Leaf(host string) (*tls.Certificate, error) {
	host = strings.ToLower(host)
	if !ValidHost(host) {
		return nil, fmt.Errorf("no certificate for %q: only *.localhost", host)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if l := c.leaves[host]; l != nil && time.Until(l.Leaf.NotAfter) > leafRenew {
		return l, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafLife),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	l := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
	if len(c.leaves) >= maxLeaves {
		c.leaves = map[string]*tls.Certificate{}
	}
	c.leaves[host] = l
	return l, nil
}

// GetCertificate is tls.Config's: the certificate for the name the client
// asked for.
func (c *CA) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return c.Leaf(hello.ServerName)
}
