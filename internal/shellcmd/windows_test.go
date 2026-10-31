package shellcmd

import (
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

// decode is the script of PowerShell's -EncodedCommand.
func decode(t *testing.T, b64 string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b)%2 != 0 {
		t.Fatalf("not base64 UTF-16LE: %q", b64)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

func TestWindowsShell(t *testing.T) {
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, h := range names {
				if h == n {
					return `C:\bin\` + n, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	for _, c := range []struct {
		look    func(string) (string, error)
		comspec string
		want    string
	}{
		{has("pwsh.exe", "powershell.exe"), `C:\Windows\system32\cmd.exe`, `C:\bin\pwsh.exe`},
		{has("powershell.exe"), `C:\Windows\system32\cmd.exe`, `C:\bin\powershell.exe`},
		{has(), `C:\Windows\system32\cmd.exe`, `C:\Windows\system32\cmd.exe`},
		{has(), "", "cmd.exe"},
	} {
		if got := windowsShell(c.look, c.comspec); got != c.want {
			t.Errorf("windowsShell = %q, want %q", got, c.want)
		}
	}
}

func TestWindowsName(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Program Files\PowerShell\7\pwsh.exe`:                    "pwsh",
		`C:\WINDOWS\System32\WindowsPowerShell\v1.0\powershell.exe`: "powershell",
		`C:\Windows\system32\CMD.EXE`:                               "cmd",
		`C:\Program Files\Git\bin\bash.exe`:                         "bash",
		"/bin/zsh":                                                  "zsh",
		"fish":                                                      "fish",
	} {
		if got := Name(in); got != want {
			t.Errorf("Name(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWindowsArgs(t *testing.T) {
	pwsh := `C:\Program Files\PowerShell\7\pwsh.exe`
	if got := Args(pwsh, nil); !reflect.DeepEqual(got, []string{"pwsh", "-NoLogo"}) {
		t.Errorf("Args(pwsh) = %q", got)
	}
	got := Args(pwsh, []string{"claude", "--resume", "it's"})
	if len(got) != 5 || got[2] != "-NoExit" || got[3] != "-EncodedCommand" {
		t.Fatalf("Args(pwsh, claude) = %q", got)
	}
	script := decode(t, got[4])
	for _, want := range []string{"& claude --resume 'it''s';", "133;D;$code", "$global:LASTEXITCODE = $null"} {
		if !strings.Contains(script, want) {
			t.Errorf("script %q lacks %q", script, want)
		}
	}
	if got := decode(t, Args(pwsh, []string{"npm run dev"})[4]); !strings.Contains(got, "; npm run dev;") {
		t.Errorf("a one-line command is run as it is: %q", got)
	}

	cmd := `C:\Windows\system32\cmd.exe`
	if got := Args(cmd, nil); !reflect.DeepEqual(got, []string{"cmd"}) {
		t.Errorf("Args(cmd) = %q", got)
	}
	want := []string{"cmd", "/d", "/s", "/k", `claude "a b" "say ""hi"""`}
	if got := Args(cmd, []string{"claude", "a b", `say "hi"`}); !reflect.DeepEqual(got, want) {
		t.Errorf("Args(cmd, claude) = %q, want %q", got, want)
	}
}

func TestWindowsLoginArgs(t *testing.T) {
	got := LoginArgs(`C:\bin\pwsh.exe`, `npm run dev -- --port "3000"`)
	if len(got) != 4 || got[0] != `C:\bin\pwsh.exe` || got[2] != "-EncodedCommand" {
		t.Fatalf("LoginArgs(pwsh) = %q", got)
	}
	if s := decode(t, got[3]); !strings.Contains(s, `; npm run dev -- --port "3000";`) || !strings.HasSuffix(s, "exit $code") {
		t.Errorf("script = %q", s)
	}
	want := []string{`C:\Windows\system32\cmd.exe`, "/d", "/s", "/c", "npm run dev"}
	if got := LoginArgs(`C:\Windows\system32\cmd.exe`, "npm run dev"); !reflect.DeepEqual(got, want) {
		t.Errorf("LoginArgs(cmd) = %q, want %q", got, want)
	}
}
