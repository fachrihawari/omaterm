package voice

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain is also the fake whisper-server: run as one, the test binary
// serves /inference, answering with the language it was asked for.
func TestMain(m *testing.M) {
	if os.Getenv("OMATERM_FAKE_WHISPER") == "1" {
		fakeWhisper()
		return
	}
	os.Exit(m.Run())
}

func fakeWhisper() {
	fs := flag.NewFlagSet("whisper-server", flag.ExitOnError)
	model := fs.String("m", "", "")
	host := fs.String("host", "", "")
	port := fs.String("port", "", "")
	fs.String("t", "", "")
	fs.Parse(os.Args[1:])
	if _, err := os.Stat(*model); err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to load model")
		os.Exit(1)
	}
	http.HandleFunc("/inference", func(w http.ResponseWriter, r *http.Request) {
		f, _, err := r.FormFile("file")
		if err != nil || r.FormValue("response_format") != "json" {
			http.Error(w, `{"error":"bad request"}`, 400)
			return
		}
		f.Close()
		json.NewEncoder(w).Encode(map[string]string{"text": " said in " + r.FormValue("language") + " with " + filepath.Base(*model) + "\n"})
	})
	http.ListenAndServe(*host+":"+*port, nil)
}

// wav is a WAV of n samples, as the phone makes it.
func wav(n int, rate uint32, channels uint16) []byte {
	var b bytes.Buffer
	data := n * 2 * int(channels)
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+data))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), channels, rate, rate * 2 * uint32(channels), 2 * channels, uint16(16)} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(data))
	b.Write(make([]byte, data))
	return b.Bytes()
}

func TestCheckWAV(t *testing.T) {
	if d, err := CheckWAV(wav(SampleRate*3, SampleRate, 1)); err != nil || d != 3*time.Second {
		t.Fatalf("3 s of good audio: %v %v", d, err)
	}
	// A chunk the encoder didn't write, before the data, is passed over.
	b := wav(SampleRate, SampleRate, 1)
	withList := append(append(append([]byte{}, b[:36]...), []byte("LIST\x03\x00\x00\x00abc\x00")...), b[36:]...)
	if _, err := CheckWAV(withList); err != nil {
		t.Fatalf("with a LIST chunk: %v", err)
	}
	for name, b := range map[string][]byte{
		"empty":        nil,
		"not RIFF":     []byte("OggS0000000000000000"),
		"44.1 kHz":     wav(100, 44100, 1),
		"stereo":       wav(100, SampleRate, 2),
		"no samples":   wav(0, SampleRate, 1),
		"too long":     wav(int(MaxLength/time.Second)*SampleRate+SampleRate, SampleRate, 1),
		"cut short":    wav(SampleRate, SampleRate, 1)[:1000],
		"no fmt chunk": append([]byte("RIFF\x00\x00\x00\x00WAVEdata\x02\x00\x00\x00"), 0, 0),
	} {
		if _, err := CheckWAV(b); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// serve answers downloads of body, counting them.
func serve(t *testing.T, body []byte) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestDownload(t *testing.T) {
	body := bytes.Repeat([]byte("model "), 50000)
	srv, _ := serve(t, body)
	dest := filepath.Join(t.TempDir(), "voice", "ggml-test.bin")
	var last, total int64
	calls := 0
	err := Download(context.Background(), srv.URL, sum(body), dest, int64(len(body)), func(d, tot int64) {
		calls++
		last, total = d, tot
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, body) {
		t.Fatal("the file isn't what was served")
	}
	if calls == 0 || last != int64(len(body)) || total != int64(len(body)) {
		t.Fatalf("progress: %d calls, last %d of %d", calls, last, total)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part left behind")
	}
}

// A download that doesn't match its checksum, or is longer than the size
// pinned, is removed, and nothing takes its place.
func TestDownloadChecksumMismatch(t *testing.T) {
	body := []byte("not the model")
	srv, _ := serve(t, body)
	dir := t.TempDir()
	dest := filepath.Join(dir, "m.bin")
	if err := Download(context.Background(), srv.URL, sum([]byte("the model")), dest, 0, nil); !errors.Is(err, ErrChecksum) {
		t.Fatalf("mismatch: %v", err)
	}
	if err := Download(context.Background(), srv.URL, sum(body), dest, 4, nil); !errors.Is(err, ErrChecksum) {
		t.Fatalf("longer than pinned: %v", err)
	}
	for _, p := range []string{dest, dest + ".part"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s left behind", filepath.Base(p))
		}
	}
	if err := Download(context.Background(), srv.URL, "", dest, 0, nil); !errors.Is(err, ErrUnpinned) {
		t.Fatalf("no checksum: %v", err)
	}
}

// Cancelling stops a download and removes what came; a second download of
// the same file meanwhile is turned away.
func TestDownloadCancel(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		w.Write(make([]byte, 1000))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "m.bin")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Download(ctx, srv.URL, sum(nil), dest, 0, nil) }()
	<-started
	if err := Download(context.Background(), srv.URL, sum(nil), dest, 0, nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("second download: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	for _, p := range []string{dest, dest + ".part"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s left behind", filepath.Base(p))
		}
	}
}

