package main

// Voice input on the desktop: voice.dictate (or the mic in a terminal
// pane's header) starts recording, and the session daemon's whisper-server
// writes the recording down into the pane, as the user's paste. How it
// ends says what comes next: the key again (or letting go of it, held)
// pastes it to be read first; Enter pastes it and presses Enter, sending
// it; Esc drops it.
// It records on Linux with a recorder program (voice.Record) and on
// macOS through a web page (dictation_darwin.go); on Windows there is
// none. Off in Settings › Voice, as by default, nothing records.

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/daemon"
	"omaterm/internal/session"
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

// holdToTalk is how long the dictate key is held for letting go of it to
// stop the recording: a tap toggles, a hold is push-to-talk.
const holdToTalk = 400 * time.Millisecond

// sendGap is between the pasted text and the Enter that sends it: a
// program that reads both at once takes the Enter as a newline in the
// text. As the session's own pasteGap.
const sendGap = 150 * time.Millisecond

// dictation is the one recording or being written down, for its pane.
type dictation struct {
	pane  string
	step  dictationStep
	start time.Time
	rec   recording
	// held is while the dictate key's modifiers stay down after it
	// started: the key repeating is not a second press, and letting go
	// late stops it.
	held bool
	// send presses Enter after the text: Enter ended it.
	send bool
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
		switch {
		case d.held:
			// The key repeating while held: push-to-talk ends as it is
			// let go.
		case d.step == dictRecording:
			a.stopDictation()
		default:
			a.toast(c, "Still writing the last one down (Esc drops it)")
		}
		return
	}
	a.startDictation(c, a.dictationPane())
}

// dictateMods are the modifiers of the dictate key, held for push-to-talk.
func dictateMods() ui.Modifiers {
	if b := bindingsFor("voice.dictate"); len(b) > 0 {
		return b[0].Combo.Mods
	}
	return ui.Cmd | ui.Shift
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
	// Started by the key, whose modifiers are still down: push-to-talk
	// if they stay down a while.
	d.held = c != nil && c.Modifiers()&dictateMods() == dictateMods()
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
	if !voice.ValidLanguage(lang) {
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
			a.pasteDictation(d.pane, text, d.send)
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
// Enter. With send, Enter follows it; without, a space, which the next
// dictation goes on from.
func (a *appState) pasteDictation(pane, text string, send bool) {
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
	if !send {
		p.Term().Paste(text + " ")
		return
	}
	p.Term().Paste(text)
	if info := p.Info(); info.Status == session.Waiting && info.Kind == "permission" {
		// Enter would answer what it asks, not send the text.
		a.toast(nil, "It’s asking for permission: pasted, not sent")
		return
	}
	time.AfterFunc(sendGap, func() { p.Key("enter") })
}

// dictationKeys are the keys of a dictation going on: Esc drops it, and
// Enter, in its pane, ends it and sends the text once it is written down.
// Letting go of the dictate key's modifiers after holding them a while
// ends a push-to-talk, the text pasted to be read first.
func (a *appState) dictationKeys(c *ui.Context) {
	d := a.dictation
	if d == nil {
		return
	}
	c.OnShortcut(0, ui.KeyEscape, a.cancelDictation)
	if a.overlay == overlayNone && !a.modalOpen() && a.dictationPane() == d.pane {
		c.OnShortcut(0, ui.KeyEnter, func() {
			d.send = true
			if d.step == dictRecording {
				a.stopDictation()
			}
		})
	}
	if d.held && c.Modifiers()&dictateMods() != dictateMods() {
		d.held = false
		if d.step == dictRecording && time.Since(d.start) >= holdToTalk {
			a.stopDictation()
		}
	}
}

// dictationTip says how a dictation ends, for its chips.
func dictationTip() string {
	return "Enter sends · " + keyPrimary("voice.dictate") + " pastes it to read first · Esc drops it"
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
		bg := pal.Bad.Alpha(0.1)
		if chip.Hovered() {
			bg = pal.Bad.Alpha(0.18)
		}
		chip.Background(bg).Cursor(ui.CursorPointer).Label("Stop Dictating").Tooltip(dictationTip())
		if chip.Clicked() {
			a.stopDictation()
		}
	} else {
		chip.Background(pal.Chip).Tooltip("Writing it down… Esc drops it")
		if d.send {
			chip.Tooltip("Writing it down, then sending it… Esc drops it")
		}
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
			ui.Text(c, languageTag(a.voiceLanguage())).FontSize(10.5).FontWeight(600).TextColor(pal.TextMuted).SingleLine()
		} else {
			ui.Spinner(c).FontSize(11).Label("Writing it down")
			ui.Text(c, "Writing it down…").FontSize(11.5).TextColor(pal.TextMuted).SingleLine()
		}
	})
}

