package main

// The file server of media editors (media.go): a loopback HTTP server
// that serves the files open so, each under a random token, and nothing
// else. Not file:// nor a scheme of the app's: mygo lets those pages call
// the app's bound methods, and a PDF's scripts would be such a page. On
// macOS it also serves the page that records voice input (dictation.go).

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"omaterm/internal/voice"
	"omaterm/internal/web"
)

// mediaFiles is the app's file server, started as a file first opens.
var mediaFiles = &fileServer{}

// playerLook is the colors of the audio and video player page, the
// editor's as the file opened.
type playerLook struct{ Background, Text, Muted string }

// servedFile is a file the server serves, and how; or, for kind
// "recorder", the page that records voice input.
type servedFile struct {
	path, kind string
	look       playerLook
	rec        *pageRecorder
}

type fileServer struct {
	mu    sync.Mutex
	base  string // http://127.0.0.1:port, once listening
	files map[string]servedFile
}

// serve serves the file at abs under a new token: the URL to load, which
// is the file itself for a PDF and the player page for audio and video,
// and release, which stops serving it.
func (s *fileServer) serve(abs, kind string, look playerLook) (string, func(), error) {
	u, release, err := s.add(servedFile{path: abs, kind: kind, look: look})
	if err == nil && kind == "pdf" {
		u += url.PathEscape(filepath.Base(abs))
	}
	return u, release, err
}

// serveRecorder serves a page that records voice input into rec: its URL,
// and release, which stops serving it.
func (s *fileServer) serveRecorder(rec *pageRecorder) (string, func(), error) {
	return s.add(servedFile{kind: "recorder", rec: rec})
}

// add serves f under a new token, listening first if the server doesn't
// yet.
func (s *fileServer) add(f servedFile) (string, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.base == "" {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", nil, err
		}
		s.base = "http://" + ln.Addr().String()
		go http.Serve(ln, s)
	}
	if s.files == nil {
		s.files = map[string]servedFile{}
	}
	var b [16]byte
	rand.Read(b[:])
	token := hex.EncodeToString(b[:])
	s.files[token] = f
	u := s.base + "/" + token + "/"
	var once sync.Once
	return u, func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.files, token)
			s.mu.Unlock()
		})
	}, nil
}

