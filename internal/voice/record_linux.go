package voice

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Recorder is a program that records the microphone into a WAV file and
// finishes the file when interrupted.
type Recorder struct {
	Name string
	// Args are its arguments to record 16 kHz mono 16-bit WAV into file.
	Args func(file string) []string
}

// Recorders are tried in turn, the first on the PATH recording: PipeWire's,
// PulseAudio's, then ALSA's.
var Recorders = []Recorder{
	{"pw-record", func(f string) []string {
		return []string{"--rate", strconv.Itoa(SampleRate), "--channels", "1", "--format", "s16", f}
	}},
	{"parecord", func(f string) []string {
		return []string{"--rate=" + strconv.Itoa(SampleRate), "--channels=1", "--format=s16le", "--file-format=wav", f}
	}},
	{"arecord", func(f string) []string {
		return []string{"-q", "-t", "wav", "-f", "S16_LE", "-r", strconv.Itoa(SampleRate), "-c", "1", f}
	}},
}

// ErrNoRecorder is a computer with none of the Recorders.
var ErrNoRecorder = errors.New("nothing to record with: install PipeWire (pw-record), PulseAudio (parecord) or ALSA (arecord)")

// FindRecorder is the first of the Recorders on the PATH, and where it is.
func FindRecorder() (Recorder, string, error) {
	for _, r := range Recorders {
		if p, err := exec.LookPath(r.Name); err == nil {
			return r, p, nil
		}
	}
	return Recorder{}, "", ErrNoRecorder
}

// stopWait is how long a recorder may take to finish its file once
// interrupted, before it is killed.
var stopWait = 3 * time.Second

// Recording is a recorder running.
type Recording struct {
	cmd  *exec.Cmd
	file string
	tail *tailBuffer
	done chan struct{} // closed as the recorder ends
	err  error         // how it ended, once done
	once sync.Once
}

// Record starts the first of the Recorders, writing into a temporary file
// in dir, until Stop or Cancel.
func Record(dir string) (*Recording, error) {
	rec, path, err := FindRecorder()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, "recording-*.wav")
	if err != nil {
		return nil, err
	}
	f.Close()
	r := &Recording{file: f.Name(), tail: &tailBuffer{max: 4096}, done: make(chan struct{})}
	r.cmd = exec.Command(path, rec.Args(r.file)...)
	r.cmd.Stdout, r.cmd.Stderr = r.tail, r.tail
	setProcAttr(r.cmd)
	if err := r.cmd.Start(); err != nil {
		os.Remove(r.file)
		return nil, fmt.Errorf("%s: %w", rec.Name, err)
	}
	go func() {
		r.err = r.cmd.Wait()
		close(r.done)
	}()
	return r, nil
}

// Done is closed as the recorder ends: by Stop or Cancel, or by itself,
// as when there is no microphone, which Stop then tells.
func (r *Recording) Done() <-chan struct{} { return r.done }

// end interrupts the recorder, which finishes its file, and waits for it.
func (r *Recording) end() (stopped bool) {
	select {
	case <-r.done:
		return false
	default:
	}
	r.cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-r.done:
	case <-time.After(stopWait):
		r.cmd.Process.Kill()
		<-r.done
	}
	return true
}

// Stop ends the recording and gives it, as a WAV CheckWAV takes.
func (r *Recording) Stop() ([]byte, error) {
	stopped := r.end()
	defer os.Remove(r.file)
	b, err := os.ReadFile(r.file)
	if err != nil {
		return nil, err
	}
	b = finishWAV(b)
	if _, err := CheckWAV(b); err != nil {
		if !stopped {
			// It ended by itself: what it said is why.
			return nil, fmt.Errorf("%s stopped: %s", r.cmd.Args[0], firstNonEmpty(r.tail.last(), errString(r.err)))
		}
		return nil, err
	}
	return b, nil
}

// Cancel ends the recording, keeping nothing of it.
func (r *Recording) Cancel() {
	r.end()
	os.Remove(r.file)
}

// finishWAV writes the sizes a recorder ended abruptly left out of its
// header (0, or 0xFFFFFFFF as when streaming), the data running to the end
// of the file, and cuts the data at MaxLength, which a recording stopped
// at its limit may pass by a little.
func finishWAV(b []byte) []byte {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return b
	}
	for at := 12; at+8 <= len(b); {
		size := int(binary.LittleEndian.Uint32(b[at+4:]))
		body := at + 8
		if string(b[at:at+4]) == "data" {
			n := min((len(b)-body)&^1, int(MaxLength/time.Second)*SampleRate*2)
			if size == 0 || size > n {
				size = n
			}
			binary.LittleEndian.PutUint32(b[at+4:], uint32(size))
			b = b[:body+size]
			binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
			return b
		}
		if size > len(b)-body {
			return b
		}
		at = body + size + size%2
	}
	return b
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func errString(err error) string {
	if err == nil {
		return "it ended"
	}
	return err.Error()
}
