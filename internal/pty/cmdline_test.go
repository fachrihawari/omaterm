package pty

import (
	"reflect"
	"testing"
)

func TestCommandLine(t *testing.T) {
	for _, c := range []struct {
		path string
		args []string
		want string
	}{
		{`C:\Program Files\PowerShell\7\pwsh.exe`, []string{"pwsh", "-NoLogo"}, `"C:\Program Files\PowerShell\7\pwsh.exe" -NoLogo`},
		{`C:\bin\node.exe`, []string{"node", `a "b"`, `c:\x\`, ""}, `C:\bin\node.exe "a \"b\"" c:\x\ ""`},
		// cmd's line goes in as it is, between the quotes /s strips.
		{`C:\Windows\system32\cmd.exe`, []string{"cmd", "/d", "/s", "/c", `npm run dev -- --name "a b" & echo %PATH%`},
			`C:\Windows\system32\cmd.exe /d /s /c "npm run dev -- --name "a b" & echo %PATH%"`},
		{`C:\Windows\system32\cmd.exe`, []string{"cmd", "/k", "claude"}, `C:\Windows\system32\cmd.exe /s /k "claude"`},
		{`C:\Windows\system32\cmd.exe`, []string{"cmd"}, `C:\Windows\system32\cmd.exe`},
	} {
		if got := commandLine(c.path, c.args); got != c.want {
			t.Errorf("commandLine(%q) =\n %s\nwant\n %s", c.args, got, c.want)
		}
	}
}

// What escapeArg writes, splitCommandLine reads back.
func TestCommandLineRoundTrip(t *testing.T) {
	args := []string{`C:\Program Files\x.exe`, "plain", "with space", `quote"inside`, `trailing\`, `trail space\ `, `\\server\share\`, "", `a\\"b`}
	line := ""
	for i, a := range args {
		if i > 0 {
			line += " "
		}
		line += escapeArg(a)
	}
	if got := splitCommandLine(line); !reflect.DeepEqual(got, args) {
		t.Errorf("split(%s) =\n %q\nwant\n %q", line, got, args)
	}
	if got := splitCommandLine(`"C:\Windows\system32\cmd.exe" /c "C:\Users\me\AppData\Roaming\npm\claude.cmd" --resume`); !reflect.DeepEqual(got,
		[]string{`C:\Windows\system32\cmd.exe`, "/c", `C:\Users\me\AppData\Roaming\npm\claude.cmd`, "--resume"}) {
		t.Errorf("split = %q", got)
	}
}

func TestForeground(t *testing.T) {
	procs := []winProc{
		{pid: 10, ppid: 1, name: "pwsh"},
		{pid: 11, ppid: 1, name: "conhost"},
		{pid: 20, ppid: 10, name: "cmd"},  // npm's claude.cmd
		{pid: 30, ppid: 20, name: "node"}, // the agent
		{pid: 40, ppid: 30, name: "bash"}, // one of its tools
		{pid: 50, ppid: 60, name: "cmd"},  // the user's own cmd, at its prompt
		{pid: 60, ppid: 1, name: "pwsh"},
	}
	args := map[int][]string{20: {"cmd.exe", "/c", "claude.cmd"}, 50: {"cmd.exe"}}
	get := func(pid int) []string { return args[pid] }
	for shell, want := range map[int]int{10: 30, 60: 50, 30: 40, 40: 40} {
		if got := foreground(shell, procs, get); got != want {
			t.Errorf("foreground(%d) = %d, want %d", shell, got, want)
		}
	}
	if got := exeName(`C:\Program Files\nodejs\Node.EXE`); got != "node" {
		t.Errorf("exeName = %q", got)
	}
}