// ServeHTTP serves /<token>/<name>, the file, with Range requests, so a
// video seeks; and /<token>/, the player page of a song or a video.
func (s *fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token, name, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	s.mu.Lock()
	f, ok := s.files[token]
	s.mu.Unlock()
	if ok && f.rec != nil {
		f.rec.ServeHTTP(w, r, name)
		return
	}
	if !ok || r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	// Revalidated each time: Reload shows the file as it is now.
	h.Set("Cache-Control", "no-cache")
	if name == "" && f.kind != "pdf" {
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", "default-src 'none'; media-src 'self'; style-src 'unsafe-inline'; script-src 'sha256-"+playerScriptHash+"'")
		w.Write([]byte(playerPage(f)))
		return
	}
	if name != filepath.Base(f.path) {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(f.path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	if t := mediaTypes[lowerExt(f.path)].mime; t != "" {
		h.Set("Content-Type", t)
	}
	http.ServeContent(w, r, name, st.ModTime(), file)
}

// playerScript tells a format the web view can't play, rather than
// showing a player that never starts: an error, or nothing read of it
// in 5s, as WebKitGTK without GStreamer's plugins stalls, never failing.
const playerScript = `const m=document.querySelector("audio,video"),e=document.getElementById("err"),no=()=>{e.hidden=false};m.addEventListener("error",no);setTimeout(()=>{if(m.readyState===0)no()},5000)`

// playerScriptHash is playerScript's, which the page's CSP allows alone.
var playerScriptHash = scriptHash(playerScript)

// scriptHash is a script's CSP hash, base64 of its SHA-256.
func scriptHash(js string) string {
	sum := sha256.Sum256([]byte(js))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// cantPlay is the note of a song or video the web view can't play. On
// Linux it is most often GStreamer's plugins that are missing.
var cantPlay = func() string {
	if runtime.GOOS == "linux" {
		return "This can't be played here: open it in the default app, or install GStreamer's plugins (gst-plugins-good, gst-libav)."
	}
	return "This format can't be played here: open it in the default app."
}()

// playerPage is the page of a song or a video: the player, in the
// editor's colors, and a note it shows when the format can't play.
func playerPage(f servedFile) string {
	name := html.EscapeString(filepath.Base(f.path))
	src := html.EscapeString(url.PathEscape(filepath.Base(f.path)))
	player := `<video controls autoplay src="` + src + `"></video>`
	if f.kind == "audio" {
		player = `<div class="name">` + name + `</div><audio controls src="` + src + `"></audio>`
	}
	return `<!doctype html><html><head><meta charset="utf-8"><title>` + name + `</title><style>
html,body{margin:0;height:100%;background:` + f.look.Background + `;color:` + f.look.Text + `;font:13px system-ui,sans-serif}
body{display:flex;flex-direction:column;align-items:center;justify-content:center;gap:14px;box-sizing:border-box;padding:16px}
video{max-width:100%;max-height:100%;min-height:0;flex:0 1 auto;background:#000}
audio{width:min(560px,100%)}
.name{font-weight:600}
#err{color:` + f.look.Muted + `;text-align:center}
</style></head><body>` + player + `<div id="err" hidden>` + cantPlay + `</div>
<script>` + playerScript + `</script></body></html>`
}

// pageRecorder is a recording a web page makes, where there is no
// recorder to run (macOS): the page, recorderPage, records with
// getUserMedia and web access's voice.js, and posts the WAV, or why it
// couldn't record.
type pageRecorder struct {
	once sync.Once
	done chan struct{} // closed as the page sends either
	wav  []byte
	err  error
}

func newPageRecorder() *pageRecorder { return &pageRecorder{done: make(chan struct{})} }

// finish keeps what the page sent first.
func (p *pageRecorder) finish(wav []byte, err error) {
	p.once.Do(func() {
		p.wav, p.err = wav, err
		close(p.done)
	})
}

// recorderScript runs the recording: from the page's load, until the app
// evaluates voiceStop() or voiceCancel().
const recorderScript = `import { record } from "./voice.js";
const send = (what, body) => fetch(what, { method: "POST", body });
const started = record().catch((err) => {
  send("error", err?.name === "NotAllowedError" ? "Microphone access was refused" : "The microphone didn't start: " + (err?.message || err));
  return null;
});
window.voiceStop = async () => {
  const rec = await started;
  if (!rec) return;
  try {
    await send("wav", await rec.stop());
  } catch (err) {
    send("error", "Not recorded: " + (err?.message || err));
  }
};
window.voiceCancel = async () => (await started)?.cancel();
`

// ServeHTTP serves the recorder's page and its scripts, and takes what
// the page posts: wav, the recording, or error.
func (p *pageRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request, name string) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	switch {
	case r.Method == http.MethodGet && name == "":
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'")
		io.WriteString(w, `<!doctype html><meta charset="utf-8"><title>Voice input</title><script type="module" src="record.js"></script>`)
	case r.Method == http.MethodGet && name == "record.js":
		h.Set("Content-Type", "text/javascript; charset=utf-8")
		io.WriteString(w, recorderScript)
	case r.Method == http.MethodGet && name == "voice.js":
		h.Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(web.VoiceScript())
	case r.Method == http.MethodPost && name == "wav":
		// A little over 2 minutes of it, as web access takes.
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err == nil {
			_, err = voice.CheckWAV(b)
		}
		if err != nil {
			p.finish(nil, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p.finish(b, nil)
	case r.Method == http.MethodPost && name == "error":
		b, _ := io.ReadAll(io.LimitReader(r.Body, 512))
		p.finish(nil, errors.New(firstNonEmpty(strings.TrimSpace(string(b)), "The microphone didn't start")))
	default:
		http.NotFound(w, r)
	}
}
