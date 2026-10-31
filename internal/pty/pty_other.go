//go:build !darwin && !linux

package pty

import (
	"errors"
	"io"
	"time"
)

// ErrUnsupported is Start's error where the app does not own terminals
// (Windows): the terminal plugin runs the program itself there.
var ErrUnsupported = errors.New("pty: not supported on this system")

type Proc struct{}

type Options struct {
	Path       string
	Args       []string
	Dir        string
	Env        []string
	Cols, Rows int
}

func Start(Options) (*Proc, error)                      { return nil, ErrUnsupported }
func (*Proc) Read([]byte) (int, error)                  { return 0, io.EOF }
func (*Proc) Write(b []byte) (int, error)               { return len(b), nil }
func (*Proc) Resize(cols, rows, cellW, cellH int) error { return nil }
func (*Proc) PID() int                                  { return 0 }
func (*Proc) Exited() <-chan struct{}                   { c := make(chan struct{}); close(c); return c }
func (*Proc) Code() int                                 { return 0 }
func (*Proc) Close() error                              { return nil }
func (*Proc) Kill()                                     {}
func (*Proc) Stop(time.Duration)                        {}
func (*Proc) Foreground() int                           { return 0 }
