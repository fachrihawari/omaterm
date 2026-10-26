// Ported from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/daemon/osc.go.

// Package osc reads the escape sequences programs use to tell a terminal
// what they are doing: OSC 7501 (the Program Status Protocol), OSC 133
// (shell prompts and command ends) and OSC 9;4 (progress). The terminal
// emulator ignores these; the app reads them from the output on its way to
// the emulator.
package osc

import "bytes"

// Scanner finds OSC 7501, 133 and 9;4 in a program's output, and full
// resets (RIS), across reads. Its zero value is ready.
type Scanner struct {
	state int // 0 text, 1 after ESC, 2 in an OSC we keep, 3 in one we skip, 4 ESC in an OSC
	buf   []byte
	// prev is the byte before, across reads: after a byte of 0x80 or
	// more, 0x9d and 0x9c continue a UTF-8 character ("Н" is D0 9D),
	// and are no C1 controls.
	prev byte
}

// MaxLen is the longest sequence kept, OSC 7501's limit.
const MaxLen = 4096

// Scan calls fn with the body of each OSC 7501, 133 and 9;4 in p (what is
// between "ESC ]" and the terminator), and reset at each RIS (ESC c).
func (o *Scanner) Scan(p []byte, fn func(body []byte), reset func()) {
	for _, b := range p {
		c1 := o.prev < 0x80 // a byte 0x80-0x9f here is a C1 control
		o.prev = b
		switch o.state {
		case 0:
			if b == 0x1b {
				o.state = 1
			} else if b == 0x9d && c1 { // C1 OSC, in 8-bit output
				o.begin()
			}
		case 1:
			if b == ']' {
				o.begin()
			} else if b != 0x1b {
				o.state = 0
				if b == 'c' && reset != nil {
					reset()
				}
			}
		case 2, 3:
			switch {
			case b == 0x07, b == 0x9c && c1:
				o.end(fn)
			case b == 0x1b:
				o.state = 4
			default:
				if o.state == 2 {
					o.buf = append(o.buf, b)
					if !o.wanted() || len(o.buf) > MaxLen {
						o.state, o.buf = 3, o.buf[:0]
					}
				}
			}
		case 4:
			if b == '\\' {
				o.end(fn)
			} else {
				// An ESC ends the string whatever follows (a new sequence).
				o.state, o.buf = 1, o.buf[:0]
				if b == ']' {
					o.begin()
				} else if b != 0x1b {
					o.state = 0
				}
			}
		}
	}
}

func (o *Scanner) begin() { o.state, o.buf = 2, o.buf[:0] }

func (o *Scanner) end(fn func([]byte)) {
	if o.state == 2 || o.state == 4 && len(o.buf) > 0 {
		if o.wanted() && len(o.buf) > 0 {
			fn(o.buf)
		}
	}
	o.state, o.buf = 0, o.buf[:0]
}

var prefixes = [][]byte{[]byte("7501;"), []byte("133;"), []byte("9;4;")}

// wanted reports whether what was collected may still be an OSC kept.
func (o *Scanner) wanted() bool {
	for _, pre := range prefixes {
		n := min(len(o.buf), len(pre))
		if bytes.Equal(o.buf[:n], pre[:n]) {
			return true
		}
	}
	return false
}
