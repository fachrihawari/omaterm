package shellcmd

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	cases := map[string]string{"abc": "abc", "a b": "'a b'", "it's": `'it'\''s'`, "": "''", "$(x)": "'$(x)'"}
	for in, want := range cases {
		if got := Quote("bash", in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArgs(t *testing.T) {
	if got := Args("/bin/zsh", nil); len(got) != 1 || got[0] != "-zsh" {
		t.Errorf("login: %q", got)
	}
	got := Args("/bin/sh", []string{"echo", "a b"})
	if got[0] != "sh" || !strings.Contains(got[4], "echo 'a b'; printf") {
		t.Errorf("command: %q", got)
	}
	if !strings.HasPrefix(Args("/usr/bin/fish", []string{"ls"})[4], "status job-control full; ") {
		t.Error("fish job control")
	}
}

func TestLoginArgs(t *testing.T) {
	if got := LoginArgs("/bin/sh", "exit 3"); !slices.Equal(got, []string{"/bin/sh", "-l", "-c", "exit 3"}) {
		t.Errorf("sh: %q", got)
	}
	if got := LoginArgs("/usr/bin/fish", "exit 3"); !slices.Equal(got, []string{"/usr/bin/fish", "-l", "-c", "status job-control full; exit 3"}) {
		t.Errorf("fish: %q", got)
	}
}

// The command's exit status reaches the terminal as OSC 133;D.
func TestArgsReportsExit(t *testing.T) {
	args := Args("/bin/sh", []string{"sh", "-c", "exit 7"})
	// Replace the trailing exec of a login shell, which would wait for
	// input, with nothing.
	script := strings.Replace(args[4], "; exec /bin/sh -l", "", 1)
	out, _ := exec.Command("/bin/sh", "-c", script).Output()
	if !strings.Contains(string(out), "\x1b]133;D;7\a") {
		t.Errorf("output %q", out)
	}
}

func TestAccountShellParsers(t *testing.T) {
	passwd := "root:x:0:0:root:/root:/bin/bash\nme:x:1000:1000::/home/me:/usr/bin/fish\n"
	if got := passwdShell(passwd, "me"); got != "/usr/bin/fish" {
		t.Errorf("passwd: %q", got)
	}
	if got := passwdShell(passwd, "nobody"); got != "" {
		t.Errorf("passwd, no such user: %q", got)
	}
	if got := dsclShell("UserShell: /opt/homebrew/bin/fish\n"); got != "/opt/homebrew/bin/fish" {
		t.Errorf("dscl: %q", got)
	}
	// A missing attribute: dscl says so on stderr and prints nothing of use.
	for _, out := range []string{"", "No such key: UserShell\n", "UserShell:\n"} {
		if got := dsclShell(out); got != "" {
			t.Errorf("dscl %q: %q", out, got)
		}
	}
}
