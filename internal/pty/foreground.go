package pty

import "strings"

// winProc is a process in a snapshot of Windows' process list.
type winProc struct {
	pid, ppid int
	name      string // without .exe, lower case: "node", "claude"
}

// foreground picks a Windows pane's foreground program, which Windows
// doesn't track: the newest program the shell runs, past cmd's /c that a
// .cmd launcher (npm's claude.cmd) runs its program in. The console's own
// host is no program of the pane's. It returns shell when the shell runs
// nothing.
func foreground(shell int, procs []winProc, args func(pid int) []string) int {
	newest := func(parent int) int {
		pid := 0
		for _, p := range procs {
			if p.ppid == parent && p.pid != parent && p.name != "conhost" && p.name != "openconsole" {
				pid = p.pid
			}
		}
		return pid
	}
	cur := newest(shell)
	if cur == 0 {
		return shell
	}
	for name(procs, cur) == "cmd" && launcher(args(cur)) {
		next := newest(cur)
		if next == 0 {
			break
		}
		cur = next
	}
	return cur
}

func name(procs []winProc, pid int) string {
	for _, p := range procs {
		if p.pid == pid {
			return p.name
		}
	}
	return ""
}

// launcher tells cmd running one command (/c), as a .cmd file runs,
// from an interactive cmd the user started.
func launcher(args []string) bool {
	for _, a := range args[min(1, len(args)):] {
		if strings.EqualFold(a, "/c") {
			return true
		}
	}
	return false
}

// exeName is a process's name as panes show it: "node.exe" is "node".
func exeName(file string) string {
	if i := strings.LastIndexAny(file, `\/`); i >= 0 {
		file = file[i+1:]
	}
	file = strings.ToLower(file)
	return strings.TrimSuffix(file, ".exe")
}
