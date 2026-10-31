package devurl

import (
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	anchorBase         = "omaterm-dev-ca"
	caCommonNamePrefix = "omaterm development CA"
	macSystemKeychain  = "/Library/Keychains/System.keychain"
)

// trustStore is how a system keeps the authorities it trusts.
type trustStore struct {
	kind string // "fedora", "debian", "suse", "p11kit" or "macos"
	// dir and file are where the certificate is copied to, for the kinds
	// that copy it; tool is what the user is told it runs.
	dir, file, tool string
	// update is the command that makes the system read it.
	update []string
}

// trustStore finds how this system trusts an authority, by the folders its
// distribution keeps them in, in the order mkcert looks.
func (e Env) trustStore() (trustStore, error) {
	if e.OS == "darwin" {
		return trustStore{kind: "macos", tool: "the System keychain"}, nil
	}
	for _, s := range []trustStore{
		{kind: "fedora", dir: "/etc/pki/ca-trust/source/anchors", file: anchorBase + ".pem", tool: "update-ca-trust", update: []string{"update-ca-trust", "extract"}},
		{kind: "debian", dir: "/usr/local/share/ca-certificates", file: anchorBase + ".crt", tool: "update-ca-certificates", update: []string{"update-ca-certificates"}},
		{kind: "suse", dir: "/usr/share/pki/trust/anchors", file: anchorBase + ".pem", tool: "update-ca-certificates", update: []string{"update-ca-certificates"}},
		{kind: "p11kit", dir: "/etc/ca-certificates/trust-source/anchors", tool: "trust anchor"},
	} {
		if info, err := os.Stat(e.path(s.dir)); err == nil && info.IsDir() {
			return s, nil
		}
	}
	return trustStore{}, errors.New("can't tell how this system trusts certificate authorities (no known folder for them)")
}

// ValidateCA reads an authority's certificate from PEM, and refuses what
// isn't omaterm's: the privileged step installs it for the whole system, so
// it takes nothing but a certificate authority named as omaterm's that signs
// for .localhost only. It returns the certificate and its PEM, made again
// from the parsed certificate.
func ValidateCA(data []byte) (*x509.Certificate, []byte, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, errors.New("not a PEM certificate")
	}
	if b, _ := pem.Decode(rest); b != nil {
		return nil, nil, errors.New("more than one certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("not a certificate: %w", err)
	}
	switch {
	case !cert.IsCA || !cert.BasicConstraintsValid:
		return nil, nil, errors.New("not a certificate authority")
	case !strings.HasPrefix(cert.Subject.CommonName, caCommonNamePrefix):
		return nil, nil, fmt.Errorf("not omaterm's certificate authority (%q)", cert.Subject.CommonName)
	case !cert.PermittedDNSDomainsCritical || !slices.Equal(cert.PermittedDNSDomains, []string{"localhost"}) || len(cert.ExcludedDNSDomains) > 0:
		return nil, nil, errors.New("not limited to .localhost")
	case cert.CheckSignatureFrom(cert) != nil:
		return nil, nil, errors.New("not self-signed")
	case time.Now().After(cert.NotAfter):
		return nil, nil, errors.New("expired")
	}
	return cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), nil
}

func (e Env) needRoot() error {
	if e.Geteuid == nil || e.Geteuid() != 0 {
		how := "pkexec"
		if e.OS == "darwin" {
			how = "macOS's password dialog"
		}
		return errors.New("this step changes the system and must run as root: omaterm proxy setup does it through " + how + ", or run sudo omaterm proxy setup --system --ca <ca.pem>")
	}
	return nil
}

// ApplySystem is the privileged step of setup, and does nothing else: it
// trusts the authority in caPath in the system's store, and on Linux lets
// programs listen on port 443 and up. It uses no network and writes no other
// file.
func ApplySystem(e Env, caPath string) error {
	if e.OS != "linux" && e.OS != "darwin" {
		return ErrUnsupported
	}
	if err := e.needRoot(); err != nil {
		return err
	}
	data, err := os.ReadFile(caPath)
	if err != nil {
		return err
	}
	_, certPEM, err := ValidateCA(data)
	if err != nil {
		return fmt.Errorf("%s: %w", caPath, err)
	}
	st, err := e.trustStore()
	if err != nil {
		return err
	}
	if err := e.installAnchor(st, certPEM); err != nil {
		return err
	}
	e.say("✓ trusted system-wide (%s)", st.tool)
	if e.OS == "linux" {
		if e.portStart() <= firstPort {
			e.say("✓ ports %d and up are already open to programs", firstPort)
			return nil
		}
		if err := e.writeSysctl(); err != nil {
			return err
		}
		e.say("✓ programs may listen on ports %d and up (%s)", firstPort, sysctlFile)
	}
	return nil
}

// installAnchor puts the certificate where the system reads authorities
// from, and has it read.
func (e Env) installAnchor(st trustStore, certPEM []byte) error {
	switch st.kind {
	case "macos":
		f, err := tempCert(certPEM)
		if err != nil {
			return err
		}
		defer os.RemoveAll(filepath.Dir(f))
		return run("security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", macSystemKeychain, f)
	case "p11kit":
		f, err := tempCert(certPEM)
		if err != nil {
			return err
		}
		defer os.RemoveAll(filepath.Dir(f))
		if err := run("trust", "anchor", "--store", f); err != nil {
			return err
		}
		// Where the bundle isn't made again by the store itself.
		if _, err := exec.LookPath("update-ca-trust"); err == nil {
			return run("update-ca-trust")
		}
		return nil
	}
	dst := e.path(filepath.Join(st.dir, st.file))
	if err := os.WriteFile(dst, certPEM, 0o644); err != nil {
		return err
	}
	return run(st.update[0], st.update[1:]...)
}

