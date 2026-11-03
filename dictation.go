package main

// Voice input on the desktop: voice.dictate (or the mic in a terminal
// pane's header) starts recording, again stops, and the session daemon's
// whisper-server writes the recording down; the text is pasted into the
// pane, as the user's paste, never with Enter after it. Esc drops it.
// It records on Linux with a recorder program (voice.Record) and on
// macOS through a web page (dictation_darwin.go); on Windows there is
// none. Off in Settings › Voice, as by default, nothing records.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/daemon"
	"omaterm/internal/voice"
)

// recording is a recording in progress.
type recording interface {
	// Done is closed as the recording ends by itself, as when the
	// microphone is refused, which Stop then tells.
	Done() <-chan struct{}
	// Stop ends it and gives it as 16 kHz mono 16-bit WAV.
	Stop() ([]byte, error)
	// Cancel ends it, keeping nothing.
	Cancel()
}

// startRecording starts recording the microphone; nil where omaterm can't
// (dictation_other.go). Tests give a fake.
var startRecording = platformRecorder

// transcribeRecording writes a recording down, through the session
// daemon. Tests give a fake.
var transcribeRecording = daemon.TranscribeVoice

// voiceDaemon tells whether the session daemon runs, which writes the
// recordings down. Tests say it does.
var voiceDaemon = func(a *appState) bool {
	_, ok := a.panes.(*daemon.Remote)
	return ok
}

// dictationStep is where a dictation is.
type dictationStep int

const (
	dictRecording dictationStep = iota
	dictTranscribing
)

// dictation is the one recording or being written down, for its pane.
type dictation struct {
	pane  string
	step  dictationStep
	start time.Time
	rec   recording
	// cancel drops it while it is written down.
	cancel context.CancelFunc
	limit  *time.Timer
}

// voiceCheck is voice.Check's word, kept a little: the mic button asks
// it each frame, and Check looks at files and the PATH.
type voiceCheck struct {
	at       time.Time
	settings voice.Settings
	status   voice.Status
}

// desktopVoice tells whether voice input is on and can work here.
func (a *appState) desktopVoice() voice.Status {
	if startRecording == nil {
		return voice.Status{}
	}
	vc := &a.voiceCheck
	if vc.at.IsZero() || time.Since(vc.at) > 5*time.Second || vc.settings != a.settings.Voice {
		*vc = voiceCheck{at: time.Now(), settings: a.settings.Voice, status: voice.Check(voiceDir(), a.settings.Voice)}
	}
	return vc.status
}

// dictationPane is the terminal pane with the keyboard, which takes the
// text: "" for a browser pane, or with no terminal tab shown.
func (a *appState) dictationPane() string {
	if a.activeView != tabTerminal {
		return ""
	}
	id := a.activeSplitLeaf()
	if id == "" || a.isBrowser(id) || a.panes.Get(id) == nil {
		return ""
	}
	return id
}

// toggleDictation starts recording into the focused terminal pane, or
// stops the recording, which is then written down.
func (a *appState) toggleDictation(c *ui.Context) {
	if d := a.dictation; d != nil {
		if d.step == dictRecording {
			a.stopDictation()
		}
		return
	}
	a.startDictation(c, a.dictationPane())
}

// startDictation records for pane, once voice input is on and ready.
func (a *appState) startDictation(c *ui.Context, pane string) {
	if startRecording == nil {
		a.toast(c, "Voice input isn’t available on Windows yet")
		return
	}
	st := a.desktopVoice()
	switch {
	case !st.Enabled:
		a.toast(c, "Voice input is off: turn it on in Settings › Voice")
		return
	case !st.Ready:
		a.toast(c, "Voice input isn’t ready: "+voiceNotReady(st.Reason))
		return
	case pane == "":
		a.toast(c, "Click into a terminal pane to dictate into it")
		return
	}
	if !voiceDaemon(a) {
		a.toast(c, "Voice input needs the session daemon: turn it on in Settings › General")
		return
	}
	rec, err := startRecording(a)
	if err != nil {
		a.toast(c, "Can’t record: "+err.Error())
		return
	}
	d := &dictation{pane: pane, start: time.Now(), rec: rec}
	a.dictation = d
	// At the limit, the recording stops and is written down, as the
	// phone's does.
	d.limit = time.AfterFunc(voice.MaxLength, func() {
		a.post(func() {
			if a.dictation == d && d.step == dictRecording {
				a.stopDictation()
			}
		})
	})
	go func() {
		<-rec.Done()
		a.post(func() {
			if a.dictation == d && d.step == dictRecording {
				a.stopDictation() // which tells why it ended
			}
		})
	}()
	a.invalidateWindows()
}

// voiceNotReady words what voice.Check says is missing for the desktop:
// its reasons are written for the phone.
func voiceNotReady(reason string) string {
	return strings.TrimSuffix(reason, " on your computer")
}

// stopDictation ends the recording and has it written down; the text is
// pasted into its pane as it comes.
func (a *appState) stopDictation() {
	d := a.dictation
	d.limit.Stop()
	d.step = dictTranscribing
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	lang := a.settings.Voice.Language
	if !voice.KnownLanguage(lang) {
		lang = ""
	}
	go func() {
		defer cancel()
		wav, err := d.rec.Stop()
		text := ""
		if err == nil {
			text, err = transcribeRecording(ctx, wav, lang)
		}
		a.post(func() {
			if a.dictation != d {
				return // dropped
			}
			a.dictation = nil
			a.invalidateWindows()
			if err != nil {
				a.toast(nil, "Not written down: "+err.Error())
				return
			}
			a.pasteDictation(d.pane, text)
		})
	}()
	a.invalidateWindows()
}

