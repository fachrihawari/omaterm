package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"omaterm/internal/voice"
)

// fakeRecording records nothing: Stop gives wav.
type fakeRecording struct {
	done              chan struct{}
	wav               []byte
	stopped, canceled atomic.Bool
}

func (f *fakeRecording) Done() <-chan struct{} { return f.done }
func (f *fakeRecording) Stop() ([]byte, error) { f.stopped.Store(true); return f.wav, nil }
func (f *fakeRecording) Cancel()               { f.canceled.Store(true) }

// dictationApp is a test app with voice input as the settings make it, a
// fake recorder and a fake transcriber, which writes down what it is
// sent once let go.
type dictationApp struct {
	a       *appState
	tr      *ui.Tester
	term    *terminal.Terminal
	recs    []*fakeRecording
	heard   chan string // the language of each recording sent
	release chan string // what the transcriber answers
}

func newDictationApp(t *testing.T) *dictationApp {
	t.Helper()
	if startRecording == nil {
		t.Skip("no desktop voice input here")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	da := &dictationApp{heard: make(chan string, 4), release: make(chan string, 4)}
	start, transcribe, daemonOn := startRecording, transcribeRecording, voiceDaemon
	t.Cleanup(func() { startRecording, transcribeRecording, voiceDaemon = start, transcribe, daemonOn })
	startRecording = func(*appState) (recording, error) {
		r := &fakeRecording{done: make(chan struct{}), wav: []byte("RIFF")}
		da.recs = append(da.recs, r)
		return r, nil
	}
	transcribeRecording = func(ctx context.Context, wav []byte, lang string) (string, error) {
		da.heard <- lang
		select {
		case text := <-da.release:
			return text, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	voiceDaemon = func(*appState) bool { return true }

	da.a = newTestApp(t)
	t.Cleanup(func() { stopTermPanes(da.a) })
	da.tr = ui.NewTester(da.a.view, 1440, 900)
	da.tr.Frame()
	if p := da.a.panes.Get(da.a.activeSplitLeaf()); p != nil {
		da.term = p.Term()
	}
	if da.term == nil {
		t.Fatal("no terminal pane")
	}
	return da
}

// ready turns voice input on with an engine and a model in place.
func (da *dictationApp) ready() {
	dir := voiceDir()
	os.MkdirAll(filepath.Dir(voice.EnginePath(dir)), 0o700)
	os.WriteFile(voice.EnginePath(dir), []byte("x"), 0o700)
	m, _ := voice.ModelByName(voice.DefaultModel)
	os.WriteFile(voice.ModelPath(dir, m), []byte("x"), 0o600)
	da.a.settings.Voice = voice.Settings{Enabled: true, Language: "id"}
	da.tr.Frame()
}

// hover puts the pointer on the pane, whose header then shows its
// controls.
func (da *dictationApp) hover() {
	da.tr.Move(900, 450)
	da.tr.Frame()
}

// settle runs frames until the dictation is over, or fails.
func (da *dictationApp) settle(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); da.a.dictation != nil; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the dictation never ended")
		}
		da.tr.Frame()
	}
	da.tr.Frame()
}

// dictate is the key that starts and stops it, tapped: its modifiers let
// go of at once.
func (da *dictationApp) dictate() {
	da.tr.Key(ui.Cmd|ui.Shift, ui.KeySpace)
	da.tr.HoldModifiers(0)
	da.tr.Frame()
}

// waitText waits for the pane to show want, or fails.
func (da *dictationApp) waitText(t *testing.T, want string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		da.tr.Frame()
		if strings.Contains(da.term.Text(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never showed %q: %q", want, da.term.Text())
		}
	}
}

// Off, as by default, the header has no mic, and the key records nothing:
// it points to Settings.
// timerText is the recording chip's time, as 0:07.
var timerText = regexp.MustCompile(`^\d+:\d\d$`)