func TestInstallEngine(t *testing.T) {
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	exe := []byte("#!/bin/sh\n")
	tw.WriteHeader(&tar.Header{Name: "whisper-server-linux-amd64/README", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("hi"))
	tw.WriteHeader(&tar.Header{Name: "whisper-server-linux-amd64/whisper-server", Mode: 0o755, Size: int64(len(exe)), Typeflag: tar.TypeReg})
	tw.Write(exe)
	tw.Close()
	gz.Close()
	srv, _ := serve(t, archive.Bytes())
	dir := t.TempDir()
	e := Engine{OS: "linux", Arch: "amd64", SHA256: sum(archive.Bytes())}
	archivePath := filepath.Join(dir, e.Asset())
	if err := Download(context.Background(), srv.URL, e.SHA256, archivePath, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := unpackEngine(archivePath, EnginePath(dir)); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(EnginePath(dir))
	if err != nil || fi.Mode()&0o100 == 0 {
		t.Fatalf("whisper-server: %v %v", fi, err)
	}
	if got, _ := os.ReadFile(EnginePath(dir)); !bytes.Equal(got, exe) {
		t.Fatal("unpacked the wrong file")
	}
}

// Find takes whisper-server from the PATH over the one downloaded.
func TestFindPrefersPATH(t *testing.T) {
	dir := t.TempDir()
	defer func(was func(string) (string, error)) { lookPath = was }(lookPath)
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if _, _, ok := Find(dir); ok {
		t.Fatal("found an engine in an empty folder")
	}
	os.MkdirAll(filepath.Dir(EnginePath(dir)), 0o700)
	os.WriteFile(EnginePath(dir), []byte("x"), 0o700)
	if p, onPATH, ok := Find(dir); !ok || onPATH || p != EnginePath(dir) {
		t.Fatalf("downloaded: %q %v %v", p, onPATH, ok)
	}
	lookPath = func(string) (string, error) { return "/usr/bin/whisper-server", nil }
	if p, onPATH, ok := Find(dir); !ok || !onPATH || p != "/usr/bin/whisper-server" {
		t.Fatalf("PATH: %q %v %v", p, onPATH, ok)
	}
}

// Off, voice says only that; on, it says what is missing.
func TestCheck(t *testing.T) {
	dir := t.TempDir()
	defer func(was func(string) (string, error)) { lookPath = was }(lookPath)
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	if b, _ := json.Marshal(Check(dir, Settings{Model: "small"})); string(b) != `{"enabled":false}` {
		t.Fatalf("off: %s", b)
	}
	st := Check(dir, Settings{Enabled: true, Language: "xx"})
	if st.Ready || !strings.Contains(st.Reason, "engine") || st.Model != DefaultModel || st.Language != DefaultLanguage {
		t.Fatalf("no engine: %+v", st)
	}
	lookPath = func(string) (string, error) { return "/bin/whisper-server", nil }
	if st := Check(dir, Settings{Enabled: true, Model: "tiny"}); st.Ready || !strings.Contains(st.Reason, "model") {
		t.Fatalf("no model: %+v", st)
	}
	os.WriteFile(filepath.Join(dir, "ggml-tiny.bin"), []byte("x"), 0o600)
	if st := Check(dir, Settings{Enabled: true, Model: "tiny", Language: "id"}); !st.Ready || st.Language != "id" {
		t.Fatalf("ready: %+v", st)
	}
}

// Transcribe starts whisper-server with the model, asks it, and stops it
// once idle; another model starts it again.
func TestServerTranscribe(t *testing.T) {
	t.Setenv("OMATERM_FAKE_WHISPER", "1")
	defer func(was time.Duration) { IdleStop = was }(IdleStop)
	IdleStop = 300 * time.Millisecond
	dir := t.TempDir()
	tiny, small := filepath.Join(dir, "ggml-tiny.bin"), filepath.Join(dir, "ggml-small.bin")
	os.WriteFile(tiny, []byte("x"), 0o600)
	os.WriteFile(small, []byte("x"), 0o600)
	bin, _ := os.Executable()
	s := &Server{}
	defer s.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	text, err := s.Transcribe(ctx, bin, tiny, wav(SampleRate, SampleRate, 1), "id")
	if err != nil || text != "said in id with ggml-tiny.bin" {
		t.Fatalf("transcribe: %q %v", text, err)
	}
	if !s.Running() {
		t.Fatal("whisper-server isn't running after a recording")
	}
	if text, err := s.Transcribe(ctx, bin, small, wav(SampleRate, SampleRate, 1), ""); err != nil || text != "said in "+DefaultLanguage+" with ggml-small.bin" {
		t.Fatalf("another model: %q %v", text, err)
	}
	for deadline := time.Now().Add(5 * time.Second); s.Running(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("whisper-server still runs after its idle time")
		}
	}
	// A model it can't load says why.
	if _, err := s.Transcribe(ctx, bin, filepath.Join(dir, "missing.bin"), wav(SampleRate, SampleRate, 1), ""); err == nil || !strings.Contains(err.Error(), "failed to load model") {
		t.Fatalf("missing model: %v", err)
	}
}

// inference reads whisper-server's answer, or its error.
func TestInference(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference" || r.FormValue("language") != "en" {
			http.Error(w, `{"error":"no"}`, 500)
			return
		}
		w.Write([]byte(`{"text":"  hello world \n"}`))
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	if text, err := inference(context.Background(), addr, wav(10, SampleRate, 1), "en"); err != nil || text != "hello world" {
		t.Fatalf("%q %v", text, err)
	}
	if _, err := inference(context.Background(), addr, wav(10, SampleRate, 1), "fr"); err == nil {
		t.Fatal("an error answer gave no error")
	}
}

