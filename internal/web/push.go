package web

// Web Push, with the standard library alone. A paired phone that asked to
// be notified gets a notification when a session needs the user, fails or
// finishes, while the laptop runs at home: through its browser's push
// service (Google's, Apple's, Mozilla's or Microsoft's), encrypted end to
// end for that browser (RFC 8291) and signed as this omaterm's (VAPID, RFC
// 8292). The services see when, how big and for whom, never what.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"omaterm/internal/notice"
)

// Subscription is where a browser takes its pushes, and the keys they are
// encrypted for.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	// P256dh is the browser's public key and Auth its secret, base64url,
	// as PushSubscription.toJSON gives them.
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
	// Words lets the agent's own words show: without it a notification
	// says only the session and its state, all a lock screen then shows.
	Words bool `json:"words,omitempty"`
}

// Notice is what the phones are told of a session; Server.Push sends it.
type Notice struct {
	// Session is whose it is: a newer notice of the same session replaces
	// an older one, on the way and on the phone.
	Session string
	// Pane is the pane a tap opens, "" for the list.
	Pane string
	// Title is the session, "name · project".
	Title string
	// State is what happened, in the status chip's words: "Needs
	// permission", "Done", "Failed".
	State string
	// Words are the agent's or the program's own, shown after State only
	// to a device that asked for them.
	Words string
	// Program marks a program's own notification (OSC 9, 777), which any
	// program in a pane can send: it never replaces omaterm's.
	Program bool
	// Urgent asks the push service to wake the phone: for a session that
	// waits for the user, or failed.
	Urgent bool
	// Waiting is how many sessions wait for the user, for the badge on the
	// home-screen app's icon.
	Waiting int
}

const (
	// pushCap is how many notifications a device gets in an hour at most.
	pushCap = 30
	// pushTTL is how long a push service keeps one for a phone that is
	// off, in seconds.
	pushTTL = 12 * 60 * 60
	// pushPad is the size every payload is padded to, so that its length
	// tells nothing of what it says.
	pushPad = 2048
	// maxTitle and maxBody cap what a notification says, in runes.
	maxTitle = 100
	maxBody  = 200
	// vapidSubject is who the push services may contact about our pushes:
	// the project, never the user's address.
	vapidSubject = "https://github.com/fachrihawari/omaterm"
)

// pushOrigin is an http origin a test's fake push service listens on,
// which allowedEndpoint takes too.
var pushOrigin string

