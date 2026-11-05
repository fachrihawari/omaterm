//go:build linux || darwin

package web_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"omaterm/internal/daemon"
	"omaterm/internal/model"
	"omaterm/internal/web"
)

// fakePush is a push service: it hands over what is posted to it, and
// answers with status.
type fakePush struct {
	url    string
	got    chan pushed
	status atomic.Int32
}

type pushed struct {
	path   string
	header http.Header
	body   []byte
}

func newFakePush(t *testing.T) *fakePush {
	p := &fakePush{got: make(chan pushed, 16)}
	p.status.Store(http.StatusCreated)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		p.got <- pushed{r.URL.Path, r.Header.Clone(), b}
		w.WriteHeader(int(p.status.Load()))
	}))
	t.Cleanup(srv.Close)
	p.url = srv.URL
	return p
}

func (p *fakePush) next(t *testing.T) pushed {
	t.Helper()
	select {
	case got := <-p.got:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("nothing pushed")
	}
	return pushed{}
}

// pushBrowser is a browser's push subscription: its keys, and where its
// push service takes its pushes.
type pushBrowser struct {
	key      *ecdh.PrivateKey
	secret   []byte
	endpoint string
}

func newPushBrowser(endpoint string) pushBrowser {
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	secret := make([]byte, 16)
	rand.Read(secret)
	return pushBrowser{key, secret, endpoint}
}

// body is what the page posts to /api/push, words "" leaving them be.
func (b pushBrowser) body(words string) string {
	enc := base64.RawURLEncoding
	s := `{"subscription":{"endpoint":"` + b.endpoint + `","expirationTime":null,"keys":{"p256dh":"` +
		enc.EncodeToString(b.key.PublicKey().Bytes()) + `","auth":"` + enc.EncodeToString(b.secret) + `"}}`
	if words != "" {
		s += `,"words":` + words
	}
	return s + "}"
}

// open reads a push as the service worker gets it.
func (b pushBrowser) open(t *testing.T, p pushed) map[string]any {
	t.Helper()
	plain, err := web.DecryptPush(b.key, b.secret, p.body)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(plain, &out); err != nil {
		t.Fatalf("payload %q: %v", plain, err)
	}
	return out
}

func (f *fixture) do(t *testing.T, c *http.Client, method, path, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, f.base+path, strings.NewReader(body))
	req.Header.Set("Origin", f.base)
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// pushKey is GET /api/push/key.
func (f *fixture) pushKey(t *testing.T, c *http.Client) (st struct {
	Key, Endpoint string
	On, Words     bool
}) {
	t.Helper()
	res, err := c.Get(f.base + "/api/push/key")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("push key: %v %v", res.Status, err)
	}
	json.NewDecoder(res.Body).Decode(&st)
	return st
}

// startBlocked runs an agent in session sid that comes to wait for
// permission to deploy.
func (f *fixture) startBlocked(t *testing.T, pane, sid string) {
	t.Helper()
	msg := base64.StdEncoding.EncodeToString([]byte("Approve deploy?"))
	script := `printf '\033]7501;state=working\007'; sleep 0.3; printf '\033]7501;state=blocked:kind=permission:msg=` + msg + `\033\\'; sleep 30`
	params, _ := json.Marshal(daemon.StartParams{ID: pane, Session: sid, Project: "p1", Command: []string{"sh", "-c", script}, Exec: true, Cols: 80, Rows: 24})
	if _, err := f.d.Handle(context.Background(), "pane.start", params); err != nil {
		t.Fatal(err)
	}
}

