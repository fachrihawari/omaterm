package session

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// Two starts of one pane at once: one runs, the other is told it exists.
// Both ran, the id went twice into the order, and after its close the
// daemon's tick hit a nil pane and died with every session in it.
func TestStartOnceAtATime(t *testing.T) {
	r := NewRegistry()
	defer r.CloseAll()
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = r.Start(StartOptions{ID: "p1", Session: "s", Command: []string{"/bin/sh", "-c", "sleep 30"}, Exec: true, Cols: 80, Rows: 24})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrExists):
			t.Fatalf("start: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d starts of one pane ran, want 1", ok)
	}
	r.Remove("p1")
	if n := len(r.list()); n != 0 {
		t.Fatalf("%d panes listed after the close", n)
	}
	r.Tick(time.Now()) // panicked on the nil pane
}