func TestDictationOff(t *testing.T) {
	da := newDictationApp(t)
	da.hover()
	if _, ok := da.tr.Find("Dictate"); ok {
		t.Fatal("a mic while voice input is off")
	}
	da.dictate()
	if da.a.dictation != nil || len(da.recs) != 0 {
		t.Fatal("recorded while voice input is off")
	}
	if !da.tr.HasText("Settings › Voice") {
		t.Fatalf("no toast pointing to Settings: %q", da.tr.Texts())
	}
	// On, but with nothing downloaded: still no mic.
	da.a.settings.Voice.Enabled = true
	da.hover()
	if _, ok := da.tr.Find("Dictate"); ok {
		t.Fatal("a mic while voice input isn't ready")
	}
	da.ready()
	da.hover()
	if _, ok := da.tr.Find("Dictate"); !ok {
		t.Fatal("no mic once voice input is ready")
	}
}

// The key starts recording into the focused pane, whose header shows a
// red dot and a timer; again, it stops, and the text comes, written down
// in the settings' language, pasted into the pane without Enter.
func TestDictationPastesWithoutEnter(t *testing.T) {
	da := newDictationApp(t)
	da.ready()
	pane := da.a.activeSplitLeaf()
	da.dictate()
	d := da.a.dictation
	if d == nil || d.pane != pane || d.step != dictRecording || len(da.recs) != 1 {
		t.Fatalf("not recording: %+v", d)
	}
	// The timer, whatever second a slow run (-race) is at.
	if !slices.ContainsFunc(da.tr.Texts(), func(s string) bool { return timerText.MatchString(s) }) {
		t.Fatalf("no timer: %q", da.tr.Texts())
	}
	// The chip says what it listens for.
	if !da.tr.HasText("ID") {
		t.Fatalf("no language: %q", da.tr.Texts())
	}
	da.dictate()
	if !da.recs[0].stopped.Load() && da.a.dictation.step != dictTranscribing {
		t.Fatal("the second press didn't stop it")
	}
	if lang := <-da.heard; lang != "id" {
		t.Fatalf("written down in %q", lang)
	}
	da.tr.Frame()
	if !da.tr.HasText("Writing it down…") {
		t.Fatalf("no spinner while writing it down: %q", da.tr.Texts())
	}
	// The shell's answer, 42_RAN, is in the pane only once it runs.
	da.release <- " echo $((6*7))_RAN\n"
	da.settle(t)
	text := func() string { da.tr.Frame(); return da.term.Text() }
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(text(), "echo $((6*7))_RAN"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("nothing pasted: %q", da.term.Text())
		}
	}
	// Pasted and left there: the shell runs nothing until Enter.
	time.Sleep(400 * time.Millisecond)
	if strings.Contains(text(), "42_RAN") {
		t.Fatalf("the paste ran: %q", da.term.Text())
	}
	da.tr.Key(0, ui.KeyEnter)
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(text(), "42_RAN"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("Enter didn't run the pasted command: %q", da.term.Text())
		}
	}
}

// Enter, while it records, stops it and sends the text once it is written
// down: pasted, then Enter, which runs it in a shell. Enter is the
// dictation's, not the pane's, until then.
func TestDictationEnterSends(t *testing.T) {
	da := newDictationApp(t)
	da.ready()
	da.dictate()
	da.tr.Key(0, ui.KeyEnter)
	da.tr.Frame()
	if d := da.a.dictation; d == nil || d.step != dictTranscribing || !d.send {
		t.Fatalf("Enter didn't end the recording to send it: %+v", d)
	}
	<-da.heard
	da.release <- "echo $((6*7))_SENT"
	da.settle(t)
	da.waitText(t, "42_SENT")
}

