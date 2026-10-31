//go:build darwin || linux

// Ported from Kero (github.com/egoist/kero, MIT, docs/third_party/kero-LICENSE),
// internal/daemon/pty_unix.go.

// Package pty runs a program in a pseudo-terminal the app owns, so that
// the app sees what the program writes before the terminal emulator does
// (status escapes), and knows the program's process: its foreground job
// and working directory.
package pty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

// Proc is a program running in a pseudo-terminal of its own.
type Proc struct {
	master *os.File
	cmd    *exec.Cmd
	exited chan struct{}
	code   int
}

// Options are how Start runs a program.
type Options struct {
	// Path is the executable, Args its argv (Args[0] included).
	Path string
	Args []string
	Dir  string
	Env  []string
	Cols int
	Rows int
}

// Start runs a program in a new pseudo-terminal.
func Start(o Options) (*Proc, error) {
	master, slaveName, err := openMaster()
	if err != nil {
		return nil, err
	}
	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, fmt.Errorf("pty: opening %s: %w", slaveName, err)
	}
	defer slave.Close() // the child has its own copies
	if err := setSize(master, o.Cols, o.Rows, 0, 0); err != nil {
		master.Close()
		return nil, err
	}
	cmd := &exec.Cmd{Path: o.Path, Args: o.Args, Dir: o.Dir, Env: o.Env, Stdin: slave, Stdout: slave, Stderr: slave}
	// A session of its own, whose controlling terminal is the slave, so
	// that the terminal's signals reach the process group in the
	// foreground, and nothing ties it to the app's.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, err
	}
	p := &Proc{master: master, cmd: cmd, exited: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		p.code = 0
		var e *exec.ExitError
		if errors.As(err, &e) {
			if ws, ok := e.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				p.code = 128 + int(ws.Signal())
			} else {
				p.code = e.ExitCode()
			}
		}
		close(p.exited)
	}()
	return p, nil
}

// Read reads the program's output; io.EOF once nothing has the terminal
// open any more.
func (p *Proc) Read(b []byte) (int, error) {
	n, err := p.master.Read(b)
	if err != nil && n == 0 {
		if errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed) {
			err = io.EOF
		}
	}
	return n, err
}

// Write writes to the program's input.
func (p *Proc) Write(b []byte) (int, error) { return p.master.Write(b) }

// Resize resizes the terminal, a cell being cellW by cellH pixels (0 when
// unknown).
func (p *Proc) Resize(cols, rows, cellW, cellH int) error {
	return setSize(p.master, cols, rows, cellW, cellH)
}

// PID is the program's process, the leader of its session.
func (p *Proc) PID() int { return p.cmd.Process.Pid }

// Exited is closed once the program has exited; Code is then its status,
// 128+n for a signal n.
func (p *Proc) Exited() <-chan struct{} { return p.exited }
func (p *Proc) Code() int               { <-p.exited; return p.code }

// Close hangs up the terminal: its processes get SIGHUP.
func (p *Proc) Close() error { return p.master.Close() }

// Kill kills the program's process group.
func (p *Proc) Kill() {
	if syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL) != nil {
		p.cmd.Process.Kill()
	}
}

// Stop ends the program and everything of its process group: SIGTERM (and
// SIGCONT, for a stopped one), then SIGKILL for what is left after grace.
// Only the program's own group is signalled, never the leader's id alone
// when the group is gone.
func (p *Proc) Stop(grace time.Duration) {
	pgid := p.cmd.Process.Pid
	if !groupAlive(pgid) {
		return
	}
	syscall.Kill(-pgid, syscall.SIGTERM)
	syscall.Kill(-pgid, syscall.SIGCONT)
	for deadline := time.Now().Add(grace); groupAlive(pgid) && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if groupAlive(pgid) {
		syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

// groupAlive tells whether a process group has a member: the program may be
// gone, and what it started in its group not.
func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || err == syscall.EPERM
}

// Foreground returns the process group in the terminal's foreground: the
// shell's own when it waits at its prompt, else the job it runs.
func (p *Proc) Foreground() int {
	var pgid int32
	if ioctl(p.master, syscall.TIOCGPGRP, unsafe.Pointer(&pgid)) != nil {
		return 0
	}
	return int(pgid)
}

func setSize(f *os.File, cols, rows, cellW, cellH int) error {
	ws := struct{ row, col, xpixel, ypixel uint16 }{clamp(rows), clamp(cols), pixels(cols, cellW), pixels(rows, cellH)}
	return ioctl(f, syscall.TIOCSWINSZ, unsafe.Pointer(&ws))
}

func clamp(v int) uint16 { return uint16(max(1, min(v, 0xFFFF))) }

// pixels is the extent of n cells of size cell, 0 when unknown.
func pixels(n, cell int) uint16 { return uint16(max(0, min(n*cell, 0xFFFF))) }

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	c, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	err = c.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	})
	if err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}