// dictationStatus is a dictation going on, in the status bar, where it is
// seen whatever is shown: its pane's header may be out of sight. Clicked,
// it stops the recording, or shows the pane it is written into.
func (a *appState) dictationStatus(c *ui.Context) {
	d := a.dictation
	if d == nil {
		return
	}
	recording := d.step == dictRecording
	seg := a.statusSegment(c, "dictation", func() {
		if recording {
			dot := ui.Box(c.Key("dot")).Size(7, 7).Radius(3.5).Background(pal.Bad)
			a.pulse(dot)
			s := int(time.Since(d.start).Seconds())
			a.swap(ui.Text(c.Key("time"), "Dictating "+strconv.Itoa(s/60)+":"+twoDigits(s%60)).FontSize(10).
				TextColor(pal.Bad).SingleLine().Shrink(0), true)
			ui.Text(c, languageTag(a.voiceLanguage())).FontSize(10).FontWeight(600).TextColor(pal.TextMuted).SingleLine()
		} else {
			ui.Spinner(c).FontSize(10).Label("Writing it down")
			ui.Text(c, "Writing it down…").FontSize(10).TextColor(pal.TextMuted).SingleLine().Shrink(0)
		}
		if v, ok := d.rec.(interface{ view() ui.NativeWebView }); ok && recording && v.view() != nil {
			// The page that records (macOS) is in the window, at a
			// pixel, transparent: WebKit lets pages it shows record. The
			// status bar is always there, as a pane's header is not.
			ui.WebView(c, v.view()).Size(1, 1).Absolute().Top(0).Left(0)
		}
	})
	seg.Tooltip(dictationTip())
	if seg.Clicked() {
		if recording {
			a.stopDictation()
		} else {
			a.revealPane(d.pane)
		}
	}
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
		Tooltip(withKeys("Dictate in "+voice.LanguageName(a.voiceLanguage()), "voice.dictate") + " · right-click for the language")
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
// phone: one language or several, as Indonesian and English, for whisper
// to tell which; the languages most asked for, the rest in a submenu,
// then whisper guessing among all.
func (a *appState) voiceLanguageMenu(m *ui.Menu) {
	cur := voice.ParseLanguages(a.voiceLanguage())
	set := func(codes []string) {
		a.settings.Voice.Language = strings.Join(codes, ",")
		a.voiceChanged()
	}
	item := func(m *ui.Menu, l voice.Language) {
		on := slices.Contains(cur, l.Code)
		if m.Item(l.Name).Checked(on).Chosen() {
			switch {
			case !on:
				set(append(slices.Clone(cur), l.Code))
			case len(cur) > 1:
				// The last one stays: it listens for something.
				set(slices.DeleteFunc(slices.Clone(cur), func(c string) bool { return c == l.Code }))
			}
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
	if m.Item("Detect Each Time").Checked(cur == nil).Chosen() {
		a.settings.Voice.Language = "auto"
		a.voiceChanged()
	}
}

// voiceLanguage is what voice input listens for: the setting, or the
// default for an empty or unknown one, as voice.Check reads it.
func (a *appState) voiceLanguage() string {
	if l := a.settings.Voice.Language; voice.ValidLanguage(l) {
		return l
	}
	return voice.DefaultLanguage
}

// languageTag is a language setting as the chip shows it: ID, ID+EN, or
// Auto.
func languageTag(s string) string {
	if !voice.ValidLanguage(s) {
		s = voice.DefaultLanguage
	}
	if s == "auto" {
		return "Auto"
	}
	return strings.ToUpper(strings.Join(voice.ParseLanguages(s), "+"))
}