// Held, the key is push-to-talk: its repeats don't stop the recording,
// and letting go stops it, the text pasted to be read first, without
// Enter, and with a space, which the next dictation goes on from.
func TestDictationHoldToTalk(t *testing.T) {
	da := newDictationApp(t)
	da.ready()
	for range 3 { // the press, and the key repeating
		da.tr.Key(ui.Cmd|ui.Shift, ui.KeySpace)
		da.tr.Frame()
	}
	d := da.a.dictation
	if d == nil || d.step != dictRecording || !d.held {
		t.Fatalf("holding the key doesn't keep it recording: %+v", d)
	}
	d.start = d.start.Add(-time.Second)
	da.tr.HoldModifiers(0)
	da.tr.Frame()
	if d.step != dictTranscribing || d.send {
		t.Fatalf("letting go didn't stop it to paste: %+v", d)
	}
	<-da.heard
	da.release <- "echo one"
	da.settle(t)
	da.waitText(t, "echo one")
	da.dictate()
	da.dictate()
	<-da.heard
	da.release <- "two"
	da.settle(t)
	da.waitText(t, "echo one two")
	// Run, it would print "one two" on a line of its own.
	time.Sleep(300 * time.Millisecond)
	da.tr.Frame()
	if n := strings.Count(da.term.Text(), "one two"); n != 1 {
		t.Fatalf("a dictation ran without Enter: %q", da.term.Text())
	}
}

// Esc drops a recording, and the text being written down; the mic in the
// header starts one too.
func TestDictationCancel(t *testing.T) {
	da := newDictationApp(t)
	da.ready()
	da.dictate()
	da.tr.Key(0, ui.KeyEscape)
	da.tr.Frame()
	if da.a.dictation != nil {
		t.Fatal("Esc didn't drop the recording")
	}
	for deadline := time.Now().Add(5 * time.Second); !da.recs[0].canceled.Load(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the recorder wasn't stopped")
		}
	}
	if da.recs[0].stopped.Load() || len(da.heard) != 0 {
		t.Fatal("a dropped recording was written down")
	}

	da.hover()
	if err := da.tr.Click("Dictate"); err != nil {
		t.Fatal(err)
	}
	da.tr.Frame()
	if da.a.dictation == nil || len(da.recs) != 2 {
		t.Fatal("the mic didn't start recording")
	}
	// The chip with the timer stops it.
	if err := da.tr.Click("Stop Dictating"); err != nil {
		t.Fatal(err)
	}
	<-da.heard
	da.tr.Frame()
	da.tr.Key(0, ui.KeyEscape)
	da.tr.Frame()
	da.release <- "never pasted"
	da.settle(t)
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(da.term.Text(), "never pasted") || da.tr.HasText("Not written down") {
		t.Fatalf("a dropped text was pasted, or said to fail: %q", da.tr.Texts())
	}
}

// Turning voice input off drops a recording.
func TestDictationVoiceOff(t *testing.T) {
	da := newDictationApp(t)
	da.ready()
	da.dictate()
	da.a.setVoice(false)
	da.tr.Frame()
	if da.a.dictation != nil {
		t.Fatal("still recording with voice input off")
	}
	for deadline := time.Now().Add(5 * time.Second); !da.recs[0].canceled.Load(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the recorder wasn't stopped")
		}
	}
}

// With no terminal pane focused, it says so rather than recording.
func TestDictationNeedsATerminal(t *testing.T) {
	da := newDictationApp(t)
	da.ready()
	da.a.openEditor("hello.txt")
	da.tr.Frame()
	if da.a.activeView != tabEditor {
		t.Fatal("no editor shown")
	}
	da.dictate()
	if da.a.dictation != nil || len(da.recs) != 0 || !da.tr.HasText("Click into a terminal pane") {
		t.Fatalf("recorded with no terminal pane: %q", da.tr.Texts())
	}
}

func TestVoiceLanguageNames(t *testing.T) {
	if languageTag("id") != "ID" || languageTag("id,en") != "ID+EN" || languageTag("auto") != "Auto" || languageTag("xx") != languageTag(voice.DefaultLanguage) {
		t.Fatal("languageTag")
	}
	// Auto is offered last, and the first five are the common ones.
	if voice.Languages[len(voice.Languages)-1].Code != "auto" || voice.Languages[0].Code != "id" || voice.Languages[4].Code != "ms" {
		t.Fatalf("Languages order: %v", voice.Languages[:5])
	}
}
