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
