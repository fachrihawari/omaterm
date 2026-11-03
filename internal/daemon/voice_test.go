//go:build linux || darwin

package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"omaterm/internal/voice"
)

// runningVoice is a whisper-server that runs once asked, until stopped.
type runningVoice struct{ running, stops atomic.Int32 }

func (v *runningVoice) Transcribe(context.Context, string, string, []byte, string) (string, error) {
	v.running.Store(1)
	return "ok", nil
}
func (v *runningVoice) Running() bool { return v.running.Load() == 1 }
func (v *runningVoice) Stop() {
	v.running.Store(0)
	v.stops.Add(1)
}

// Voice off starts nothing; turned off, it stops the whisper-server that
// runs, keeping the downloads.
func TestVoiceOffStopsWhisper(t *testing.T) {
	d, _ := startDaemon(t, time.Minute)
	fv := &runningVoice{}
	d.voice = fv
	settings := filepath.Join(filepath.Dir(d.stateFile()), "settings.json")
	if _, err := d.Transcribe(context.Background(), nil, ""); !errors.Is(err, voice.ErrOff) {
		t.Fatalf("off: %v", err)
	}
	dir := voice.Dir(d.opts.StateDir)
	os.MkdirAll(filepath.Dir(voice.EnginePath(dir)), 0o700)
	os.WriteFile(voice.EnginePath(dir), []byte("x"), 0o700)
	model := filepath.Join(dir, "ggml-small.bin")
	os.WriteFile(model, []byte("x"), 0o600)
	os.WriteFile(settings, []byte(`{"voice":{"enabled":true}}`), 0o600)
	if text, err := d.Transcribe(context.Background(), nil, ""); err != nil || text != "ok" {
		t.Fatalf("on: %q %v", text, err)
	}
	res, _ := d.Handle(context.Background(), "voice.status", nil)
	if st := res.(voice.Status); !st.Ready || !st.Running || st.Model != voice.DefaultModel {
		t.Fatalf("status while on: %+v", st)
	}
	os.WriteFile(settings, []byte(`{"voice":{"enabled":false}}`), 0o600)
	res, _ = d.Handle(context.Background(), "voice.reload", nil)
	if st := res.(voice.Status); st.Enabled || st.Running || fv.Running() || fv.stops.Load() != 1 {
		t.Fatalf("after turning off: %+v, %d stops", st, fv.stops.Load())
	}
	if _, err := os.Stat(model); err != nil {
		t.Fatal("turning voice off removed the model")
	}
}
