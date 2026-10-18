package service

import (
	"context"
	"sync"
)

// Job guards async work: each Run cancels the previous invocation, and the
// done callback is skipped for stale results (a newer Run happened first).
type Job struct {
	mu     sync.Mutex
	seq    uint64
	cancel context.CancelFunc
}

// Run cancels any in-flight run and starts fn. done is called with the
// result only if no newer Run has started since; it is always called on
// the goroutine that runs fn, so callers must marshal UI updates through
// Window.Update or similar.
func (j *Job) Run(fn func(ctx context.Context) error, done func(err error)) {
	j.mu.Lock()
	j.seq++
	seq := j.seq
	if j.cancel != nil {
		j.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.mu.Unlock()

	go func() {
		err := fn(ctx)
		cancel()
		j.mu.Lock()
		stale := j.seq != seq
		j.mu.Unlock()
		if stale {
			return
		}
		if done != nil {
			done(err)
		}
	}()
}

// Cancel stops the in-flight run, if any.
func (j *Job) Cancel() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancel != nil {
		j.cancel()
		j.cancel = nil
	}
}