// allowedEndpoint tells an endpoint omaterm will post to: https, on a push
// service's own host. A paired page gives the endpoint, so without this
// the daemon would post wherever it said.
func allowedEndpoint(endpoint string) bool {
	if len(endpoint) > 2048 {
		return false
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.Opaque != "" || u.Host == "" {
		return false
	}
	if pushOrigin != "" && u.Scheme+"://"+u.Host == pushOrigin {
		return true
	}
	if u.Scheme != "https" || u.Port() != "" && u.Port() != "443" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "fcm.googleapis.com" || strings.HasSuffix(host, ".push.services.mozilla.com") ||
		strings.HasSuffix(host, ".push.apple.com") || strings.HasSuffix(host, ".notify.windows.com")
}

var b64 = base64.RawURLEncoding

// unb64 reads base64url, padded or not, as browsers write it.
func unb64(s string) ([]byte, error) { return b64.DecodeString(strings.TrimRight(s, "=")) }

// checkSubscription tells what is wrong with a subscription a page sent.
func checkSubscription(sub Subscription) error {
	if !allowedEndpoint(sub.Endpoint) {
		return errors.New("not a push service's address")
	}
	key, err := unb64(sub.P256dh)
	if err != nil {
		return errors.New("bad p256dh key")
	}
	if _, err := ecdh.P256().NewPublicKey(key); err != nil {
		return errors.New("bad p256dh key")
	}
	if auth, err := unb64(sub.Auth); err != nil || len(auth) != 16 {
		return errors.New("bad auth secret")
	}
	return nil
}

// encrypt seals a payload for a browser as RFC 8291 says: one aes128gcm
// record, padded to pad bytes, under a key agreed between a key made for
// this message alone (from) and the browser's, mixed with its secret.
func encrypt(p256dh, secret, payload []byte, pad int, from *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	browser, err := ecdh.P256().NewPublicKey(p256dh)
	if err != nil {
		return nil, err
	}
	shared, err := from.ECDH(browser)
	if err != nil {
		return nil, err
	}
	ours := from.PublicKey().Bytes()
	info := "WebPush: info\x00" + string(p256dh) + string(ours)
	ikm, err := hkdf.Key(sha256.New, shared, secret, info, 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// The record: the payload, the last record's delimiter, then zeros.
	record := make([]byte, max(len(payload)+1, pad))
	copy(record, payload)
	record[len(payload)] = 2
	// The header: salt, record size, and our key as the key id.
	header := make([]byte, 0, 16+4+1+len(ours))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, 4096)
	header = append(header, byte(len(ours)))
	header = append(header, ours...)
	return gcm.Seal(header, nonce, record, nil), nil
}

// seal encrypts a payload for a subscription, with a key and salt of its
// own.
func seal(sub Subscription, payload []byte) ([]byte, error) {
	key, err := unb64(sub.P256dh)
	if err != nil {
		return nil, err
	}
	secret, err := unb64(sub.Auth)
	if err != nil {
		return nil, err
	}
	from, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encrypt(key, secret, payload, pushPad, from, salt)
}

// vapid is the Authorization header that signs a push to endpoint as
// ours: a JWT for the push service's origin, signed ES256, and our public
// key.
func vapid(key *ecdsa.PrivateKey, endpoint string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host, "exp": now.Add(12 * time.Hour).Unix(), "sub": vapidSubject,
	})
	if err != nil {
		return "", err
	}
	signed := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`)) + "." + b64.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signed))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	pub, err := key.PublicKey.Bytes()
	if err != nil {
		return "", err
	}
	return "vapid t=" + signed + "." + b64.EncodeToString(sig) + ", k=" + b64.EncodeToString(pub), nil
}

// topic names a session's notices to the push services, which keep only
// the newest of a topic for a phone that is off. It is a keyed hash: they
// never see the session's id.
func topic(key []byte, kind, session string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(kind + ":" + session))
	return b64.EncodeToString(m.Sum(nil)[:24])
}

// clean makes text fit for a lock screen: no control characters, nor the
// ones that turn text around to hide what it says, its spaces collapsed,
// and at most limit runes.
func clean(s string, limit int) string {
	var out []rune
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = len(out) > 0
			continue
		case unicode.IsControl(r), unicode.Is(unicode.Bidi_Control, r), r == unicode.ReplacementChar:
			continue
		}
		if space {
			out = append(out, ' ')
			space = false
		}
		out = append(out, r)
	}
	if len(out) > limit {
		out = append(out[:limit-1], '…')
	}
	return string(out)
}

// payload is what the service worker shows (sw.js).
type payload struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Tag     string `json:"tag"`
	Pane    string `json:"pane,omitempty"`
	Waiting int    `json:"waiting"`
}

// payloadFor is a notice as a device shows it: the state alone, or with
// the words after it for a device that asked for them.
func payloadFor(n Notice, words bool, tag string) ([]byte, error) {
	body := n.State
	if w := clean(n.Words, maxBody); words && w != "" {
		body += ": " + w
	}
	p := payload{Title: clean(n.Title, maxTitle), Body: clean(body, maxBody), Tag: tag, Pane: n.Pane, Waiting: n.Waiting}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(b.Bytes()), nil
}

// --- the store's part --------------------------------------------------------

// errNoVAPID is a store with no key for signing pushes yet.
var errNoVAPID = errors.New("notifications aren't set up yet: turn web access off and on")

// vapidKey is this omaterm's key for signing its pushes, kept in
// web.json; create makes it when there is none, as web access starts.
func (s *Store) vapidKey(create bool) (*ecdsa.PrivateKey, error) {
	defer s.lock()()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	if f.VAPID != "" {
		raw, err := unb64(f.VAPID)
		if err != nil {
			return nil, err
		}
		return ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	}
	if !create {
		return nil, errNoVAPID
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	raw, err := key.Bytes()
	if err != nil {
		return nil, err
	}
	f.VAPID = b64.EncodeToString(raw)
	return key, s.save(f)
}

// SetPush keeps a device's subscription, in place of the one it had, or
// drops it for nil. It reports whether the device is still paired.
func (s *Store) SetPush(device string, sub *Subscription) (bool, error) {
	defer s.lock()()
	f, err := s.load()
	if err != nil {
		return false, err
	}
	for i := range f.Devices {
		if f.Devices[i].ID == device {
			f.Devices[i].Push = sub
			return true, s.save(f)
		}
	}
	return false, nil
}

// pushes are the paired devices that take notifications. Reading them
// leaves their last use as it was: a push is no use of the device.
func (s *Store) pushes() ([]Device, error) {
	devices, err := s.Devices()
	if err != nil {
		return nil, err
	}
	var out []Device
	for _, d := range devices {
		if d.Push != nil {
			out = append(out, d)
		}
	}
	return out, nil
}

// dropPush forgets a subscription its push service no longer knows,
// unless the device subscribed anew since.
func (s *Store) dropPush(device, endpoint string) error {
	defer s.lock()()
	f, err := s.load()
	if err != nil {
		return err
	}
	for i := range f.Devices {
		if d := &f.Devices[i]; d.ID == device && d.Push != nil && d.Push.Endpoint == endpoint {
			d.Push = nil
			return s.save(f)
		}
	}
	return nil
}

// --- sending -----------------------------------------------------------------

// pushJob is a notice to send, to one device or ("") every one.
type pushJob struct {
	n      Notice
	device string
}

// pusher sends the notices from a queue of its own, so a slow push
// service never holds the daemon, and limits them: a session once every
// five seconds, a device pushCap an hour, and none to a device whose push
// service asked to wait.
type pusher struct {
	store  *Store
	client *http.Client
	queue  chan pushJob
	limit  notice.Limiter
	mu     sync.Mutex
	sent   map[string][]time.Time
	until  map[string]time.Time
	now    func() time.Time
}

func newPusher(store *Store) *pusher {
	return &pusher{store: store, queue: make(chan pushJob, 64), sent: map[string][]time.Time{}, until: map[string]time.Time{},
		now: time.Now, client: &http.Client{
			Timeout: 10 * time.Second,
			// A push service answers; it never sends us elsewhere.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}}
}

// run sends what is queued until done closes.
func (p *pusher) run(done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case job := <-p.queue:
			p.fanOut(job)
		}
	}
}

// add queues a job, dropping it when the queue is full.
func (p *pusher) add(job pushJob) {
	select {
	case p.queue <- job:
	default:
	}
}

func (p *pusher) fanOut(job pushJob) {
	n := job.n
	kind := "s"
	if n.Program {
		kind = "p"
	}
	if job.device == "" && !p.limit.Allow(kind+":"+n.Session) {
		return
	}
	devices, err := p.store.pushes()
	if err != nil || len(devices) == 0 {
		return
	}
	key, err := p.store.vapidKey(false)
	if err != nil {
		log.Printf("web: push key: %v", err)
		return
	}
	raw, _ := key.Bytes()
	m := hmac.New(sha256.New, raw)
	m.Write([]byte("omaterm push topic"))
	tag := topic(m.Sum(nil), kind, n.Session)
	for _, d := range devices {
		if job.device != "" && d.ID != job.device || !p.allow(d.ID) {
			continue
		}
		p.send(key, d, n, tag)
	}
}

// allow tells whether a device may take another notification now,
// counting it if so.
func (p *pusher) allow(device string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if now.Before(p.until[device]) {
		return false
	}
	delete(p.until, device)
	kept := p.sent[device][:0]
	for _, t := range p.sent[device] {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	if len(kept) >= pushCap {
		p.sent[device] = kept
		return false
	}
	p.sent[device] = append(kept, now)
	return true
}

// wait holds a device's pushes for as long as its push service asked
// (Retry-After), a minute when it didn't say, an hour at most.
func (p *pusher) wait(device, retryAfter string) {
	d := time.Minute
	if s, err := strconv.Atoi(retryAfter); err == nil && s > 0 {
		d = time.Duration(s) * time.Second
	} else if t, err := http.ParseTime(retryAfter); err == nil {
		d = t.Sub(p.now())
	}
	d = min(max(d, time.Second), time.Hour)
	p.mu.Lock()
	p.until[device] = p.now().Add(d)
	p.mu.Unlock()
}

func (p *pusher) send(key *ecdsa.PrivateKey, d Device, n Notice, tag string) {
	sub := *d.Push
	body, err := payloadFor(n, sub.Words, tag)
	if err == nil {
		body, err = seal(sub, body)
	}
	var auth string
	if err == nil {
		auth, err = vapid(key, sub.Endpoint, p.now())
	}
	if err != nil {
		log.Printf("web: push to %s: %v", d.Name, err)
		return
	}
	req, err := http.NewRequest("POST", sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	h := req.Header
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Encoding", "aes128gcm")
	h.Set("Authorization", auth)
	h.Set("TTL", strconv.Itoa(pushTTL))
	h.Set("Topic", tag)
	h.Set("Urgency", "normal")
	if n.Urgent {
		h.Set("Urgency", "high")
	}
	res, err := p.client.Do(req)
	if err != nil {
		log.Printf("web: push to %s: %v", d.Name, err)
		return
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 4<<10))
	res.Body.Close()
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
	case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone:
		// The browser unsubscribed, or the service expired it.
		p.store.dropPush(d.ID, sub.Endpoint)
		log.Printf("web: push to %s: subscription gone", d.Name)
	case res.StatusCode == http.StatusTooManyRequests:
		p.wait(d.ID, res.Header.Get("Retry-After"))
		log.Printf("web: push to %s: too many, waiting", d.Name)
	default:
		log.Printf("web: push to %s: %s", d.Name, res.Status)
	}
}

// Push tells the devices that asked to be notified, without waiting.
func (s *Server) Push(n Notice) { s.pusher().add(pushJob{n: n}) }

// pusher is the server's, started the first time it is needed and stopped
// with the server.
func (s *Server) pusher() *pusher {
	s.pushOnce.Do(func() {
		s.push = newPusher(s.store)
		go s.push.run(s.done)
	})
	return s.push
}

// --- routes ------------------------------------------------------------------

// pushStatus is this device's notifications: the key its browser
// subscribes with, whether they are on, and where to. It only reads:
// Listen made the key.
func (s *Server) pushStatus(w http.ResponseWriter, r *http.Request) {
	key, err := s.store.vapidKey(false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	pub, _ := key.PublicKey.Bytes()
	d := r.Context().Value(deviceKey{}).(Device)
	st := map[string]any{"key": b64.EncodeToString(pub), "on": d.Push != nil}
	if d.Push != nil {
		st["endpoint"], st["words"] = d.Push.Endpoint, d.Push.Words
	}
	writeJSON(w, st)
}

// subscribe turns this device's notifications on, with the subscription
// its browser made: one a device, the new in place of the old. Words,
// when given, says whether the agent's words show; else it stays as it
// was. The first time, a notification says they are on.
func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Subscription struct {
			Endpoint string `json:"endpoint"`
			Keys     struct {
				P256dh string `json:"p256dh"`
				Auth   string `json:"auth"`
			} `json:"keys"`
		} `json:"subscription"`
		Words *bool `json:"words"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&p); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	d := r.Context().Value(deviceKey{}).(Device)
	sub := Subscription{Endpoint: p.Subscription.Endpoint, P256dh: p.Subscription.Keys.P256dh, Auth: p.Subscription.Keys.Auth}
	if d.Push != nil {
		sub.Words = d.Push.Words
	}
	if p.Words != nil {
		sub.Words = *p.Words
	}
	if err := checkSubscription(sub); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if ok, err := s.store.SetPush(d.ID, &sub); err != nil || !ok {
		http.Error(w, "not paired", http.StatusUnauthorized)
		return
	}
	if d.Push == nil || d.Push.Endpoint != sub.Endpoint {
		s.pusher().add(pushJob{device: d.ID, n: Notice{Title: "omaterm", State: "Notifications are on for this phone"}})
	}
	writeJSON(w, map[string]any{"on": true, "words": sub.Words})
}

// unsubscribe turns this device's notifications off.
func (s *Server) unsubscribe(w http.ResponseWriter, r *http.Request) {
	d := r.Context().Value(deviceKey{}).(Device)
	if _, err := s.store.SetPush(d.ID, nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"on": false})
}
