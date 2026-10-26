package proto

// Frames of an attach connection: a kind, the payload's length as a
// big-endian uint32, and the payload. The daemon sends a pane's screen as
// frames of terminal.Mirror (FrameOutput); the app sends what is typed,
// the size its view fits, and its colors. Adapted from Kero's
// (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE).

import (
	"encoding/binary"
	"errors"
	"io"
)

const (
	FrameOutput  byte = 'o' // daemon → app: VT that brings the app's terminal to the pane's
	FrameInput   byte = 'i' // app → daemon: what was typed
	FrameResize  byte = 'r' // app → daemon: cols, rows, cell width and height (uint16s)
	FrameResized byte = 'z' // daemon → app: the pane took cols, rows (uint16s)
	FrameClosed  byte = 'c' // daemon → app: the pane is gone
	FrameTheme   byte = 't' // app → daemon: the colors shown (19 RGBA)
)

const maxFrame = 16 << 20

// AppendFrame appends a frame.
func AppendFrame(dst []byte, kind byte, payload []byte) []byte {
	n := len(payload)
	dst = append(dst, kind, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	return append(dst, payload...)
}

// ReadFrame reads a frame.
func ReadFrame(r io.Reader) (kind byte, payload []byte, err error) {
	var hdr [5]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrame {
		return 0, nil, errors.New("proto: frame too large")
	}
	// From a message in hand, as a web page's: no more than it holds. A
	// 5-byte message claiming 16 MB had that much made each time.
	if m, ok := r.(interface{ Len() int }); ok && uint64(m.Len()) < uint64(n) {
		return 0, nil, io.ErrUnexpectedEOF
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}

// Uint16s encodes small numbers for resize frames.
func Uint16s(v ...int) []byte {
	b := make([]byte, 2*len(v))
	for i, x := range v {
		binary.BigEndian.PutUint16(b[2*i:], uint16(max(0, min(x, 0xFFFF))))
	}
	return b
}

// ParseUint16s decodes them.
func ParseUint16s(b []byte, n int) ([]int, bool) {
	if len(b) != 2*n {
		return nil, false
	}
	out := make([]int, n)
	for i := range out {
		out[i] = int(binary.BigEndian.Uint16(b[2*i:]))
	}
	return out, true
}
