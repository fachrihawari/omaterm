//go:build windows

package shellcmd

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// PowerShell and cmd run what LoginArgs and Args give them, and exit with,
// or tell, the command's status.
func TestWindowsShellsRun(t *testing.T) {
	shells := []string{os.Getenv("ComSpec")}
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			shells = append(shells, p)
		}
	}
	for _, shell := range shells {
		argv := LoginArgs(shell, `echo "it's here" & exit 7`)
		if powerShell(Name(shell)) {
			argv = LoginArgs(shell, `Write-Output "it's here"; exit 7`)
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		if Name(shell) == "cmd" {
			// cmd's line goes in raw, as package pty passes it.
			cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(shell) + " /d /s /c \"" + argv[4] + "\""}
		}
		out, err := cmd.Output()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 7 {
			t.Errorf("%s: exit %v, want 7 (output %q)", Name(shell), err, out)
		}
		if !strings.Contains(string(out), "it's here") {
			t.Errorf("%s: output %q", Name(shell), out)
		}
	}
}

func TestPowerShellReportsExit(t *testing.T) {
	shell, err := exec.LookPath("pwsh.exe")
	if err != nil {
		if shell, err = exec.LookPath("powershell.exe"); err != nil {
			t.Skip("no PowerShell")
		}
	}
	args := Args(shell, []string{"cmd", "/c", "exit 5"})
	// -NoExit would wait for input at the prompt: the same script, then
	// the shell's end.
	for i, a := range args {
		if a == "-NoExit" {
			args = append(args[:i:i], args[i+1:]...)
			break
		}
	}
	out, _ := exec.Command(shell, args[1:]...).Output()
	if !strings.Contains(string(out), "\x1b]133;D;5\a") {
		t.Errorf("output %q", out)
	}
}