// cancelDictation drops the recording, or the text being written down.
func (a *appState) cancelDictation() {
	d := a.dictation
	if d == nil {
		return
	}
	a.dictation = nil
	d.limit.Stop()
	if d.step == dictRecording {
		go d.rec.Cancel()
	} else {
		d.cancel()
	}
	a.invalidateWindows()
}

// pasteDictation pastes text into pane as the user's paste: bracketed
// when its program takes that, on one line, so nothing in it presses
// Enter.
func (a *appState) pasteDictation(pane, text string) {
	text = strings.Join(strings.Fields(strings.ReplaceAll(text, "[BLANK_AUDIO]", "")), " ")
	if text == "" {
		a.toast(nil, "Nothing was heard")
		return
	}
	p := a.panes.Get(pane)
	if p == nil || p.Term() == nil {
		copyText(text)
		a.toast(nil, "Its pane closed: the text is on the clipboard")
		return
	}
	p.Term().Paste(text)
}

// dictationKeys are the keys of a dictation going on: Esc drops it.
func (a *appState) dictationKeys(c *ui.Context) {
	if a.dictation != nil {
		c.OnShortcut(0, ui.KeyEscape, a.cancelDictation)
	}
}

// dictationChip shows, in the header of pane, its recording (a red dot
// and how long it has run) or that it is being written down. Clicked
// while recording, it stops.
func (a *appState) dictationChip(c *ui.Context, pane string) {
	d := a.dictation
	if d == nil || d.pane != pane {
		return
	}
	chip := ui.Row(c.Key("dictation")).Height(20).Radius(6).Padding(0, 7).Gap(6).AlignItems(ui.Center).Shrink(0)
	a.transition(chip, motionPop)
	recording := d.step == dictRecording
	if recording {
		chip.Background(pal.Bad.Alpha(0.1)).Cursor(ui.CursorPointer).
			Tooltip(withKeys("Stop and write it down", "voice.dictate") + " · Esc drops it")
		if chip.Clicked() {
			a.stopDictation()
		}
	} else {
		chip.Background(pal.Chip).Tooltip("Writing it down… Esc drops it")
	}
	chip.Children(func() {
		if recording {
			dot := ui.Box(c.Key("dot")).Size(8, 8).Radius(4).Background(pal.Bad)
			a.pulse(dot)
			s := int(time.Since(d.start).Seconds())
			a.swap(ui.Text(c.Key("time"), strconv.Itoa(s/60)+":"+twoDigits(s%60)).FontSize(11.5).FontWeight(500).
				TextColor(pal.Bad).SingleLine(), true)
			c.After(time.Second - time.Since(d.start)%time.Second)
			// What it listens for, as a wrong one is what garbles it.
			ui.Text(c, languageTag(a.settings.Voice.Language)).FontSize(10.5).FontWeight(600).TextColor(pal.TextMuted).SingleLine()
		} else {
			ui.Spinner(c).FontSize(11).Label("Writing it down")
			ui.Text(c, "Writing…").FontSize(11.5).TextColor(pal.TextMuted).SingleLine()
		}
		if v, ok := d.rec.(interface{ view() ui.NativeWebView }); ok && v.view() != nil {
			// The page that records (macOS) is in the window, at a
			// pixel, transparent: WebKit lets pages it shows record.
			ui.WebView(c, v.view()).Size(1, 1).Shrink(0)
		}
	})
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// micButton is the mic among a pane's controls, while voice input is on
// and ready: it starts dictating into the pane.
func (a *appState) micButton(c *ui.Context, pane string) {
	if a.dictation != nil && a.dictation.pane == pane {
		return // the chip stops it
	}
	if st := a.desktopVoice(); !st.Enabled || !st.Ready {
		return
	}
	b := a.cmdButton(c, iconMic, 13, false).Label("Dictate").
		Tooltip(withKeys("Dictate in "+languageName(a.settings.Voice.Language), "voice.dictate") + " · right-click for the language")
	// The language is a right-click away: dictating in another one than
	// it listens for comes out garbled, or in a third.
	b.ContextMenu(func(m *ui.Menu) { a.voiceLanguageMenu(m) })
	if b.Clicked() {
		if a.dictation != nil {
			return
		}
		a.focusPane(pane)
		a.startDictation(c, pane)
	}
}

// voiceLanguageMenu picks what voice input listens for, here and on the
// phone: the languages most asked for, the rest in a submenu, then
// whisper guessing.
func (a *appState) voiceLanguageMenu(m *ui.Menu) {
	cur := a.settings.Voice.Language
	item := func(m *ui.Menu, l voice.Language) {
		if m.Item(l.Name).Checked(l.Code == cur).Chosen() {
			a.settings.Voice.Language = l.Code
			a.voiceChanged()
		}
	}
	const common = 5 // Indonesian, English, Javanese, Sundanese, Malay
	for _, l := range voice.Languages[:common] {
		item(m, l)
	}
	m.Submenu("Other Languages", func(m *ui.Menu) {
		for _, l := range voice.Languages[common:] {
			if l.Code != "auto" {
				item(m, l)
			}
		}
	})
	m.Separator()
	for _, l := range voice.Languages {
		if l.Code == "auto" {
			item(m, l)
		}
	}
}

// languageName names a language code for a tooltip.
func languageName(code string) string {
	for _, l := range voice.Languages {
		if l.Code == code {
			if code == "auto" {
				return "any language"
			}
			return l.Name
		}
	}
	return languageName(voice.DefaultLanguage)
}

// languageTag is a language code as the chip shows it: ID, EN, or Auto.
func languageTag(code string) string {
	if !voice.KnownLanguage(code) {
		code = voice.DefaultLanguage
	}
	if code == "auto" {
		return "Auto"
	}
	return strings.ToUpper(code)
}
