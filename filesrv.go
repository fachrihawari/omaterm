package main

// The file server of media editors (media.go): a loopback HTTP server
// that serves the files open so, each under a random token, and nothing
// else. Not file:// nor a scheme of the app's: mygo lets those pages call
// the app's bound methods, and a PDF's scripts would be such a page.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// mediaFiles is the app's file server, started as a file first opens.
var mediaFiles = &fileServer{}

// playerLook is the colors of the audio and video player page, the
// editor's as the file opened.
type playerLook struct{ Background, Text, Muted string }

// servedFile is a file the server serves, and how.
type servedFile struct {
	path, kind string
	look       playerLook
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
	s.files[token] = servedFile{path: abs, kind: kind, look: look}
	u := s.base + "/" + token + "/"
	if kind == "pdf" {
		u += url.PathEscape(filepath.Base(abs))
	}
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
