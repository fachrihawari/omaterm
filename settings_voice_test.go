package main

import (
	"os"
	"path/filepath"
	"testing"

	"omaterm/internal/voice"
)

// Voice is off by default, its page the switch alone; on, it shows the
// engine, the models and the language, and Remove Downloads once there
// are any.
func TestSettingsVoicePage(t *testing.T) {
	if onWindows {
		t.Skip("no web access on Windows")
	}
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // no daemon to tell
	a, tr := newSettingsTester(t, "voice")
	defer stopTermPanes(a)
	if a.settings.Voice.Enabled {
		t.Fatal("voice is on by default")
	}
	if !tr.HasText("Voice input") || tr.HasText("whisper-server") || tr.HasText("Large v3 Turbo") || tr.HasText("Remove Downloads…") {
		t.Fatal("the page shows more than the switch while voice is off")
	}
	a.setVoice(true)
	tr.Frame()
	for _, want := range []string{"whisper-server", "Tiny", "Small", "Large v3 Turbo", "Language"} {
		if !tr.HasText(want) {
			t.Errorf("on, the page has no %q", want)
		}
	}
	dir := voiceDir()
	if filepath.Dir(filepath.Dir(dir)) != state {
		t.Fatalf("voice files in %s, not the state folder", dir)
	}
	os.MkdirAll(dir, 0o700)
	m, _ := voice.ModelByName("tiny")
	os.WriteFile(voice.ModelPath(dir, m), []byte("model"), 0o600)
	a.refreshVoice()
	tr.Frame()
	if !tr.HasText("Use") || !tr.HasText("Remove Downloads…") {
		t.Fatal("a downloaded model has no Use, or the page no Remove Downloads")
	}
	if err := tr.Click("Use"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if a.settings.Voice.Model != "tiny" || !tr.HasText("In use") {
		t.Fatalf("model %q after Use", a.settings.Voice.Model)
	}
	a.setVoice(false)
	tr.Frame()
	if tr.HasText("Tiny") || !tr.HasText("Remove Downloads…") {
		t.Fatal("off, the page shows the models, or can't remove the downloads")
	}
	if _, err := os.Stat(voice.ModelPath(dir, m)); err != nil {
		t.Fatal("turning voice off removed the model")
	}
}
