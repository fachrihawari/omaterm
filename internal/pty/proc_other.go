//go:build !linux && !darwin && !windows

package pty

// Process lookups are Linux's /proc, macOS's libSystem and Windows'
// Toolhelp only: elsewhere a pane knows no foreground program, and its
// status comes from its title and reports.
func ProcName(pid int) string   { return "" }
func ProcArgs(pid int) []string { return nil }
func ProcEnv(pid int) []string  { return nil }
func ProcCwd(pid int) string    { return "" }
func ProcParent(pid int) int    { return 0 }
func Procs() []int              { return nil }
