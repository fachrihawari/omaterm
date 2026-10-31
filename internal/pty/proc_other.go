//go:build !linux && !darwin

package pty

// Process lookups are Linux's /proc and macOS's libSystem only: elsewhere
// a pane knows no foreground program, and its status comes from its title
// and reports.
func ProcName(pid int) string   { return "" }
func ProcArgs(pid int) []string { return nil }
func ProcEnv(pid int) []string  { return nil }
func ProcCwd(pid int) string    { return "" }
func ProcParent(pid int) int    { return 0 }
func Procs() []int              { return nil }
