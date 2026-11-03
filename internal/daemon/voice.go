package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"omaterm/internal/client"
	"omaterm/internal/paths"
	"omaterm/internal/proto"
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

// VoiceParams are a recording the app made, to write down with
// voice.transcribe: a WAV as voice.CheckWAV takes, in Lang ("" for the
// settings' language). Base64 in the JSON, 4 MB of it is well under
// proto.MaxLine.
type VoiceParams struct {
	WAV  []byte `json:"wav"`
	Lang string `json:"lang,omitempty"`
}

// VoiceText is what voice.transcribe wrote down.
type VoiceText struct {
	Text string `json:"text"`
}

// transcribeCall is voice.transcribe: the app's recording, written down
// as the phone's are.
func (d *Daemon) transcribeCall(ctx context.Context, raw json.RawMessage) (any, error) {
	var p VoiceParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, proto.Errorf(proto.CodeUsage, "bad recording")
	}
	if p.Lang != "" && !voice.KnownLanguage(p.Lang) {
		return nil, proto.Errorf(proto.CodeUsage, "unknown language "+p.Lang)
	}
	if _, err := voice.CheckWAV(p.WAV); err != nil {
		return nil, proto.Errorf(proto.CodeUsage, err.Error())
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	text, err := d.Transcribe(ctx, p.WAV, p.Lang)
	var notReady *voice.NotReadyError
	switch {
	case errors.Is(err, voice.ErrOff):
		return nil, proto.Errorf(proto.CodeUsage, "Voice input is off: turn it on in Settings › Voice")
	case errors.As(err, &notReady):
		return nil, proto.Errorf(proto.CodeUsage, notReady.Reason)
	case err != nil:
		log.Printf("voice: %v", err)
		return nil, err
	}
	return VoiceText{Text: text}, nil
}

// TranscribeVoice has the session daemon write down a recording of the
// app's, in lang ("" for the settings' language).
func TranscribeVoice(ctx context.Context, wav []byte, lang string) (string, error) {
	c, err := client.Dial(paths.DaemonSocketPath())
	if err != nil {
		return "", err
	}
	defer c.Close()
	var out VoiceText
	err = c.CallContext(ctx, "voice.transcribe", VoiceParams{WAV: wav, Lang: lang}, &out)
	return out.Text, err
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
