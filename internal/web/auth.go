// Package web serves omaterm's sessions to a browser: their list, with
// each agent's status, and their panes as live terminals. It listens on
// the loopback unless told to listen on the network; a tunnel the user
// runs (tailscale serve, cloudflared) carries it further, and a device
// must be paired before it sees anything.
package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// codeTTL is how long a pairing code works; maxCodes how many can
	// wait at once, the oldest giving way.
	codeTTL  = 10 * time.Minute
	maxCodes = 3
	// deviceTTL is how long a paired device stays signed in unused.
	deviceTTL = DeviceTTL
	// seenEvery is how often a device's last use is written down.
	seenEvery = time.Hour
)

// DeviceTTL is how long a paired device stays signed in unused.
const DeviceTTL = 30 * 24 * time.Hour

// crockford is the alphabet of pairing codes: no I, L, O or U to misread.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ErrBadCode is a pairing code that is wrong, used or expired.
var ErrBadCode = errors.New("that code is wrong or has expired")

// Device is a paired browser.
type Device struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Created  int64  `json:"created"`
	LastSeen int64  `json:"lastSeen"`
	// Hash is the SHA-256 of the cookie's secret; the secret itself is
	// kept only by the browser.
	Hash string `json:"hash"`
}

type code struct {
	Code    string `json:"code"`
	Expires int64  `json:"expires"`
}

// Config is how web access is set up. It lives here, not in
// settings.json: the omaterm command changes it while the app runs, and
// the app writes its settings whole.
type Config struct {
	Enabled bool `json:"enabled"`
	// Host is the address listened on: empty for the loopback, which only
	// this machine and its tunnels reach, or 0.0.0.0 for the network.
	Host string `json:"host,omitempty"`
	// Port is the port served; 0 is DefaultPort.
	Port int `json:"port,omitempty"`
	// URL is where devices reach it through a tunnel, for pairing links;
	// empty asks Tailscale, else gives the loopback address.
	URL string `json:"url,omitempty"`
}

// ListenAddr is the address to serve.
func (c Config) ListenAddr() string {
	host := c.Host
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(c.ListenPort()))
}

// OnNetwork tells a config that listens beyond the loopback.
func (c Config) OnNetwork() bool {
	if c.Host == "" || c.Host == "localhost" {
		return false
	}
	ip := net.ParseIP(c.Host)
	return ip == nil || !ip.IsLoopback()
}

// ListenPort is the port to serve.
func (c Config) ListenPort() int {
	if c.Port <= 0 || c.Port > 65535 {
		return DefaultPort
	}
	return c.Port
}

type authFile struct {
	Config  Config   `json:"config"`
	Codes   []code   `json:"codes,omitempty"`
	Devices []Device `json:"devices,omitempty"`
}

// Store keeps web access's config, the pairing codes and the paired
// devices in web.json. The omaterm command, the app and the daemon all
// change it, so each call reads it afresh, holding its lock.
type Store struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

// lock orders a change of the store against the others: this process's
// with its mutex, the app's, the daemon's and the omaterm command's with
// a lock on a file beside it. Each read it, changed it and wrote it back,
// so one could write back a device another had just revoked.
func (s *Store) lock() (unlock func()) {
	s.mu.Lock()
	release := fileLock(s.path + ".lock")
	return func() {
		release()
		s.mu.Unlock()
	}
}

// OpenStore returns the store kept at path.
func OpenStore(path string) *Store {
	return &Store{path: path, now: time.Now}
}

// StorePath is where the store lives in a config directory.
func StorePath(configDir string) string { return filepath.Join(configDir, "web.json") }

func (s *Store) load() (authFile, error) {
	var f authFile
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return authFile{}, err
	}
	return f, nil
}

