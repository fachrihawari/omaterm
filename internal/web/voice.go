package web

// VoiceScript is assets/voice.js, the phone's recorder, which the
// desktop's recorder page on macOS runs too: one WAV encoder for both.
func VoiceScript() []byte {
	b, _ := assets.ReadFile("assets/voice.js")
	return b
}
