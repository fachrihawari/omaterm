package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/voice"
	"omaterm/internal/web"
)

func TestMediaKind(t *testing.T) {
	for path, want := range map[string]string{
		"doc.pdf":        "pdf",
		"Docs/Spec.PDF":  "pdf",
		"song.mp3":       "audio",
		"a/b/voice.opus": "audio",
		"take.FLAC":      "audio",
		"clip.mp4":       "video",
		"clip.webm":      "video",
		"clip.mov":       "video",
		"main.go":        "",
		"logo.png":       "",
		"pdf":            "",
		"notes.pdf.txt":  "",
	} {
		if got := mediaKind(path); got != want {
			t.Errorf("mediaKind(%q) = %q, want %q", path, got, want)
		}
	}
}

// A media file opens without being read, whatever its size, and closing
// its tab stops serving it.
func TestMediaOpensUnread(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	f, _ := os.Create(filepath.Join(a.workspace, "big.mp4"))
	f.Truncate(maxEditorSize + 1)
	f.Close()
	a.openEditor("big.mp4")
	doc := a.editors["big.mp4"]
	if doc == nil || doc.Media != "video" || doc.TooLarge || doc.Binary || doc.State != nil || doc.Size != maxEditorSize+1 {
		t.Fatalf("doc = %+v", doc)
	}
	// With no web view, as here, the tab says so and still opens the file
	// where the system would.
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("This file can't be previewed here") || !tr.HasText("Video") || tr.HasText("too large to open here") {
		t.Fatalf("texts = %q", tr.Texts())
	}
	a.closeTab(a.activeTab)
	if a.editors["big.mp4"] != nil {
		t.Fatal("editor kept after its tab closed")
	}
}

func TestFileServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip one.mp4")
	os.WriteFile(path, []byte("0123456789"), 0o644)
	s := &fileServer{}
	u, release, err := s.serve(path, "video", playerLook{Background: "#000", Text: "#fff", Muted: "#888"})
	if err != nil {
		t.Fatal(err)
	}
	get := func(url, rng string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", url, nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	// The player page, under its CSP, plays the file beside it.
	res := get(u, "")
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(page), `<video controls autoplay src="clip%20one.mp4">`) ||
		!strings.Contains(res.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("player: %d %s %q", res.StatusCode, res.Header.Get("Content-Security-Policy"), page)
	}

	// A Range request gets the part asked for, so a video seeks.
	res = get(u+"clip%20one.mp4", "bytes=2-5")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || string(body) != "2345" || res.Header.Get("Content-Type") != "video/mp4" ||
		res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("range: %d %q %v", res.StatusCode, body, res.Header)
	}

	// Another name under the token, or an unknown token, is not found.
	base := u[:strings.LastIndex(strings.TrimSuffix(u, "/"), "/")]
	for _, url := range []string{u + "other.mp4", base + "/nope/clip%20one.mp4", base + "/"} {
		if res := get(url, ""); res.StatusCode != 404 {
			t.Errorf("%s: %d, want 404", url, res.StatusCode)
		}
	}

	// Released, the token serves nothing.
	release()
	release()
	if res := get(u+"clip%20one.mp4", ""); res.StatusCode != 404 {
		t.Fatalf("after release: %d, want 404", res.StatusCode)
	}
	if len(s.files) != 0 {
		t.Fatalf("files = %v", s.files)
	}
}

// A PDF is served as itself: the web view's viewer shows it.
func TestFileServerPDF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.pdf")
	os.WriteFile(path, []byte("%PDF-1.4"), 0o644)
	s := &fileServer{}
	u, release, err := s.serve(path, "pdf", playerLook{})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if !strings.HasSuffix(u, "/spec.pdf") {
		t.Fatalf("url = %s", u)
	}
	res, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/pdf" {
		t.Fatalf("%d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
}

// The recorder page (macOS's voice input) is served under its token with
// web access's voice.js, and takes the WAV it posts, or why it couldn't
// record; nothing else.
func TestRecorderPage(t *testing.T) {
	rec := newPageRecorder()
	u, release, err := mediaFiles.serveRecorder(rec)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	res, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || !strings.Contains(string(page), `src="record.js"`) {
		t.Fatalf("page %q, CSP %q", page, csp)
	}
	res, _ = http.Get(u + "voice.js")
	js, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !bytes.Equal(js, web.VoiceScript()) || len(js) == 0 {
		t.Fatal("voice.js isn't web access's")
	}
	if res, _ := http.Post(u+"wav", "audio/wav", strings.NewReader("not a wav")); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("a bad WAV: %s", res.Status)
	}
	<-rec.done
	if rec.err == nil {
		t.Fatal("a bad WAV was taken")
	}

	rec = newPageRecorder()
	u2, release2, _ := mediaFiles.serveRecorder(rec)
	defer release2()
	wav := make([]byte, 44+3200)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	for at, v := range map[int]uint32{16: 16, 24: voice.SampleRate, 28: 2 * voice.SampleRate, 40: 3200} {
		binary.LittleEndian.PutUint32(wav[at:], v)
	}
	for at, v := range map[int]uint16{20: 1, 22: 1, 32: 2, 34: 16} {
		binary.LittleEndian.PutUint16(wav[at:], v)
	}
	copy(wav[36:], "data")
	if res, err := http.Post(u2+"wav", "audio/wav", bytes.NewReader(wav)); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("a good WAV: %v %v", res, err)
	}
	<-rec.done
	if rec.err != nil || !bytes.Equal(rec.wav, wav) {
		t.Fatalf("got %d bytes, %v", len(rec.wav), rec.err)
	}
	// What came first stays: a later error doesn't replace the recording.
	http.Post(u2+"error", "text/plain", strings.NewReader("late"))
	if rec.err != nil {
		t.Fatal("a later error replaced the recording")
	}
	release2()
	if res, _ := http.Get(u2); res.StatusCode != http.StatusNotFound {
		t.Fatalf("released, the page is %s", res.Status)
	}
}
