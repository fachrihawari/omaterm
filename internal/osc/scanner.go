// Ported from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/daemon/osc.go.

// Package osc reads the escape sequences programs use to tell a terminal
// what they are doing: OSC 7501 (the Program Status Protocol), OSC 133
// (shell prompts and command ends) and OSC 9;4 (progress). The terminal
// emulator ignores these; the app reads them from the output on its way to
// the emulator, and with them whether the program takes bracketed pastes.
package osc

import (
	"bytes"
	"strconv"
)

// Scanner finds OSC 7501, 133 and 9;4 in a program's output, and full
// resets (RIS), across reads. Its zero value is ready.
type Scanner struct {
	state int // 0 text, 1 after ESC, 2 in an OSC we keep, 3 in one we skip, 4 ESC in an OSC, 5 in a CSI
	buf   []byte
	// prev is the byte before, across reads: after a byte of 0x80 or
	// more, 0x9d and 0x9c continue a UTF-8 character ("Н" is D0 9D),
	// and are no C1 controls.
	prev byte
	// Bracketed tells that the program asked for bracketed pastes (mode
	// 2004), until it asks no more or a full reset.
	Bracketed bool
	// Kitty is the kitty keyboard protocol's flags, which decide whether
	// keys with modifiers are sent as CSI u; 0 when off. The program
	// pushes and pops them (CSI > n u, CSI < n u) and may set them.
	Kitty int
	// ModifyOtherKeys is xterm's modifyOtherKeys level, 0 when off.
	ModifyOtherKeys int
	// kitty are the flags pushed, the last current, one stack for each
	// screen: the program's alternate screen has its own, dropped as it
	// leaves it, so a program that quits without popping leaves the main
	// screen's flags as they were.
	kitty [2][]int
	alt   bool   // the alternate screen is shown
	csi   []byte // a CSI's parameters and intermediates, so far
}

// maxKittyStack is how many kitty keyboard flags are kept pushed.
const maxKittyStack = 64

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
			} else if b == 0x9b && c1 { // C1 CSI
				o.state, o.csi = 5, o.csi[:0]
			}
		case 1:
			switch {
			case b == ']':
				o.begin()
			case b == '[':
				o.state, o.csi = 5, o.csi[:0]
			case b != 0x1b:
				o.state = 0
				if b == 'c' {
					o.Bracketed = false
					o.Kitty, o.kitty, o.alt, o.ModifyOtherKeys = 0, [2][]int{}, false, 0
					if reset != nil {
						reset()
					}
				}
			}
		case 5:
			switch {
			case b >= 0x40 && b <= 0x7e:
				o.state = 0
				o.mode(b)
			case b == 0x1b:
				o.state = 1
			case b < 0x20 || len(o.csi) >= 64:
				// A control in a CSI is acted on, and the CSI goes on; one
				// this long is none a program sends to set modes.
				if len(o.csi) >= 64 {
					o.state = 0
				}
			default:
				o.csi = append(o.csi, b)
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

// mode follows a CSI that ended with final: the modes that tell how the
// program wants pastes and keys.
func (o *Scanner) mode(final byte) {
	switch final {
	case 'h', 'l':
		o.decMode(final)
	case 'u':
		o.keyboard()
	case 'm':
		o.modifyOtherKeys()
	}
}

// decMode follows DECSET and DECRST ("CSI ? … h" and "l") of mode 2004,
// among the modes they set.
func (o *Scanner) decMode(final byte) {
	if len(o.csi) < 2 || o.csi[0] != '?' {
		return
	}
	for _, m := range bytes.Split(o.csi[1:], []byte(";")) {
		switch string(m) {
		case "2004":
			o.Bracketed = final == 'h'
		case "47", "1047", "1049":
			o.screen(final == 'h')
		}
	}
}

// screen follows the program showing its alternate screen (on) or the main
// one: each has its own kitty flags, and the alternate's start empty.
func (o *Scanner) screen(on bool) {
	if on == o.alt {
		return
	}
	o.alt = on
	o.kitty[1] = nil
	o.setKitty()
}

// stack is the kitty flags pushed on the screen shown.
func (o *Scanner) stack() *[]int {
	if o.alt {
		return &o.kitty[1]
	}
	return &o.kitty[0]
}

// setKitty makes Kitty the flags on top of the screen's stack.
func (o *Scanner) setKitty() {
	o.Kitty = 0
	if st := *o.stack(); len(st) > 0 {
		o.Kitty = st[len(st)-1]
	}
}

// keyboard follows the kitty keyboard protocol's flags: CSI > n u pushes
// n, CSI < n u pops n (one when none is given), and CSI = n ; m u sets
// them, or adds or clears bits with mode 2 or 3.
func (o *Scanner) keyboard() {
	if len(o.csi) == 0 {
		return
	}
	params := bytes.Split(o.csi[1:], []byte(";"))
	num := func(i int) int {
		if i >= len(params) {
			return 0
		}
		n, _ := strconv.Atoi(string(params[i]))
		return n
	}
	st := o.stack()
	switch o.csi[0] {
	case '>':
		// The stack is bounded, as kitty bounds it: a program pushing
		// without end drops its oldest entries, not the daemon's memory.
		if len(*st) >= maxKittyStack {
			*st = append((*st)[:0], (*st)[1:]...)
		}
		*st = append(*st, num(0))
	case '<':
		n := num(0)
		if n < 1 {
			n = 1
		}
		if n > len(*st) {
			n = len(*st)
		}
		*st = (*st)[:len(*st)-n]
	case '=':
		if len(*st) == 0 {
			*st = append(*st, 0)
		}
		flags, mode := num(0), num(1)
		cur := (*st)[len(*st)-1]
		switch mode {
		case 2: // add
			cur |= flags
		case 3: // clear
			cur &^= flags
		default: // set
			cur = flags
		}
		(*st)[len(*st)-1] = cur
	default:
		return // a query, CSI ? u: the program asks, it does not say
	}
	o.setKitty()
}

// modifyOtherKeys follows xterm's "CSI > 4 ; n m", which sets the level,
// and "CSI > 4 m", which turns it off. Other "m" sequences (SGR's) are
// not its business.
func (o *Scanner) modifyOtherKeys() {
	if len(o.csi) < 2 || o.csi[0] != '>' {
		return
	}
	params := bytes.Split(o.csi[1:], []byte(";"))
	if string(params[0]) != "4" {
		return
	}
	if len(params) == 1 {
		o.ModifyOtherKeys = 0
		return
	}
	n, err := strconv.Atoi(string(params[1]))
	if err != nil {
		return
	}
	o.ModifyOtherKeys = n
}

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
