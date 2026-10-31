//go:build windows

package cli

import (
	"os"
	"syscall"
)

// The omaterm executable is a GUI program on Windows, which starts with no
// console: a shell that runs it attaches none, so its output goes nowhere.
var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole = kernel32.NewProc("AttachConsole")
	procGetStdHandle  = kernel32.NewProc("GetStdHandle")
)

const (
	attachParentProcess = ^uintptr(0) // ATTACH_PARENT_PROCESS, (DWORD)-1
	stdOutputHandle     = ^uint32(10) // STD_OUTPUT_HANDLE, (DWORD)-11
	stdErrorHandle      = ^uint32(11) // STD_ERROR_HANDLE, (DWORD)-12
)

// attachConsole points the standard output and error that have no handle at
// the parent shell's console. A redirected stream has a handle already, so a
// pipe keeps working; the shell doesn't wait for the command, though.
func attachConsole() {
	outOK := hasHandle(stdOutputHandle)
	errOK := hasHandle(stdErrorHandle)
	if outOK && errOK {
		return
	}
	if ok, _, _ := procAttachConsole.Call(attachParentProcess); ok == 0 {
		return
	}
	console, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		return
	}
	if !outOK {
		os.Stdout = console
	}
	if !errOK {
		os.Stderr = console
	}
}

// hasHandle tells whether the standard stream has a handle to write to.
func hasHandle(which uint32) bool {
	h, _, _ := procGetStdHandle.Call(uintptr(which))
	return h != 0 && h != ^uintptr(0)
}
