package notice

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := &Limiter{Now: func() time.Time { return now }}
	if !l.Allow("s") || l.Allow("s") || !l.Allow("other") {
		t.Fatal("a session notified twice at once, or held another back")
	}
	now = now.Add(6 * time.Second)
	if !l.Allow("s") {
		t.Fatal("still held after five seconds")
	}
}

func TestBody(t *testing.T) {
	if Body(Waiting, "") != "Needs your input" || Body(Done, "Tests pass") != "Tests pass" {
		t.Fatal("body")
	}
}
