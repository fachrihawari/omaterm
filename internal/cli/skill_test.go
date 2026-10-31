package cli

import (
	"strings"
	"testing"
)

// The skill tells agents where a process's Dev URL is, so they open it
// rather than a localhost port.
func TestSkillMentionsLink(t *testing.T) {
	if !strings.Contains(skillGuide, "omaterm ps --json") || !strings.Contains(skillGuide, "link") {
		t.Fatalf("the skill does not name ps --json's link:\n%s", skillGuide)
	}
}
