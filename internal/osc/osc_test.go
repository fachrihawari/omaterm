package osc

import (
	"encoding/base64"
	"strings"
	"testing"
)

func scanAll(t *testing.T, in string, byteAtATime bool) (bodies []string, resets int) {
	t.Helper()
	var s Scanner
	fn := func(b []byte) { bodies = append(bodies, string(b)) }
	reset := func() { resets++ }
	if byteAtATime {
		for i := 0; i < len(in); i++ {
			s.Scan([]byte{in[i]}, fn, reset)
		}
	} else {
		s.Scan([]byte(in), fn, reset)
	}
	return
}

func TestScanner(t *testing.T) {
	in := "hi\x1b]7501;state=working\x07 x \x1b]133;A\x1b\\\x1b]0;title\x07\x1b]9;4;1;40\x1b\\\x1bc\x9d133;D;0\x9c"
	for _, one := range []bool{false, true} {
		bodies, resets := scanAll(t, in, one)
		want := []string{"7501;state=working", "133;A", "9;4;1;40", "133;D;0"}
		if strings.Join(bodies, "|") != strings.Join(want, "|") {
			t.Errorf("byte-at-a-time=%v: %q", one, bodies)
		}
		if resets != 1 {
			t.Errorf("resets %d", resets)
		}
	}
}

