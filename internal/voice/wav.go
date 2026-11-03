package voice

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// SampleRate is the WAV whisper takes, and the phone records: 16 kHz mono
// 16-bit PCM.
const SampleRate = 16000

// MaxLength is the longest recording heard: the phone stops there too.
const MaxLength = 2 * time.Minute

// ErrBadWAV is audio that isn't the WAV the phone sends.
var ErrBadWAV = errors.New("not 16 kHz mono 16-bit WAV audio")

// CheckWAV checks that b is a WAV of 16 kHz mono 16-bit PCM, no longer
// than MaxLength, and tells how long it is.
func CheckWAV(b []byte) (time.Duration, error) {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, ErrBadWAV
	}
	fmtSeen := false
	for at := 12; at+8 <= len(b); {
		id, size := string(b[at:at+4]), int(binary.LittleEndian.Uint32(b[at+4:at+8]))
		body := at + 8
		if size < 0 || size > len(b)-body {
			return 0, ErrBadWAV
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return 0, ErrBadWAV
			}
			f := b[body:]
			format, channels := binary.LittleEndian.Uint16(f[0:]), binary.LittleEndian.Uint16(f[2:])
			rate, bits := binary.LittleEndian.Uint32(f[4:]), binary.LittleEndian.Uint16(f[14:])
			if format != 1 || channels != 1 || rate != SampleRate || bits != 16 {
				return 0, fmt.Errorf("%w: format %d, %d channels, %d Hz, %d bits", ErrBadWAV, format, channels, rate, bits)
			}
			fmtSeen = true
		case "data":
			if !fmtSeen {
				return 0, ErrBadWAV
			}
			length := time.Duration(size/2) * time.Second / SampleRate
			if size == 0 {
				return 0, errors.New("the recording is empty")
			}
			if length > MaxLength {
				return length, fmt.Errorf("the recording is longer than %v", MaxLength)
			}
			return length, nil
		}
		// Chunks are padded to an even size.
		at = body + size + size%2
	}
	return 0, ErrBadWAV
}