func (s *Store) save(f authFile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".web-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// prune drops expired codes and devices.
func (s *Store) prune(f *authFile) {
	now := s.now()
	codes := f.Codes[:0]
	for _, c := range f.Codes {
		if now.UnixMilli() < c.Expires {
			codes = append(codes, c)
		}
	}
	f.Codes = codes
	devices := f.Devices[:0]
	for _, d := range f.Devices {
		if now.Sub(time.UnixMilli(d.LastSeen)) < deviceTTL {
			devices = append(devices, d)
		}
	}
	f.Devices = devices
}

// Config reads the config.
func (s *Store) Config() (Config, error) {
	defer s.lock()()
	f, err := s.load()
	return f.Config, err
}

// SetConfig changes the config.
func (s *Store) SetConfig(change func(*Config)) (Config, error) {
	defer s.lock()()
	f, err := s.load()
	if err != nil {
		return Config{}, err
	}
	change(&f.Config)
	return f.Config, s.save(f)
}

// NewCode makes a pairing code, written XXXX-XXXX, and says when it
// stops working.
func (s *Store) NewCode() (string, time.Time, error) {
	defer s.lock()()
	f, err := s.load()
	if err != nil {
		return "", time.Time{}, err
	}
	s.prune(&f)
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	for i := range b {
		b[i] = crockford[int(b[i])%len(crockford)]
	}
	expires := s.now().Add(codeTTL)
	f.Codes = append(f.Codes, code{Code: string(b), Expires: expires.UnixMilli()})
	if len(f.Codes) > maxCodes {
		f.Codes = f.Codes[len(f.Codes)-maxCodes:]
	}
	if err := s.save(f); err != nil {
		return "", time.Time{}, err
	}
	return string(b[:4]) + "-" + string(b[4:]), expires, nil
}

// normalize reads a code as typed: any case, with or without its dash,
// and with the letters people mistake for digits.
func normalize(c string) string {
	c = strings.ToUpper(c)
	c = strings.NewReplacer("-", "", " ", "", "O", "0", "I", "1", "L", "1").Replace(c)
	return c
}

// Pair spends a code and signs a device in: it returns the cookie's
// value, which only the browser keeps.
func (s *Store) Pair(typed, name string) (string, Device, error) {
	defer s.lock()()
	f, err := s.load()
	if err != nil {
		return "", Device{}, err
	}
	s.prune(&f)
	typed = normalize(typed)
	found := -1
	for i, c := range f.Codes {
		if subtle.ConstantTimeCompare([]byte(c.Code), []byte(typed)) == 1 {
			found = i
		}
	}
	if found < 0 {
		return "", Device{}, ErrBadCode
	}
	f.Codes = append(f.Codes[:found], f.Codes[found+1:]...)
	id, err := randomHex(8)
	if err != nil {
		return "", Device{}, err
	}
	secret, err := randomHex(32)
	if err != nil {
		return "", Device{}, err
	}
	now := s.now().UnixMilli()
	d := Device{ID: id, Name: name, Created: now, LastSeen: now, Hash: hashSecret(secret)}
	f.Devices = append(f.Devices, d)
	if err := s.save(f); err != nil {
		return "", Device{}, err
	}
	return id + "." + secret, d, nil
}

// Check tells the device a cookie signs in, if any.
func (s *Store) Check(cookie string) (Device, bool) {
	id, secret, ok := strings.Cut(cookie, ".")
	if !ok || id == "" || secret == "" {
		return Device{}, false
	}
	defer s.lock()()
	f, err := s.load()
	if err != nil {
		return Device{}, false
	}
	s.prune(&f)
	want := hashSecret(secret)
	for i := range f.Devices {
		d := &f.Devices[i]
		if d.ID != id || subtle.ConstantTimeCompare([]byte(d.Hash), []byte(want)) != 1 {
			continue
		}
		if now := s.now(); now.Sub(time.UnixMilli(d.LastSeen)) >= seenEvery {
			d.LastSeen = now.UnixMilli()
			s.save(f)
		}
		return *d, true
	}
	return Device{}, false
}

// Devices lists the paired devices.
func (s *Store) Devices() ([]Device, error) {
	defer s.lock()()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	s.prune(&f)
	return f.Devices, nil
}

// Revoke signs a device out, or every device for "all". It reports
// whether any was.
func (s *Store) Revoke(id string) (bool, error) {
	defer s.lock()()
	f, err := s.load()
	if err != nil {
		return false, err
	}
	kept := f.Devices[:0]
	for _, d := range f.Devices {
		if id != "all" && d.ID != id {
			kept = append(kept, d)
		}
	}
	gone := len(kept) < len(f.Devices)
	f.Devices = kept
	if id == "all" {
		f.Codes = nil
	}
	return gone, s.save(f)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}
