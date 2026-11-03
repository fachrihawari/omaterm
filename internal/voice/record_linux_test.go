package voice

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRecorders puts fake recorders named names on a PATH of their own:
// each notes its arguments, writes the WAV in src to the file it is given,
// and runs until interrupted, or, with fail, says so and ends at once.
func fakeRecorders(t *testing.T, src string, fail bool, names ...string) (args string) {
	t.Helper()
	bin := t.TempDir()
	args = filepath.Join(t.TempDir(), "args")
	body := `trap 'exit 0' INT
for f; do :; done
cat "` + src + `" > "$f"
while :; do sleep 0.02; done`
	if fail {
		body = `echo "no such audio device" >&2; exit 1`
	}
	for _, n := range names {
		script := "#!/bin/sh\nPATH=/usr/bin:/bin\necho " + n + ` "$@" > "` + args + "\"\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(bin, n), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	return args
}

// The first recorder on the PATH records: PipeWire's, else PulseAudio's,
// else ALSA's; with none, it says what to install.
func TestFindRecorder(t *testing.T) {
	for _, tc := range []struct {
		have []string
		want string
	}{
		{[]string{"arecord"}, "arecord"},
		{[]string{"parecord", "arecord"}, "parecord"},
		{[]string{"arecord", "parecord", "pw-record"}, "pw-record"},
	} {
		fakeRecorders(t, "/dev/null", false, tc.have...)
		if r, path, err := FindRecorder(); err != nil || r.Name != tc.want || filepath.Base(path) != tc.want {
			t.Errorf("with %v: %q at %s, %v; want %s", tc.have, r.Name, path, err, tc.want)
		}
	}
	fakeRecorders(t, "/dev/null", false)
	if _, err := Record(t.TempDir()); !errors.Is(err, ErrNoRecorder) {
		t.Fatalf("with none: %v", err)
	}
}

// A recording records 16 kHz mono 16-bit WAV into a file of its own in the
// folder given, until interrupted: Stop gives the WAV, Cancel nothing, and
// neither leaves the file behind.
func TestRecordStopAndCancel(t *testing.T) {
	src := filepath.Join(t.TempDir(), "in.wav")
	os.WriteFile(src, wav(SampleRate/2, SampleRate, 1), 0o600)
	for _, name := range []string{"pw-record", "parecord", "arecord"} {
		args := fakeRecorders(t, src, false, name)
		dir := t.TempDir()
		r, err := Record(dir)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		select {
		case <-r.Done():
			t.Fatalf("%s ended before Stop", name)
		default:
		}
		b, err := r.Stop()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if d, err := CheckWAV(b); err != nil || d != 500*time.Millisecond {
			t.Fatalf("%s gave %v, %v", name, d, err)
		}
		got, _ := os.ReadFile(args)
		a := strings.Fields(string(got))
		if !strings.Contains(string(got), "16000") || !strings.HasPrefix(a[len(a)-1], dir+"/recording-") {
			t.Fatalf("%s ran as %q", name, got)
		}
		if left, _ := os.ReadDir(dir); len(left) != 0 {
			t.Fatalf("%s left %v", name, left)
		}

		r, err = Record(dir)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		r.Cancel()
		<-r.Done()
		if left, _ := os.ReadDir(dir); len(left) != 0 {
			t.Fatalf("%s left %v after Cancel", name, left)
		}
	}
}

// A recorder that ends by itself, as with no microphone, ends the
// recording, and Stop says why.
func TestRecordFails(t *testing.T) {
	fakeRecorders(t, "/dev/null", true, "pw-record")
	r, err := Record(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("still recording")
	}
	if _, err := r.Stop(); err == nil || !strings.Contains(err.Error(), "no such audio device") {
		t.Fatalf("Stop: %v", err)
	}
}

// A WAV whose recorder was killed has no sizes in its header: the data
// runs to the end. One that ran past the limit is cut there.
func TestFinishWAV(t *testing.T) {
	b := wav(SampleRate, SampleRate, 1)
	binary.LittleEndian.PutUint32(b[4:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(b[40:], 0xFFFFFFFF)
	if d, err := CheckWAV(finishWAV(b)); err != nil || d != time.Second {
		t.Fatalf("unsized: %v %v", d, err)
	}
	b = wav(SampleRate*121, SampleRate, 1)
	if d, err := CheckWAV(finishWAV(b)); err != nil || d != MaxLength {
		t.Fatalf("past the limit: %v %v", d, err)
	}
}
