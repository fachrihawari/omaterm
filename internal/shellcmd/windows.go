package shellcmd

import (
	"encoding/base64"
	"strings"
	"unicode/utf16"
)

// Windows' shells. PowerShell takes its script as -EncodedCommand, base64
// of UTF-16LE, which no quoting of the command line can break; cmd takes
// its line raw after /c or /k (package pty keeps it as it is).

// windowsShell picks the shell of a Windows pane, as Windows Terminal
// would: PowerShell 7, else Windows PowerShell, else %ComSpec%.
func windowsShell(lookPath func(string) (string, error), comspec string) string {
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if p, err := lookPath(name); err == nil {
			return p
		}
	}
	if comspec != "" {
		return comspec
	}
	return "cmd.exe"
}

// powerShell tells PowerShell's names apart from other shells'.
func powerShell(name string) bool { return name == "pwsh" || name == "powershell" }

// encoded is the arguments that run script in PowerShell.
func encoded(script string) []string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return []string{"-EncodedCommand", base64.StdEncoding.EncodeToString(b)}
}

// psQuote quotes an argument for PowerShell, where only ' is special
// inside single quotes.
func psQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./\\:,+=", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// psJoin writes a command for PowerShell: its call operator runs a quoted
// program name, which alone would be a string.
func psJoin(args []string) string {
	if OneLine(args) {
		return args[0]
	}
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = psQuote(a)
	}
	return "& " + strings.Join(q, " ")
}

// psStatus is PowerShell that leaves the status of what ran last in $code:
// a program's exit code, else 1 for a failed command.
const psStatus = `$ok = $?; $code = if ($null -ne $global:LASTEXITCODE) { $global:LASTEXITCODE } elseif ($ok) { 0 } else { 1 }`

// cmdJoin writes a command for cmd, which reads its line as it is: an
// argument with a space or a quote is put in double quotes.
func cmdJoin(args []string) string {
	if OneLine(args) {
		return args[0]
	}
	q := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"&|<>^()") {
			a = `"` + strings.ReplaceAll(a, `"`, `""`) + `"`
		}
		q[i] = a
	}
	return strings.Join(q, " ")
}

// windowsArgs is Args for PowerShell and cmd: the shell with its logo
// left out, or running the command first and staying once it ends.
// PowerShell then tells the command's status (OSC 133;D), as Args does on
// Unix; cmd can't print an escape from its command line.
func windowsArgs(name string, command []string) []string {
	if powerShell(name) {
		if len(command) == 0 {
			return []string{name, "-NoLogo"}
		}
		script := "$global:LASTEXITCODE = $null; " + psJoin(command) + "; " + psStatus +
			"; [Console]::Write(\"$([char]27)]133;D;$code$([char]7)\")"
		return append([]string{name, "-NoLogo", "-NoExit"}, encoded(script)...)
	}
	if len(command) == 0 {
		return []string{name}
	}
	return []string{name, "/d", "/s", "/k", cmdJoin(command)}
}

// windowsLoginArgs is LoginArgs for PowerShell and cmd: the shell runs the
// command with the user's profile, and exits with its status.
func windowsLoginArgs(shell, name, cmd string) []string {
	if powerShell(name) {
		script := "$global:LASTEXITCODE = $null; " + cmd + "; " + psStatus + "; exit $code"
		return append([]string{shell, "-NoLogo"}, encoded(script)...)
	}
	return []string{shell, "/d", "/s", "/c", cmd}
}

// windowsName tells pwsh, powershell and cmd by their names, without the
// folder or the .exe, whichever separator the path has.
func windowsName(name string) string {
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	if len(name) > 4 && strings.EqualFold(name[len(name)-4:], ".exe") {
		name = name[:len(name)-4]
	}
	switch l := strings.ToLower(name); l {
	case "pwsh", "powershell", "cmd":
		return l
	}
	return name
}