func TestLocaleLanguage(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"LANG": "id_ID.UTF-8"}, "id"},
		{map[string]string{"LANG": "en_US.UTF-8", "LC_ALL": "ja_JP.UTF-8"}, "ja"},
		{map[string]string{"LANG": "id_ID.UTF-8", "LC_MESSAGES": "de_DE"}, "de"},
		{map[string]string{"LANG": "jv_ID"}, "jw"},
		{map[string]string{"LANG": "C.UTF-8"}, "en"},
		{map[string]string{"LANG": "eo_XX"}, "en"},
		{map[string]string{"LANG": "auto"}, "en"},
		{map[string]string{}, "en"},
		{map[string]string{"LC_ALL": "._", "LANG": "id_ID"}, "id"},
	} {
		if got := localeLanguage(func(k string) string { return tc.env[k] }); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.env, got, tc.want)
		}
	}
	if DefaultLanguage == "auto" || !KnownLanguage(DefaultLanguage) {
		t.Errorf("DefaultLanguage %q", DefaultLanguage)
	}
}

func TestLanguageSets(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
		name string
	}{
		{"id", []string{"id"}, "Indonesian"},
		{"id,en", []string{"id", "en"}, "Indonesian or English"},
		{" en , id ,en", []string{"en", "id"}, "English or Indonesian"},
		{"id,en,jw", []string{"id", "en", "jw"}, "Indonesian, English or Javanese"},
		{"auto", nil, "any language"},
		{"id,auto", nil, "any language"},
		{"id,xx", nil, "any language"},
		{"", nil, "any language"},
	} {
		if got := ParseLanguages(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("ParseLanguages(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got := LanguageName(tc.in); got != tc.name {
			t.Errorf("LanguageName(%q) = %q, want %q", tc.in, got, tc.name)
		}
	}
	if !ValidLanguage("auto") || !ValidLanguage("id,en") || ValidLanguage("id,xx") || ValidLanguage("") {
		t.Fatal("ValidLanguage")
	}
	// A set is offered to the phone as a choice of its own, first.
	dir := t.TempDir()
	st := Check(dir, Settings{Enabled: true, Model: "tiny", Language: "id,en"})
	if st.Language != "id,en" || st.Languages[0].Code != "id,en" || st.Languages[0].Name != "Indonesian or English" {
		t.Fatalf("set: %+v", st.Languages[:2])
	}
}

