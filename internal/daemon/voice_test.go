//go:build linux || darwin

package daemon

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/proto"
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

// testWAV is d of silence as the app records it: 16 kHz mono 16-bit.
func testWAV(d time.Duration) []byte {
	n := int(d * voice.SampleRate / time.Second)
	b := make([]byte, 44+2*n)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*n))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], voice.SampleRate)
	binary.LittleEndian.PutUint32(b[28:], 2*voice.SampleRate)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*n))
	return b
}

// heardVoice writes down what it is given as how long and in which
// language.
type heardVoice struct {
	runningVoice
	got  []byte
	lang string
}

func (v *heardVoice) Transcribe(_ context.Context, _, _ string, wav []byte, lang string) (string, error) {
	v.got, v.lang = wav, lang
	return " hello there\n", nil
}

// The app's recordings go through voice.transcribe, over the socket: off,
// it refuses them, starting nothing; on, a 2-minute WAV, near 4 MB, comes
// through whole, in the settings' language unless the call names one.
func TestVoiceTranscribeMethod(t *testing.T) {
	d, socket := startDaemon(t, time.Minute)
	fv := &heardVoice{}
	d.voice = fv
	call := func(p VoiceParams) (VoiceText, error) {
		c, err := client.Dial(socket)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		var out VoiceText
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = c.CallContext(ctx, "voice.transcribe", p, &out)
		return out, err
	}
	short := testWAV(time.Second)
	if _, err := call(VoiceParams{WAV: short}); err == nil || !strings.Contains(err.Error(), "off") || fv.Running() {
		t.Fatalf("off: %v, running %v", err, fv.Running())
	}

	dir := voice.Dir(d.opts.StateDir)
	os.MkdirAll(filepath.Dir(voice.EnginePath(dir)), 0o700)
	os.WriteFile(voice.EnginePath(dir), []byte("x"), 0o700)
	settings := filepath.Join(filepath.Dir(d.stateFile()), "settings.json")
	os.WriteFile(settings, []byte(`{"voice":{"enabled":true,"language":"id"}}`), 0o600)
	if _, err := call(VoiceParams{WAV: short}); err == nil || !strings.Contains(err.Error(), "No model") {
		t.Fatalf("no model: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "ggml-small.bin"), []byte("x"), 0o600)

	long := testWAV(voice.MaxLength)
	if len(long) < 3<<20 {
		t.Fatalf("a 2-minute WAV is only %d bytes", len(long))
	}
	got, err := call(VoiceParams{WAV: long})
	if err != nil || got.Text != " hello there\n" {
		t.Fatalf("on: %q %v", got.Text, err)
	}
	if !bytes.Equal(fv.got, long) || fv.lang != "id" {
		t.Fatalf("whisper got %d bytes in %q, want %d in id", len(fv.got), fv.lang, len(long))
	}
	if _, err := call(VoiceParams{WAV: short, Lang: "en"}); err != nil || fv.lang != "en" {
		t.Fatalf("lang en: %v, heard in %q", err, fv.lang)
	}
	for name, p := range map[string]VoiceParams{
		"not a WAV":        {WAV: []byte("hello")},
		"too long":         {WAV: testWAV(voice.MaxLength + time.Second)},
		"unknown language": {WAV: short, Lang: "xx"},
	} {
		if _, err := call(p); proto.CodeOf(err) != proto.CodeUsage {
			t.Errorf("%s: %v", name, err)
		}
	}
}
