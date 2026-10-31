package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// request is a pairing attempt from a peer, with headers.
func request(remote string, headers map[string]string) *http.Request {
	r := httptest.NewRequest("POST", "/api/pair", nil)
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestPairClient(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remote string
		header map[string]string
		want   string
	}{
		{"a network peer", "192.168.1.5:5000", nil, "192.168.1.5"},
		{"a network peer's headers are its own claim", "192.168.1.5:5000", map[string]string{"CF-Connecting-IP": "9.9.9.9", "X-Forwarded-For": "8.8.8.8"}, "192.168.1.5"},
		{"a tunnel with Cloudflare's header", "127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "203.0.113.7"}, "203.0.113.7"},
		{"Cloudflare's header over forwarded-for", "127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "203.0.113.7", "X-Forwarded-For": "1.1.1.1"}, "203.0.113.7"},
		{"the last hop of forwarded-for", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.9"}, "198.51.100.9"},
		{"the loopback over IPv6", "[::1]:5000", map[string]string{"X-Forwarded-For": "198.51.100.9"}, "198.51.100.9"},
		{"a header that is no address", "127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "not an ip", "X-Forwarded-For": "nor this"}, "127.0.0.1"},
		{"the loopback alone", "127.0.0.1:5000", nil, "127.0.0.1"},
	} {
		if got := pairClient(request(tc.remote, tc.header)); got != tc.want {
			t.Errorf("%s: client %q, want %q", tc.name, got, tc.want)
		}
	}
}

// One client trying too often does not lock the others out.
func TestPairLimitPerClient(t *testing.T) {
	s := &Server{}
	tunnel := func(ip string) *http.Request {
		return request("127.0.0.1:1", map[string]string{"CF-Connecting-IP": ip})
	}
	for i := 0; i < pairLimit; i++ {
		if !s.allowPair(tunnel("203.0.113.1")) {
			t.Fatalf("try %d refused", i+1)
		}
	}
	if s.allowPair(tunnel("203.0.113.1")) {
		t.Fatal("an eleventh try in a minute was heard")
	}
	if !s.allowPair(tunnel("203.0.113.2")) {
		t.Fatal("another client was locked out")
	}
	// Spoofing the header from the network does not buy fresh tries.
	for i := 0; i < pairLimit; i++ {
		s.allowPair(request("10.0.0.9:1", map[string]string{"CF-Connecting-IP": "198.51.100." + fmt.Sprint(i+1)}))
	}
	if s.allowPair(request("10.0.0.9:1", map[string]string{"CF-Connecting-IP": "198.51.100.99"})) {
		t.Fatal("a network peer changed its client with a header")
	}
}

// All clients together have a ceiling.
func TestPairLimitAll(t *testing.T) {
	s := &Server{}
	heard := 0
	for i := 0; i < 200; i++ {
		// Every try is from a client of its own, so each is under its limit.
		ip := fmt.Sprintf("203.0.113.%d", i+1)
		if s.allowPair(request("127.0.0.1:1", map[string]string{"CF-Connecting-IP": ip})) {
			heard++
		}
	}
	if heard != pairLimitAll {
		t.Fatalf("%d tries heard, want the ceiling %d", heard, pairLimitAll)
	}
}
