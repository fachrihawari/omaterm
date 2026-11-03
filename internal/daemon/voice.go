package daemon

import (
	"context"
	"log"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/paths"
	"omaterm/internal/voice"
)

// Transcriber writes down a recording with whisper-server bin running
// model; voice.Server is the one that runs it.
type Transcriber interface {
	Transcribe(ctx context.Context, bin, model string, wav []byte, lang string) (string, error)
	Running() bool
	Stop()
}

// voiceSettings are settings.json's voice settings.
func (d *Daemon) voiceSettings() voice.Settings {
	s := struct {
		Voice voice.Settings `json:"voice"`
	}{}
	d.readSettings(&s)
	return s.Voice
}

// voiceStatus tells whether voice input is on and can work, starting
// nothing.
func (d *Daemon) voiceStatus() voice.Status {
	st := voice.Check(voice.Dir(d.opts.StateDir), d.voiceSettings())
	if st.Enabled {
		st.Running = d.voice.Running()
	}
	return st
}

// reloadVoice follows the settings: off, or with its downloads removed,
// whisper-server stops. A changed model takes effect with the next
// recording, which starts it again.
func (d *Daemon) reloadVoice() voice.Status {
	st := d.voiceStatus()
	if (!st.Enabled || !st.Ready) && d.voice.Running() {
		d.voice.Stop()
		log.Printf("voice: whisper-server stopped: voice is off or not ready")
	}
	return d.voiceStatus()
}

// Transcribe writes down a recording from web access, in lang ("" for the
// settings' language). Off, it starts nothing.
func (d *Daemon) Transcribe(ctx context.Context, wav []byte, lang string) (string, error) {
	st := d.voiceStatus()
	if !st.Enabled {
		return "", voice.ErrOff
	}
	if !st.Ready {
		return "", &voice.NotReadyError{Reason: st.Reason}
	}
	if lang == "" {
		lang = st.Language
	}
	m, _ := voice.ModelByName(st.Model)
	return d.voice.Transcribe(ctx, st.Engine, voice.ModelPath(voice.Dir(d.opts.StateDir), m), wav, lang)
}

// ReloadVoice tells the session daemon the voice settings changed, so it
// stops whisper-server when voice is off. Without a daemon there is
// nothing running to stop.
func ReloadVoice() (voice.Status, error) {
	return callVoice("voice.reload")
}

// StatusVoice asks the session daemon whether whisper-server runs.
func StatusVoice() (voice.Status, error) {
	return callVoice("voice.status")
}

func callVoice(method string) (voice.Status, error) {
	c, err := client.Dial(paths.DaemonSocketPath())
	if err != nil {
		return voice.Status{}, err
	}
	defer c.Close()
	var st voice.Status
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = c.CallContext(ctx, method, nil, &st)
	return st, err
}