// A phone that turned notifications on gets one, encrypted for it and
// signed, when an agent needs permission: by default only the session and
// its state, the agent's words once it asks for them. A subscription the
// push service has forgotten is dropped, and only a push service is
// posted to.
func TestWebPush(t *testing.T) {
	fake := newFakePush(t)
	t.Cleanup(web.AllowPushOrigin(fake.url))
	f := newFixture(t, func(o *daemon.Options) {
		st := model.State{Version: model.Version, Projects: []model.Project{{ID: "p1", Name: "web", Dir: t.TempDir()}}}
		for _, s := range [][2]string{{"s1", "deploy"}, {"s2", "review"}, {"s3", "docs"}} {
			st.Sessions = append(st.Sessions, model.Session{ID: s[0], Project: "p1", Name: s[1],
				Layout: &model.Layout{Pane: "a" + s[0][1:]}, Panes: []model.Pane{{ID: "a" + s[0][1:]}}})
		}
		model.Save(o.StateFile, st)
	})
	c := f.paired(t)
	st := f.pushKey(t, c)
	if st.On || len(st.Key) != 87 {
		t.Fatalf("before: %+v", st)
	}

	phone := newPushBrowser(fake.url + "/sub/1")
	for _, endpoint := range []string{"https://evil.example/x", "http://127.0.0.1:1/x"} {
		if res := f.do(t, c, "POST", "/api/push", newPushBrowser(endpoint).body("")); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("subscribed to %s: %s", endpoint, res.Status)
		}
	}
	if res := f.post(t, c, "/api/push", phone.body(""), "https://evil.example"); res.StatusCode != http.StatusForbidden {
		t.Fatalf("subscribed from another site: %s", res.Status)
	}
	if res := f.do(t, c, "POST", "/api/push", phone.body("")); res.StatusCode != 200 {
		t.Fatalf("subscribe: %s", res.Status)
	}
	// It says so at once.
	got := fake.next(t)
	if p := phone.open(t, got); p["title"] != "omaterm" || p["body"] != "Notifications are on for this phone" {
		t.Fatalf("first push %v", p)
	}
	if !strings.HasSuffix(got.header.Get("Authorization"), ", k="+st.Key) {
		t.Fatalf("signed with another key: %s", got.header.Get("Authorization"))
	}

	f.startBlocked(t, "a1", "s1")
	got = fake.next(t)
	h := got.header
	if got.path != "/sub/1" || h.Get("Content-Encoding") != "aes128gcm" || h.Get("Urgency") != "high" ||
		h.Get("TTL") != "43200" || len(h.Get("Topic")) != 32 || !strings.HasPrefix(h.Get("Authorization"), "vapid t=") {
		t.Fatalf("push %s %v", got.path, h)
	}
	p := phone.open(t, got)
	if p["title"] != "deploy · web" || p["body"] != "Needs permission" || p["pane"] != "a1" || p["waiting"] != 1.0 || p["tag"] != h.Get("Topic") {
		t.Fatalf("payload %v", p)
	}

	// The agent's words, asked for; the same subscription isn't announced
	// again.
	if res := f.do(t, c, "POST", "/api/push", phone.body("true")); res.StatusCode != 200 {
		t.Fatalf("words: %s", res.Status)
	}
	if st := f.pushKey(t, c); !st.On || !st.Words || st.Endpoint != phone.endpoint {
		t.Fatalf("after words: %+v", st)
	}
	f.startBlocked(t, "a2", "s2")
	if p := phone.open(t, fake.next(t)); p["title"] != "review · web" || p["body"] != "Needs permission: Approve deploy?" || p["waiting"] != 2.0 {
		t.Fatalf("payload with words %v", p)
	}

	// Gone from the push service, gone from omaterm.
	fake.status.Store(http.StatusGone)
	f.startBlocked(t, "a3", "s3")
	fake.next(t)
	for deadline := time.Now().Add(5 * time.Second); f.pushKey(t, c).On; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the gone subscription stayed")
		}
	}

	// Turned off, as signing out does first.
	fake.status.Store(http.StatusCreated)
	f.do(t, c, "POST", "/api/push", phone.body(""))
	fake.next(t)
	if res := f.post(t, c, "/api/push", "", ""); res.StatusCode != http.StatusForbidden {
		t.Fatalf("a POST without an origin: %s", res.Status)
	}
	if res := f.do(t, c, "DELETE", "/api/push", ""); res.StatusCode != 200 {
		t.Fatalf("delete: %s", res.Status)
	}
	if f.pushKey(t, c).On {
		t.Fatal("still on after DELETE")
	}
	if res, _ := f.client(t).Get(f.base + "/api/push/key"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unpaired key: %s", res.Status)
	}
}
