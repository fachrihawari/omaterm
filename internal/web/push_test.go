package web

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// decrypt opens a push's body as the browser holding key and secret does
// (RFC 8291), padding taken off.
func decrypt(key *ecdh.PrivateKey, secret, body []byte) ([]byte, error) {
	if len(body) < 21 || len(body) < 21+int(body[20]) {
		return nil, errors.New("short header")
	}
	salt, n := body[:16], int(body[20])
	from, err := ecdh.P256().NewPublicKey(body[21 : 21+n])
	if err != nil {
		return nil, err
	}
	shared, err := key.ECDH(from)
	if err != nil {
		return nil, err
	}
	info := "WebPush: info\x00" + string(key.PublicKey().Bytes()) + string(body[21:21+n])
	ikm, _ := hkdf.Key(sha256.New, shared, secret, info, 32)
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	record, err := gcm.Open(nil, nonce, body[21+n:], nil)
	if err != nil {
		return nil, err
	}
	record = bytes.TrimRight(record, "\x00")
	if len(record) == 0 || record[len(record)-1] != 2 {
		return nil, errors.New("no last record's delimiter")
	}
	return record[:len(record)-1], nil
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := unb64(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The encryption gives RFC 8291's own example, Appendix A, byte for byte.
func TestPushEncryptRFC8291(t *testing.T) {
	from, err := ecdh.P256().NewPrivateKey(mustB64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	if got := b64.EncodeToString(from.PublicKey().Bytes()); got != "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8" {
		t.Fatalf("application server's public key %s", got)
	}
	browser := mustB64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	secret := mustB64(t, "BTBZMqHH6r4Tts7J_aSIgg")
	salt := mustB64(t, "DGv6ra1nlYgDCS1FRnbzlw")
	plain := "When I grow up, I want to be a watermelon"
	body, err := encrypt(browser, secret, []byte(plain), 0, from, salt)
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml" +
		"mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT" +
		"pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if got := b64.EncodeToString(body); got != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
	// The browser opens it with its private key.
	key, _ := ecdh.P256().NewPrivateKey(mustB64(t, "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"))
	if got, err := decrypt(key, secret, body); err != nil || string(got) != plain {
		t.Fatalf("decrypted %q, %v", got, err)
	}
}

// A sealed payload is padded to one size, whatever it says.
func TestPushSealPads(t *testing.T) {
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	secret := make([]byte, 16)
	rand.Read(secret)
	sub := Subscription{P256dh: b64.EncodeToString(key.PublicKey().Bytes()), Auth: b64.EncodeToString(secret)}
	short, err := seal(sub, []byte(`{"body":"Done"}`))
	if err != nil {
		t.Fatal(err)
	}
	long, _ := seal(sub, []byte(`{"body":"`+strings.Repeat("x", 600)+`"}`))
	if len(short) != len(long) {
		t.Fatalf("sizes differ: %d and %d", len(short), len(long))
	}
	if got, err := decrypt(key, secret, short); err != nil || string(got) != `{"body":"Done"}` {
		t.Fatalf("decrypted %q, %v", got, err)
	}
}

// The VAPID header carries a JWT for the push service's origin, signed
// with the key it names.
func TestPushVAPIDVerifies(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Unix(1_800_000_000, 0)
	h, err := vapid(key, "https://web.push.apple.com/QGz9", now)
	if err != nil {
		t.Fatal(err)
	}
	token, k, ok := strings.Cut(strings.TrimPrefix(h, "vapid t="), ", k=")
	if !ok || !strings.HasPrefix(h, "vapid t=") {
		t.Fatalf("header %q", h)
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), mustB64(t, k))
	if err != nil || !pub.Equal(&key.PublicKey) {
		t.Fatalf("k is not our key: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token %q", token)
	}
	var head, claims map[string]any
	json.Unmarshal(mustB64(t, parts[0]), &head)
	json.Unmarshal(mustB64(t, parts[1]), &claims)
	if head["alg"] != "ES256" || head["typ"] != "JWT" {
		t.Fatalf("header %v", head)
	}
	if claims["aud"] != "https://web.push.apple.com" || claims["sub"] != vapidSubject ||
		claims["exp"] != float64(now.Add(12*time.Hour).Unix()) {
		t.Fatalf("claims %v", claims)
	}
	sig := mustB64(t, parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(pub, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("the signature does not verify")
	}
}

// Only https endpoints on the push services' hosts are posted to.
func TestPushEndpointAllowed(t *testing.T) {
	for _, e := range []string{
		"https://fcm.googleapis.com/fcm/send/abc:def",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAA",
		"https://web.push.apple.com/QGz9",
		"https://WEB.PUSH.APPLE.COM:443/x",
		"https://wns2-par02p.notify.windows.com/w/?token=AQ",
	} {
		if !allowedEndpoint(e) {
			t.Errorf("refused %s", e)
		}
	}
	for _, e := range []string{
		"http://fcm.googleapis.com/fcm/send/abc",
		"https://fcm.googleapis.com:8443/fcm/send/abc",
		"https://evil.example/fcm.googleapis.com",
		"https://fcm.googleapis.com.evil.example/x",
		"https://push.apple.com/x",
		"https://evilpush.apple.com/x",
		"https://user@web.push.apple.com/x",
		"https://127.0.0.1/x",
		"https://localhost/x",
		"file:///etc/passwd",
		"https://web.push.apple.com/" + strings.Repeat("a", 2100),
		"",
	} {
		if allowedEndpoint(e) {
			t.Errorf("allowed %s", e)
		}
	}
}

// A lock screen gets no control or direction-changing characters, and
// no more than its runes.
func TestPushClean(t *testing.T) {
	if got := clean("rm -rf\x1b[2J /‮txt.exe\n\n  ok now", 200); got != "rm -rf[2J /txt.exe ok now" {
		t.Fatalf("cleaned %q", got)
	}
	if got := clean(strings.Repeat("é", 300), 200); len([]rune(got)) != 200 || !strings.HasSuffix(got, "…") {
		t.Fatalf("capped to %d runes", len([]rune(got)))
	}
}

// By default a notification says the session and its state; the agent's
// words come only to a device that asked for them.
func TestPushPayloadPrivate(t *testing.T) {
	n := Notice{Session: "s1", Pane: "a1", Title: "deploy · web", State: "Needs permission", Words: "Run deploy --token=abc?", Waiting: 2}
	var p payload
	b, _ := payloadFor(n, false, "t")
	json.Unmarshal(b, &p)
	if p.Body != "Needs permission" || p.Title != "deploy · web" || p.Pane != "a1" || p.Waiting != 2 || strings.Contains(string(b), "token") {
		t.Fatalf("private payload %s", b)
	}
	b, _ = payloadFor(n, true, "t")
	json.Unmarshal(b, &p)
	if p.Body != "Needs permission: Run deploy --token=abc?" {
		t.Fatalf("payload with words %s", b)
	}
}

// A device gets pushCap notifications an hour, and none while its push
// service asked to wait.
func TestPushLimits(t *testing.T) {
	now := time.Unix(1000, 0)
	p := newPusher(nil)
	p.now = func() time.Time { return now }
	for i := range pushCap {
		if !p.allow("d1") {
			t.Fatalf("held back at %d", i)
		}
	}
	if p.allow("d1") || !p.allow("d2") {
		t.Fatal("past the cap, or another device held back")
	}
	now = now.Add(time.Hour)
	if !p.allow("d1") {
		t.Fatal("still held an hour on")
	}
	p.wait("d2", "120")
	if p.allow("d2") {
		t.Fatal("pushed while asked to wait")
	}
	now = now.Add(121 * time.Second)
	if !p.allow("d2") {
		t.Fatal("still waiting past Retry-After")
	}
	p.wait("d2", "86400")
	if got := p.until["d2"].Sub(now); got != time.Hour {
		t.Fatalf("waits %v, more than an hour", got)
	}
}

// A subscription lives on its device: reading it leaves the device's last
// use alone, a newer one replaces it, and signing out drops it.
func TestPushStore(t *testing.T) {
	s := OpenStore(filepath.Join(t.TempDir(), "web.json"))
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	code, _, _ := s.NewCode()
	_, d, err := s.Pair(code, "phone")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetPush(d.ID, &Subscription{Endpoint: "https://web.push.apple.com/a"}); !ok || err != nil {
		t.Fatalf("set: %v %v", ok, err)
	}
	now = now.Add(2 * time.Hour)
	if got, _ := s.pushes(); len(got) != 1 || got[0].Push.Endpoint != "https://web.push.apple.com/a" {
		t.Fatalf("pushes %+v", got)
	}
	if got, _ := s.Devices(); got[0].LastSeen != d.LastSeen {
		t.Fatal("reading the pushes renewed the device")
	}
	s.SetPush(d.ID, &Subscription{Endpoint: "https://web.push.apple.com/b"})
	s.dropPush(d.ID, "https://web.push.apple.com/a")
	if got, _ := s.pushes(); len(got) != 1 || got[0].Push.Endpoint != "https://web.push.apple.com/b" {
		t.Fatalf("an old endpoint's 410 dropped the new one: %+v", got)
	}
	k1, err := s.vapidKey()
	if err != nil {
		t.Fatal(err)
	}
	if k2, _ := s.vapidKey(); !k1.Equal(k2) {
		t.Fatal("the VAPID key changed")
	}
	s.Revoke(d.ID)
	if got, _ := s.pushes(); len(got) != 0 {
		t.Fatalf("pushes after sign-out %+v", got)
	}
	if ok, _ := s.SetPush(d.ID, &Subscription{}); ok {
		t.Fatal("a signed-out device subscribed")
	}
}