// tempCert writes the certificate to a file of its own, in a folder only
// root can read, for the tool that takes a file.
func tempCert(certPEM []byte) (string, error) {
	dir, err := os.MkdirTemp("", "omaterm-ca-")
	if err != nil {
		return "", err
	}
	f := filepath.Join(dir, anchorBase+".pem")
	if err := os.WriteFile(f, certPEM, 0o644); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return f, nil
}

func (e Env) writeSysctl() error {
	const body = "# Written by omaterm proxy setup: lets programs listen on port 443 and up,\n" +
		"# so that https://<name>.localhost needs no port. omaterm proxy uninstall removes it.\n" +
		sysctlKey + " = 443\n"
	path := e.path(sysctlFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	return run("sysctl", "-p", path)
}

// SystemInstalled tells whether what the privileged step does is in place,
// and so whether undoing it needs the password.
func SystemInstalled(e Env) bool {
	if _, err := os.Stat(e.path(sysctlFile)); err == nil {
		return true
	}
	st, err := e.trustStore()
	if err != nil {
		return false
	}
	if st.file != "" {
		_, err := os.Stat(e.path(filepath.Join(st.dir, st.file)))
		return err == nil
	}
	ca, err := load(e.CADir)
	return err == nil && e.systemTrusts(ca)
}

// RemoveSystem undoes ApplySystem: it takes the authority out of the
// system's store and removes the sysctl file. The ports stay open to
// programs until the next boot. caPath is the authority's certificate, which
// the stores that remove by certificate need; it may be gone, for the others.
func RemoveSystem(e Env, caPath string) error {
	if e.OS != "linux" && e.OS != "darwin" {
		return ErrUnsupported
	}
	if err := e.needRoot(); err != nil {
		return err
	}
	var errs []error
	if st, err := e.trustStore(); err == nil {
		switch st.kind {
		case "macos":
			errs = append(errs, e.removeFromKeychain(caPath))
		case "p11kit":
			if _, statErr := os.Stat(caPath); statErr == nil {
				errs = append(errs, run("trust", "anchor", "--remove", caPath))
				if _, err := exec.LookPath("update-ca-trust"); err == nil {
					errs = append(errs, run("update-ca-trust"))
				}
			}
		default:
			err := os.Remove(e.path(filepath.Join(st.dir, st.file)))
			if err == nil {
				errs = append(errs, run(st.update[0], st.update[1:]...))
			} else if !os.IsNotExist(err) {
				errs = append(errs, err)
			}
		}
		e.say("✓ no longer trusted system-wide")
	}
	if err := os.Remove(e.path(sysctlFile)); err != nil && !os.IsNotExist(err) {
		errs = append(errs, err)
	} else if e.OS == "linux" {
		e.say("✓ %s removed (ports below 1024 close again at the next boot)", sysctlFile)
	}
	return errors.Join(errs...)
}

// removeFromKeychain takes the authority's trust setting and its
// certificate out of the System keychain, which keeps the certificate once
// the setting is gone. What is already gone is no failure. Without the
// authority's file nothing tells which certificate is this user's, so the
// user is told where to look.
func (e Env) removeFromKeychain(caPath string) error {
	data, err := os.ReadFile(caPath)
	if os.IsNotExist(err) {
		e.say("the authority's file is gone: if Keychain Access lists %q… in the System keychain, delete it there", caCommonNamePrefix)
		return nil
	}
	if err != nil {
		return err
	}
	// Not ValidateCA: an expired authority is removed too. The name is
	// checked all the same, as this runs as root and deletes by it.
	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("%s: not a PEM certificate", caPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("%s: not a certificate: %w", caPath, err)
	}
	name := cert.Subject.CommonName
	if !strings.HasPrefix(name, caCommonNamePrefix) {
		return fmt.Errorf("%s: not omaterm's certificate authority (%q)", caPath, name)
	}
	var errs []error
	if err := run("security", "remove-trusted-cert", "-d", caPath); err != nil && !keychainNotFound(err) {
		errs = append(errs, err)
	}
	// By its SHA-1, not its name: an older authority of the same name may
	// be there too, which a name doesn't tell apart.
	sum := sha1.Sum(cert.Raw)
	if err := run("security", "delete-certificate", "-Z", strings.ToUpper(hex.EncodeToString(sum[:])), macSystemKeychain); err != nil && !keychainNotFound(err) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// keychainNotFound tells a security command failing on what isn't there:
// a trust setting or a certificate removed before.
func keychainNotFound(err error) bool {
	return strings.Contains(err.Error(), "could not be found") || strings.Contains(err.Error(), "Unable to delete certificate matching")
}

// RemoveUser takes the authority out of the browsers' databases it is in.
func RemoveUser(e Env) error {
	var errs []error
	for _, db := range NSSDBs(e) {
		if !db.has(e) {
			continue
		}
		if err := nssRemove(db.Dir); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.short(db.Dir), err))
		} else {
			e.say("✓ no longer trusted in %s (%s)", db.Label, e.short(db.Dir))
		}
	}
	return errors.Join(errs...)
}

// RemoveFiles deletes the authority and its key.
func RemoveFiles(e Env) error {
	var errs []error
	for _, name := range []string{caFile, keyFile} {
		if err := os.Remove(filepath.Join(e.CADir, name)); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	os.Remove(e.CADir) // only when empty
	return errors.Join(errs...)
}
