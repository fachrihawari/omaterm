package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	s := OpenStore(filepath.Join(t.TempDir(), "web.json"))
	s.now = func() time.Time { return now }
	return s, &now
}

// A code pairs one device, once; its cookie signs that device in.
func TestPairOnce(t *testing.T) {
	s, _ := testStore(t)
	c, _, err := s.NewCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(c) != 9 || c[4] != '-' {
		t.Fatalf("code %q, want XXXX-XXXX", c)
	}
	// Typed in lower case, without the dash.
	cookie, d, err := s.Pair(strings.ToLower(strings.ReplaceAll(c, "-", "")), "Phone")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Check(cookie); !ok || got.ID != d.ID || got.Name != "Phone" {
		t.Fatalf("cookie does not sign in: %v %v", got, ok)
	}
	if _, _, err := s.Pair(c, "Again"); err != ErrBadCode {
		t.Fatalf("code paired twice: %v", err)
	}
	// Only the secret's hash is kept.
	b, _ := os.ReadFile(s.path)
	_, secret, _ := strings.Cut(cookie, ".")
	if strings.Contains(string(b), secret) {
		t.Fatal("the cookie's secret is on disk")
	}
	if fi, _ := os.Stat(s.path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("web.json is %v, want 0600", fi.Mode().Perm())
	}
}

func TestBadCookies(t *testing.T) {
	s, _ := testStore(t)
	c, _, _ := s.NewCode()
	cookie, _, _ := s.Pair(c, "Phone")
	id, _, _ := strings.Cut(cookie, ".")
	for _, bad := range []string{"", ".", id, id + ".", id + ".00", "x." + strings.Repeat("0", 64)} {
		if _, ok := s.Check(bad); ok {
			t.Errorf("%q signs in", bad)
		}
	}
}

// Codes expire after ten minutes; at most three wait at once.
func TestCodesExpire(t *testing.T) {
	s, now := testStore(t)
	old, _, _ := s.NewCode()
	*now = now.Add(codeTTL + time.Second)
	if _, _, err := s.Pair(old, "Late"); err != ErrBadCode {
		t.Fatalf("expired code paired: %v", err)
	}
	first, _, _ := s.NewCode()
	for range maxCodes {
		s.NewCode()
	}
	if _, _, err := s.Pair(first, "Phone"); err != ErrBadCode {
		t.Fatalf("a fourth code left the first working: %v", err)
	}
}

// A device unused for thirty days is signed out; one in use stays.
func TestDevicesExpire(t *testing.T) {
	s, now := testStore(t)
	c, _, _ := s.NewCode()
	cookie, _, _ := s.Pair(c, "Phone")
	*now = now.Add(deviceTTL - time.Hour)
	if _, ok := s.Check(cookie); !ok {
		t.Fatal("signed out too soon")
	}
	*now = now.Add(deviceTTL - time.Hour)
	if _, ok := s.Check(cookie); !ok {
		t.Fatal("use did not keep it signed in")
	}
	*now = now.Add(deviceTTL + time.Hour)
	if _, ok := s.Check(cookie); ok {
		t.Fatal("still signed in after thirty idle days")
	}
}

func TestRevoke(t *testing.T) {
	s, _ := testStore(t)
	c1, _, _ := s.NewCode()
	k1, d1, _ := s.Pair(c1, "Phone")
	c2, _, _ := s.NewCode()
	k2, _, _ := s.Pair(c2, "Laptop")
	if gone, err := s.Revoke(d1.ID); !gone || err != nil {
		t.Fatalf("revoke: %v %v", gone, err)
	}
	if _, ok := s.Check(k1); ok {
		t.Fatal("revoked device still signs in")
	}
	if _, ok := s.Check(k2); !ok {
		t.Fatal("revoking one signed out the other")
	}
	waiting, _, _ := s.NewCode()
	s.Revoke("all")
	if ds, _ := s.Devices(); len(ds) != 0 {
		t.Fatalf("devices after revoke all: %v", ds)
	}
	if _, _, err := s.Pair(waiting, "X"); err != ErrBadCode {
		t.Fatal("revoke all left a code working")
	}
}

// Two stores of one file (the app's and the daemon's, say) take turns: a
// device one revoked was written back by the other's read-modify-write.
func TestStoresTakeTurns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web.json")
	a, b := OpenStore(path), OpenStore(path)
	unlock := a.lock()
	done := make(chan struct{})
	go func() {
		b.Revoke("all")
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("another store changed the file while one held it")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the other store never got its turn")
	}
}