// inferenceAmong takes whisper's guess when it is one of the languages,
// in one pass; else it writes it down again in the likeliest of them.
func TestInferenceAmong(t *testing.T) {
	var asked []string
	probs := map[string]float64{}
	language := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang, format := r.FormValue("language"), r.FormValue("response_format")
		asked = append(asked, lang+"/"+format)
		ans := map[string]any{"text": " in " + lang}
		if format == "verbose_json" {
			ans["language"] = language
			if probs != nil {
				ans["language_probabilities"] = probs
			}
		}
		json.NewEncoder(w).Encode(ans)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	for _, tc := range []struct {
		probs    map[string]float64
		language string
		text     string
		asked    []string
	}{
		// Guessed Indonesian: one pass.
		{map[string]float64{"id": 0.7, "en": 0.2, "ms": 0.1}, "indonesian", "in auto", []string{"auto/verbose_json"}},
		// Heard Russian; English is likelier than Indonesian.
		{map[string]float64{"ru": 0.5, "en": 0.3, "id": 0.1}, "russian", "in en", []string{"auto/verbose_json", "en/json"}},
		// Neither scored: the first of them.
		{map[string]float64{"zh": 0.9}, "chinese", "in id", []string{"auto/verbose_json", "id/json"}},
		// An older whisper-server tells only what it wrote in: English,
		// one of them, takes one pass; Russian, the first of them.
		{nil, "english", "in auto", []string{"auto/verbose_json"}},
		{nil, "russian", "in id", []string{"auto/verbose_json", "id/json"}},
	} {
		asked, probs, language = nil, tc.probs, tc.language
		text, err := inferenceAmong(context.Background(), addr, wav(10, SampleRate, 1), []string{"id", "en"})
		if err != nil || text != tc.text || !slices.Equal(asked, tc.asked) {
			t.Errorf("%v: %q %v, asked %q; want %q, %q", tc.probs, text, err, asked, tc.text, tc.asked)
		}
	}
}

// Another model waits for the recordings the running one is writing down,
// rather than killing it under them.
func TestServerModelChangeWaits(t *testing.T) {
	t.Setenv("OMATERM_FAKE_WHISPER", "1")
	dir := t.TempDir()
	tiny, small := filepath.Join(dir, "ggml-tiny.bin"), filepath.Join(dir, "ggml-small.bin")
	os.WriteFile(tiny, []byte("x"), 0o600)
	os.WriteFile(small, []byte("x"), 0o600)
	bin, _ := os.Executable()
	s := &Server{}
	defer s.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	addr, err := s.acquire(ctx, bin, tiny)
	if err != nil {
		t.Fatal(err)
	}
	switched := make(chan error, 1)
	go func() {
		_, err := s.acquire(ctx, bin, small)
		switched <- err
	}()
	select {
	case err := <-switched:
		t.Fatalf("switched models under a recording: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	// The recording on tiny still gets its answer.
	if text, err := inference(ctx, addr, wav(SampleRate, SampleRate, 1), "id"); err != nil || text != "said in id with ggml-tiny.bin" {
		t.Fatalf("recording under way: %q %v", text, err)
	}
	s.release()
	if err := <-switched; err != nil {
		t.Fatal(err)
	}
	s.release()
	if s.model != small {
		t.Fatalf("runs %s", s.model)
	}
}
