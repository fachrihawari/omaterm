//go:build linux || darwin

package web_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"omaterm/internal/daemon"
	"omaterm/internal/voice"
)

// fakeVoice stands in for whisper-server, counting what it is asked.
type fakeVoice struct{ calls atomic.Int32 }

func (f *fakeVoice) Transcribe(_ context.Context, bin, model string, wav []byte, lang string) (string, error) {
	f.calls.Add(1)
	return "halo dari " + lang + " " + filepath.Base(model), nil
}
func (f *fakeVoice) Running() bool { return false }
func (f *fakeVoice) Stop()         {}

// wav is a second of silence as the phone records it.
func wav() []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+32000))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(16000), uint32(32000), uint16(2), uint16(16)} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(32000))
	b.Write(make([]byte, 32000))
	return b.Bytes()
}

func (f *fixture) postVoice(t *testing.T, c *http.Client, query string, body []byte, origin string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", f.base+"/api/voice"+query, bytes.NewReader(body))
	req.Header.Set("Content-Type", "audio/wav")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, strings.TrimSpace(string(b))
}

func (f *fixture) getVoice(t *testing.T, c *http.Client) (int, string) {
	t.Helper()
	res, err := c.Get(f.base + "/api/voice")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, strings.TrimSpace(string(b))
}

// Voice is off until turned on: the page is told only that, and a
// recording finds no route, never reaching whisper.
func TestVoiceOffByDefault(t *testing.T) {
	fv := &fakeVoice{}
	f := newFixture(t, func(o *daemon.Options) { o.Voice = fv })
	if code, _ := f.getVoice(t, f.client(t)); code != http.StatusUnauthorized {
		t.Fatalf("unpaired GET: %d", code)
	}
	if code, _ := f.postVoice(t, f.client(t), "", wav(), f.base); code != http.StatusUnauthorized {
		t.Fatalf("unpaired POST: %d", code)
	}
	c := f.paired(t)
	if code, body := f.getVoice(t, c); code != 200 || body != `{"enabled":false}` {
		t.Fatalf("GET while off: %d %s", code, body)
	}
	if code, _ := f.postVoice(t, c, "", wav(), f.base); code != http.StatusNotFound {
		t.Fatalf("POST while off: %d", code)
	}
	if n := fv.calls.Load(); n != 0 {
		t.Fatalf("whisper was asked %d times while voice is off", n)
	}
}

// On, with an engine and a model, a recording comes back as text, in the
// language asked or the settings'.
func TestVoiceTranscribes(t *testing.T) {
	fv := &fakeVoice{}
	f := newFixture(t, func(o *daemon.Options) { o.Voice = fv })
	os.WriteFile(filepath.Join(f.dir, "settings.json"), []byte(`{"voice":{"enabled":true,"model":"tiny","language":"id"}}`), 0o600)
	dir := voice.Dir(f.dir)
	os.MkdirAll(filepath.Dir(voice.EnginePath(dir)), 0o700)
	os.WriteFile(voice.EnginePath(dir), []byte("#!/bin/sh\n"), 0o700)
	c := f.paired(t)

	// No model yet: the page is told why it can't record.
	code, body := f.getVoice(t, c)
	var st voice.Status
	json.Unmarshal([]byte(body), &st)
	if code != 200 || !st.Enabled || st.Ready || !strings.Contains(st.Reason, "model") {
		t.Fatalf("GET without a model: %d %s", code, body)
	}
	if code, body := f.postVoice(t, c, "", wav(), f.base); code != http.StatusConflict || !strings.Contains(body, "model") {
		t.Fatalf("POST without a model: %d %s", code, body)
	}

	os.WriteFile(filepath.Join(dir, "ggml-tiny.bin"), []byte("x"), 0o600)
	code, body = f.getVoice(t, c)
	st = voice.Status{}
	json.Unmarshal([]byte(body), &st)
	if code != 200 || !st.Ready || st.Language != "id" || len(st.Languages) == 0 || strings.Contains(body, "engine") {
		t.Fatalf("GET: %d %s", code, body)
	}
	if code, body := f.postVoice(t, c, "", wav(), f.base); code != 200 || body != `{"text":"halo dari id ggml-tiny.bin"}` {
		t.Fatalf("POST: %d %s", code, body)
	}
	if code, body := f.postVoice(t, c, "?lang=en", wav(), f.base); code != 200 || !strings.Contains(body, "halo dari en") {
		t.Fatalf("POST in English: %d %s", code, body)
	}

	calls := fv.calls.Load()
	if code, _ := f.postVoice(t, c, "", wav(), "https://evil.example"); code != http.StatusForbidden {
		t.Fatalf("cross-site POST: %d", code)
	}
	if code, _ := f.postVoice(t, f.client(t), "", wav(), f.base); code != http.StatusUnauthorized {
		t.Fatalf("unpaired POST: %d", code)
	}
	if code, _ := f.postVoice(t, c, "", make([]byte, 4<<20+1), f.base); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over 4 MB: %d", code)
	}
	if code, _ := f.postVoice(t, c, "", []byte("OggS not a wav at all"), f.base); code != http.StatusBadRequest {
		t.Fatalf("not a WAV: %d", code)
	}
	if code, _ := f.postVoice(t, c, "?lang=klingon", wav(), f.base); code != http.StatusBadRequest {
		t.Fatalf("unknown language: %d", code)
	}
	if n := fv.calls.Load(); n != calls {
		t.Fatalf("whisper was asked for %d refused recordings", n-calls)
	}

	// A device sends so many a minute, then waits.
	limited := false
	for i := 0; i < 25 && !limited; i++ {
		code, _ := f.postVoice(t, c, "", wav(), f.base)
		limited = code == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("no limit on recordings")
	}
}
