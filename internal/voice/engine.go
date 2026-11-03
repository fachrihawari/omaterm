package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// lookPath finds whisper-server on the PATH; tests swap it.
var lookPath = exec.LookPath

// Find is the whisper-server to run: one on the PATH first, as a package
// manager installs it, else the one downloaded into dir. ok is false when
// there is neither.
func Find(dir string) (path string, onPATH, ok bool) {
	if p, err := lookPath("whisper-server"); err == nil {
		return p, true, true
	}
	p := EnginePath(dir)
	if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
		return p, false, true
	}
	return "", false, false
}

// Status is what the phone and Settings are told of voice input. Off, it
// is only {"enabled":false}.
type Status struct {
	Enabled bool `json:"enabled"`
	// Ready says a recording can be written down: the engine and the
	// model are there. Reason says why not.
	Ready     bool       `json:"ready,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	Model     string     `json:"model,omitempty"`
	Language  string     `json:"language,omitempty"`
	Languages []Language `json:"languages,omitempty"`
	// Running says whisper-server runs now.
	Running bool `json:"running,omitempty"`
	// Engine is the whisper-server that runs, and EngineOnPATH whether it
	// is the PATH's.
	Engine       string `json:"engine,omitempty"`
	EngineOnPATH bool   `json:"engineOnPATH,omitempty"`
}

// Check tells whether voice input can work with the settings s and what
// dir holds, starting and downloading nothing.
func Check(dir string, s Settings) Status {
	if !s.Enabled {
		return Status{}
	}
	st := Status{Enabled: true, Model: s.Model, Language: s.Language, Languages: Languages}
	if st.Model == "" {
		st.Model = DefaultModel
	}
	if !KnownLanguage(st.Language) {
		st.Language = DefaultLanguage
	}
	st.Engine, st.EngineOnPATH, st.Ready = Find(dir)
	if !st.Ready {
		st.Reason = "No engine: download it in Settings › Voice on your computer"
		return st
	}
	m, ok := ModelByName(st.Model)
	if fi, err := os.Stat(ModelPath(dir, m)); !ok || err != nil || fi.Size() == 0 {
		st.Ready = false
		st.Reason = "No model: download one in Settings › Voice on your computer"
	}
	return st
}

// ErrOff is a recording sent while voice input is off.
var ErrOff = errors.New("voice input is off")

// NotReadyError is a recording voice input can't write down yet: Reason
// says what is missing.
type NotReadyError struct{ Reason string }

func (e *NotReadyError) Error() string { return e.Reason }

// IdleStop is how long whisper-server keeps running with nothing to do.
var IdleStop = 10 * time.Minute

// startWait is how long whisper-server may take to load its model and
// listen.
var startWait = 90 * time.Second

// Server runs whisper-server on the loopback while it is used: started by
// the first recording, stopped after IdleStop with nothing to do, or when
// it is to run another model. A process of its own, a crash in whisper
// can't take down the daemon, which holds every terminal.
type Server struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	exit chan struct{} // closed as the process ends
	addr string
	// bin and model are what it runs, to start again when either changes.
	bin, model string
	busy       int
	idle       *time.Timer
	// tail is the end of what it printed, to say why it failed.
	tail *tailBuffer
	// running is whether it runs or starts, read without the lock, which
	// a start holds while the model loads.
	running atomic.Bool
}

// Running tells whether whisper-server runs, or is starting.
func (s *Server) Running() bool { return s.running.Load() }

func (s *Server) alive() bool {
	if s.cmd == nil {
		return false
	}
	select {
	case <-s.exit:
		return false
	default:
		return true
	}
}

// Stop stops whisper-server, if it runs.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

func (s *Server) stopLocked() {
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	if s.cmd == nil {
		return
	}
	s.cmd.Process.Kill()
	<-s.exit
	s.cmd, s.addr = nil, ""
	log.Printf("voice: whisper-server stopped")
}

// Transcribe writes down what the WAV says, in lang ("auto" or "" for
// whisper to tell), with whisper-server bin running model: started now
// if it doesn't run, or runs another.
func (s *Server) Transcribe(ctx context.Context, bin, model string, wav []byte, lang string) (string, error) {
	addr, err := s.acquire(ctx, bin, model)
	if err != nil {
		return "", err
	}
	defer s.release()
	if lang == "" {
		lang = DefaultLanguage
	}
	return inference(ctx, addr, wav, lang)
}

// acquire starts whisper-server when needed and counts a use, which keeps
// it from its idle stop.
func (s *Server) acquire(ctx context.Context, bin, model string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alive() && (s.bin != bin || s.model != model) {
		s.stopLocked()
	}
	if !s.alive() {
		s.cmd = nil
		if err := s.start(ctx, bin, model); err != nil {
			return "", err
		}
	}
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	s.busy++
	return s.addr, nil
}

// release ends a use; the last one starts the idle countdown.
func (s *Server) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy--
	if s.busy > 0 || s.cmd == nil {
		return
	}
	var t *time.Timer
	t = time.AfterFunc(IdleStop, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.idle == t && s.busy == 0 {
			s.idle = nil
			s.stopLocked()
		}
	})
	s.idle = t
}

// start runs whisper-server on a free loopback port and waits until it
// listens, which it does once its model is loaded.
func (s *Server) start(ctx context.Context, bin, model string) error {
	port, err := freePort()
	if err != nil {
		return err
	}
	threads := max(1, runtime.NumCPU()/2)
	cmd := exec.Command(bin, "-m", model, "--host", "127.0.0.1", "--port", strconv.Itoa(port), "-t", strconv.Itoa(threads))
	s.tail = &tailBuffer{max: 2 << 10}
	cmd.Stdout, cmd.Stderr = s.tail, s.tail
	setProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting whisper-server: %w", err)
	}
	exit := make(chan struct{})
	s.running.Store(true)
	go func() {
		cmd.Wait()
		s.running.Store(false)
		close(exit)
	}()
	s.cmd, s.exit, s.bin, s.model = cmd, exit, bin, model
	s.addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	log.Printf("voice: whisper-server on %s with %s", s.addr, model)
	deadline := time.NewTimer(startWait)
	defer deadline.Stop()
	for {
		if c, err := net.DialTimeout("tcp", s.addr, time.Second); err == nil {
			c.Close()
			return nil
		}
		select {
		case <-exit:
			s.cmd = nil
			return fmt.Errorf("whisper-server exited: %s", s.tail.last())
		case <-ctx.Done():
			s.stopLocked()
			return ctx.Err()
		case <-deadline.C:
			s.stopLocked()
			return errors.New("whisper-server did not start in time")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// inference posts the WAV to whisper-server's /inference.
func inference(ctx context.Context, addr string, wav []byte, lang string) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", err
	}
	fw.Write(wav)
	mw.WriteField("response_format", "json")
	mw.WriteField("language", lang)
	mw.WriteField("temperature", "0")
	mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/inference", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("whisper-server: %s: %s", res.Status, strings.TrimSpace(string(b)))
	}
	if out.Error != "" || res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whisper-server: %s %s", res.Status, out.Error)
	}
	return strings.TrimSpace(out.Text), nil
}

// freePort is a loopback port free now.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

// last is its last line that says something.
func (t *tailBuffer) last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.b)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
