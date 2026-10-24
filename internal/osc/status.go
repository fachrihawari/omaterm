// Adapted from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/daemon/osc.go.

package osc

import (
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Record is a program's status report (OSC 7501), as it last stood for
// its id.
type Record struct {
	ID       string `json:"id,omitempty"`
	State    string `json:"state"` // idle, working, done, blocked, error
	Kind     string `json:"kind,omitempty"`
	Progress int    `json:"progress"` // -1 when none
	App      string `json:"app,omitempty"`
	Title    string `json:"title,omitempty"`
	Msg      string `json:"msg,omitempty"`
	Updated  int64  `json:"updated"` // unix ms
}

// OSC 7501's grammar and limits.
var (
	keyRe     = regexp.MustCompile(`^[a-z]{1,16}$`)
	valueRe   = regexp.MustCompile(`^[A-Za-z0-9_.,+/=-]*$`)
	segmentRe = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,32}$`)
	appRe     = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,32}$`)
)

// Report is a parsed OSC 7501: a query, or a record (which may be a
// "clear").
type Report struct {
	Query  bool
	Record Record
}

// QueryReply answers a program's OSC 7501 query: the defaults, so no list.
const QueryReply = "\x1b]7501;?\x1b\\"

// ParseStatus parses the body of an OSC 7501 after "7501;", as the
// protocol says: a report that breaks a rule is discarded whole, a
// malformed pair skipped.
func ParseStatus(body string) (Report, bool) {
	body = strings.TrimSpace(body)
	if body == "?" || strings.HasPrefix(body, "?:") {
		return Report{Query: true}, true
	}
	pairs := map[string]string{}
	for _, pair := range strings.Split(body, ":") {
		k, v, ok := strings.Cut(pair, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || !keyRe.MatchString(k) || !valueRe.MatchString(v) {
			continue
		}
		pairs[k] = v // the last one wins
	}
	r := Record{State: pairs["state"], Progress: -1}
	switch r.State {
	case "idle", "working", "done", "blocked", "error", "clear":
	default:
		return Report{}, false
	}
	if id, ok := pairs["id"]; ok {
		segs := strings.Split(id, "/")
		if len(id) > 128 || len(segs) > 8 {
			return Report{}, false
		}
		for _, s := range segs {
			if !segmentRe.MatchString(s) {
				return Report{}, false
			}
		}
		r.ID = id
	}
	if r.State == "blocked" {
		switch k := pairs["kind"]; k {
		case "permission", "question", "auth":
			r.Kind = k
		}
	}
	if p, err := strconv.Atoi(pairs["progress"]); err == nil && p >= 0 && p <= 100 && (r.State == "working" || r.State == "blocked") {
		r.Progress = p
	}
	if app := pairs["app"]; appRe.MatchString(app) {
		r.App = app
	}
	text := func(key string, encoded, decoded int) (string, bool) {
		v, ok := pairs[key]
		if !ok {
			return "", true
		}
		if len(v) > encoded {
			return "", false
		}
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			b, err = base64.RawStdEncoding.DecodeString(v)
		}
		if err != nil || len(b) > decoded || !utf8.Valid(b) {
			return "", false
		}
		s := string(b)
		for _, c := range s {
			if c < 0x20 || c >= 0x7f && c <= 0x9f {
				return "", false
			}
		}
		return Neutralize(s), true
	}
	var ok bool
	if r.Msg, ok = text("msg", 2732, 2048); !ok {
		return Report{}, false
	}
	if r.Title, ok = text("title", 256, 192); !ok {
		return Report{}, false
	}
	r.Updated = time.Now().UnixMilli()
	return Report{Record: r}, true
}

// Neutralize drops the bidi overrides and invisible formatting that would
// make text shown outside the grid lie about itself.
func Neutralize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200E, r == 0x200F, r == 0x061C,
			r >= 0x200B && r <= 0x200D, r == 0xFEFF:
			return -1
		}
		return r
	}, s)
}

// Encode writes a report as OSC 7501, for `omaterm report`.
func Encode(r Record) string {
	var b strings.Builder
	b.WriteString("\x1b]7501;state=")
	b.WriteString(r.State)
	add := func(k, v string) {
		if v != "" {
			b.WriteString(":" + k + "=" + v)
		}
	}
	add("id", r.ID)
	add("kind", r.Kind)
	if r.Progress >= 0 {
		add("progress", strconv.Itoa(r.Progress))
	}
	add("app", r.App)
	if r.Title != "" {
		add("title", base64.StdEncoding.EncodeToString([]byte(r.Title)))
	}
	if r.Msg != "" {
		add("msg", base64.StdEncoding.EncodeToString([]byte(r.Msg)))
	}
	b.WriteString("\x1b\\")
	return b.String()
}