func TestScannerOverflowAndNested(t *testing.T) {
	long := "\x1b]7501;state=working:msg=" + strings.Repeat("A", MaxLen) + "\x07"
	if b, _ := scanAll(t, long, false); len(b) != 0 {
		t.Error("overlong sequence kept")
	}
	// An ESC inside an OSC starts a new sequence.
	if b, _ := scanAll(t, "\x1b]7501;state=wor\x1b]133;A\x07", false); len(b) != 1 || b[0] != "133;A" {
		t.Errorf("nested: %q", b)
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestParseStatus(t *testing.T) {
	cases := []struct {
		in    string
		ok    bool
		state string
		msg   string
	}{
		{"state=working", true, "working", ""},
		{"state=blocked:kind=permission:msg=" + b64("Approve?"), true, "blocked", "Approve?"},
		{"state=bogus", false, "", ""},
		{"state=done:id=a/b/c", true, "done", ""},
		{"state=done:id=" + strings.Repeat("a/", 9) + "a", false, "", ""},
		{"state=done:msg=" + b64("bad\x01ctl"), false, "", ""},
		{"state=done:msg=" + b64("rtl‮evil"), true, "done", "rtlevil"},
		{"state=working:junk:progress=40", true, "working", ""},
	}
	for _, c := range cases {
		r, ok := ParseStatus(c.in)
		if ok != c.ok {
			t.Errorf("%q: ok %v", c.in, ok)
			continue
		}
		if ok && (r.Record.State != c.state || r.Record.Msg != c.msg) {
			t.Errorf("%q: %+v", c.in, r.Record)
		}
	}
	if r, _ := ParseStatus("state=working:progress=40"); r.Record.Progress != 40 {
		t.Error("progress")
	}
	if r, ok := ParseStatus("?"); !ok || !r.Query {
		t.Error("query")
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	in := Record{State: "blocked", Kind: "question", Progress: 10, App: "claude", Msg: "Which one?", ID: "build/test"}
	s := Encode(in)
	bodies, _ := scanAll(t, s, false)
	if len(bodies) != 1 {
		t.Fatalf("%q", s)
	}
	r, ok := ParseStatus(strings.TrimPrefix(bodies[0], "7501;"))
	if !ok || r.Record.State != "blocked" || r.Record.Kind != "question" || r.Record.Msg != "Which one?" || r.Record.ID != "build/test" || r.Record.Progress != 10 {
		t.Fatalf("%+v", r.Record)
	}
}

func TestRecords(t *testing.T) {
	var rs Records
	rs.Apply(Record{ID: "build", State: "working", App: "make", Updated: 1})
	rs.Apply(Record{ID: "build/test", State: "error", Updated: 2})
	if l, _ := rs.Lead(); l.State != "working" {
		t.Errorf("lead %s", l.State)
	}
	rs.Apply(Record{ID: "deploy", State: "blocked", Updated: 3})
	if l, _ := rs.Lead(); l.State != "blocked" {
		t.Errorf("lead %s", l.State)
	}
	if list := rs.List(); list[1].ID != "build/test" || list[1].App != "make" {
		t.Errorf("app inherit: %+v", list)
	}
	rs.Apply(Record{ID: "build", State: "clear"})
	if rs.Len() != 1 {
		t.Errorf("clear by prefix left %d", rs.Len())
	}
	if !rs.Declared() || !rs.DropLive() || rs.Declared() {
		t.Error("declared/droplive")
	}
	for i := 0; i < MaxRecords+5; i++ {
		rs.Apply(Record{ID: "r" + string(rune('A'+i%26)) + string(rune('a'+i/26)), State: "done", Updated: int64(i)})
	}
	if rs.Len() > MaxRecords {
		t.Errorf("kept %d", rs.Len())
	}
	if !rs.DismissFinished() || rs.Len() != 0 {
		t.Error("dismiss")
	}
}

// A UTF-8 character with the byte of a C1 OSC in it ("Н" is D0 9D) starts
// no sequence: the scanner then missed a reset, and swallowed what came
// until a BEL.
func TestScannerUTF8(t *testing.T) {
	in := "Нет\x1bc\x1b]133;A\x07"
	for _, one := range []bool{false, true} {
		bodies, resets := scanAll(t, in, one)
		if resets != 1 || len(bodies) != 1 || bodies[0] != "133;A" {
			t.Errorf("byte-at-a-time=%v: %q, %d resets", one, bodies, resets)
		}
	}
}

func TestScannerBracketed(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"\x1b[?2004h", true},
		{"\x1b[?2004h\x1b[?2004l", false},
		{"\x1b[?1004;2004h", true},                // among other modes
		{"\x1b[?2004h\x1b[?1049;1004l", true},     // others reset, not it
		{"\x1b[2004h", false},                     // ANSI mode 2004, not DEC's
		{"\x1b[?2004h\x1bc", false},               // a full reset
		{"\x1b[?2004h\x1b[?2004$p", true},         // a query, no change
		{"\x1b[?20\x1b]0;t\x07\x1b[?2004h", true}, // a CSI cut short
		{"\x9b?2004h", true},                      // C1 CSI
		{"Н\x9b?2004h", false},                    // UTF-8, not C1
	}
	for _, c := range cases {
		for _, one := range []bool{false, true} {
			var s Scanner
			if one {
				for i := 0; i < len(c.in); i++ {
					s.Scan([]byte{c.in[i]}, func([]byte) {}, nil)
				}
			} else {
				s.Scan([]byte(c.in), func([]byte) {}, nil)
			}
			if s.Bracketed != c.want {
				t.Errorf("%q (byte at a time %v): bracketed %v", c.in, one, s.Bracketed)
			}
		}
	}
	// OSCs are still found around CSIs.
	if b, _ := scanAll(t, "\x1b[?2004h\x1b]133;A\x07\x1b[0m", false); len(b) != 1 || b[0] != "133;A" {
		t.Errorf("bodies %q", b)
	}
}

// A program's keyboard modes are followed from its output: the kitty
// protocol's pushed, popped and set flags, and modifyOtherKeys' level.
func TestScannerKeyboardModes(t *testing.T) {
	cases := []struct {
		in         string
		kitty, mok int
	}{
		{"\x1b[>1u", 1, 0},
		{"\x1b[>1u\x1b[>5u", 5, 0},         // pushed, the last current
		{"\x1b[>1u\x1b[<u", 0, 0},          // popped, one when none is given
		{"\x1b[>1u\x1b[>5u\x1b[<2u", 0, 0}, // popped two
		{"\x1b[>1u\x1b[>5u\x1b[<3u", 0, 0}, // popping more than was pushed
		{"\x1b[>1u\x1b[=3u", 3, 0},         // set
		{"\x1b[>1u\x1b[=4;2u", 5, 0},       // add
		{"\x1b[>7u\x1b[=1;3u", 6, 0},       // clear
		{"\x1b[=2u", 2, 0},                 // set with nothing pushed
		{"\x1b[>1u\x1b[?u", 1, 0},          // a query changes nothing
		{"\x1b[>1u\x1b[?1u", 1, 0},
		{"\x1b[1u", 0, 0},                       // restore cursor, not flags
		{"\x1b[>1u\x1bc", 0, 0},                 // a full reset
		{"\x1b[>4;2m", 0, 2},                    // modifyOtherKeys
		{"\x1b[>4;2m\x1b[>4m", 0, 0},            // reset
		{"\x1b[>4;2m\x1b[>4;0m", 0, 0},          // reset, with 0
		{"\x1b[>4;2m\x1bc", 0, 0},               // a full reset
		{"\x1b[>1;2m", 0, 0},                    // another key's option
		{"\x1b[0m\x1b[>4;1m", 0, 1},             // SGR, then the level
		{"\x1b[?2004h\x1b[>1u\x1b[>4;2m", 1, 2}, // with bracketed paste

		// Each screen has its own flags: the alternate screen's start empty
		// and are dropped as the program leaves it.
		{"\x1b[>1u\x1b[?1049h", 0, 0},                               // the alternate screen has none yet
		{"\x1b[>1u\x1b[?1049h\x1b[>9u", 9, 0},                       // its own push
		{"\x1b[>1u\x1b[?1049h\x1b[>9u\x1b[?1049l", 1, 0},            // leaving without a pop: the main's again
		{"\x1b[>1u\x1b[?1049h\x1b[<u\x1b[?1049l", 1, 0},             // a pop there pops nothing of the main's
		{"\x1b[>1u\x1b[?1047h\x1b[>9u\x1b[?1047l", 1, 0},            // 1047
		{"\x1b[>1u\x1b[?47h\x1b[>9u\x1b[?47l", 1, 0},                // 47
		{"\x1b[>1u\x1b[?1049h\x1b[>9u\x1b[?1049l\x1b[?1049h", 0, 0}, // entering again starts empty
		{"\x1b[>1u\x1b[?25;1049h\x1b[>9u\x1b[?25;1049l", 1, 0},      // among other modes
		{"\x1b[?1049h\x1b[>9u\x1bc", 0, 0},                          // a full reset leaves the alternate screen
		{"\x1b[>1u\x1b[?1049h\x1b[?1049h\x1b[>9u", 9, 0},            // set twice: still the one screen
	}
	for _, c := range cases {
		for _, one := range []bool{false, true} {
			var s Scanner
			if one {
				for i := 0; i < len(c.in); i++ {
					s.Scan([]byte{c.in[i]}, func([]byte) {}, nil)
				}
			} else {
				s.Scan([]byte(c.in), func([]byte) {}, nil)
			}
			if s.Kitty != c.kitty || s.ModifyOtherKeys != c.mok {
				t.Errorf("%q (byte at a time %v): kitty %d, modifyOtherKeys %d, want %d, %d",
					c.in, one, s.Kitty, s.ModifyOtherKeys, c.kitty, c.mok)
			}
		}
	}
}

// Of reports of a state, one that words it leads: an agent's own, over
// its program's bare one as its turn ends.
func TestLeadWorded(t *testing.T) {
	var rs Records
	rs.Apply(Record{ID: "agent", State: "done", Msg: "Merged w1", Progress: -1})
	rs.Apply(Record{State: "done", App: "claude-code", Progress: -1})
	if r, _ := rs.Lead(); r.Msg != "Merged w1" {
		t.Fatalf("lead %+v", r)
	}
	rs.Apply(Record{State: "working", App: "claude-code", Progress: -1})
	if r, _ := rs.Lead(); r.State != "working" {
		t.Fatalf("lead %+v", r)
	}
}

// A program pushing kitty keyboard flags without end keeps the stack
// bounded; the flags in force are its last push.
func TestKittyStackBounded(t *testing.T) {
	var o Scanner
	for i := 0; i < 1000; i++ {
		o.Scan([]byte("\x1b[>1u"), nil, nil)
	}
	o.Scan([]byte("\x1b[>3u"), nil, nil)
	if len(o.kitty[0]) > maxKittyStack || o.Kitty != 3 {
		t.Fatalf("stack %d, flags %d", len(o.kitty[0]), o.Kitty)
	}
}
