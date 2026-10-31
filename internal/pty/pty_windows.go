// The pseudo-console is ported from mygo (github.com/egoist/mygo, MIT, see
// THIRD_PARTY_NOTICES.md), plugins/terminal/internal/pty/pty_windows.go.

package pty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

// ErrUnsupported is Start's error where the app does not own terminals;
// never returned here.
var ErrUnsupported = errors.New("pty: not supported on this system")

var (
	kernel32                              = syscall.NewLazyDLL("kernel32.dll") // a KnownDLL
	procCreatePseudoConsole               = kernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole               = kernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole                = kernel32.NewProc("ClosePseudoConsole")
	procInitializeProcThreadAttributeList = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute         = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttributeList     = kernel32.NewProc("DeleteProcThreadAttributeList")
	procCreateJobObject                   = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject           = kernel32.NewProc("SetInformationJobObject")
	procQueryInformationJobObject         = kernel32.NewProc("QueryInformationJobObject")
	procAssignProcessToJobObject          = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject                = kernel32.NewProc("TerminateJobObject")
	procResumeThread                      = kernel32.NewProc("ResumeThread")
	procRtlGetVersion                     = syscall.NewLazyDLL("ntdll.dll").NewProc("RtlGetVersion") // a KnownDLL
)

const (
	procThreadAttributePseudoConsole = 0x00020016
	extendedStartupInfoPresent       = 0x00080000
	createUnicodeEnvironment         = 0x00000400
	createSuspended                  = 0x00000004

	jobObjectBasicAccountingInformation = 1
	jobObjectBasicProcessIDList         = 3
	jobObjectExtendedLimitInformation   = 9
	jobObjectLimitKillOnJobClose        = 0x2000
)

type startupInfoEx struct {
	syscall.StartupInfo
	attributes uintptr
}

// Proc is a program running in a pseudo-console of its own, in a job
// object with everything it starts, so that it ends with all of them.
type Proc struct {
	mu      sync.Mutex
	console uintptr // the HPCON, 0 once closed
	in, out *os.File
	process syscall.Handle
	job     syscall.Handle // 0 when the process couldn't join one
	pid     int
	exited  chan struct{}
	code    int
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

// Start runs a program in a new pseudo-console.
func Start(o Options) (*Proc, error) {
	if err := procCreatePseudoConsole.Find(); err != nil {
		return nil, errors.New("pty: ConPTY needs Windows 10 1809 or later")
	}
	// The console reads what is typed from inR and writes the output to
	// outW; this side writes to inW and reads outR.
	var inR, inW, outR, outW syscall.Handle
	if err := syscall.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return nil, fmt.Errorf("pty: %w", err)
	}
	if err := syscall.CreatePipe(&outR, &outW, nil, 0); err != nil {
		syscall.CloseHandle(inR)
		syscall.CloseHandle(inW)
		return nil, fmt.Errorf("pty: %w", err)
	}
	var console uintptr
	r, _, _ := procCreatePseudoConsole.Call(coord(o.Cols, o.Rows), uintptr(inR), uintptr(outW), 0, uintptr(unsafe.Pointer(&console)))
	// The console has its own copies of its ends.
	syscall.CloseHandle(inR)
	syscall.CloseHandle(outW)
	if r != 0 {
		syscall.CloseHandle(inW)
		syscall.CloseHandle(outR)
		return nil, fmt.Errorf("pty: CreatePseudoConsole: %w", syscall.Errno(r&0xFFFF))
	}
	p := &Proc{console: console, exited: make(chan struct{}),
		in: os.NewFile(uintptr(inW), "conpty-in"), out: os.NewFile(uintptr(outR), "conpty-out")}
	if err := p.spawn(o); err != nil {
		p.Close()
		p.out.Close()
		return nil, err
	}
	go p.wait()
	return p, nil
}

// spawn starts the program on the console, suspended until it is in its
// job, so that nothing it starts escapes the job.
func (p *Proc) spawn(o Options) error {
	var size uintptr
	procInitializeProcThreadAttributeList.Call(0, 1, 0, uintptr(unsafe.Pointer(&size)))
	list := make([]byte, size)
	if r, _, err := procInitializeProcThreadAttributeList.Call(uintptr(unsafe.Pointer(&list[0])), 1, 0, uintptr(unsafe.Pointer(&size))); r == 0 {
		return fmt.Errorf("pty: InitializeProcThreadAttributeList: %w", err)
	}
	defer procDeleteProcThreadAttributeList.Call(uintptr(unsafe.Pointer(&list[0])))
	if r, _, err := procUpdateProcThreadAttribute.Call(uintptr(unsafe.Pointer(&list[0])), 0, procThreadAttributePseudoConsole, p.console, unsafe.Sizeof(p.console), 0, 0); r == 0 {
		return fmt.Errorf("pty: UpdateProcThreadAttribute: %w", err)
	}
	si := startupInfoEx{attributes: uintptr(unsafe.Pointer(&list[0]))}
	si.Cb = uint32(unsafe.Sizeof(si))
	// The console's own standard handles: without invalid ones, a child of
	// a process whose output is redirected would write there instead.
	si.Flags = syscall.STARTF_USESTDHANDLES
	si.StdInput, si.StdOutput, si.StdErr = syscall.InvalidHandle, syscall.InvalidHandle, syscall.InvalidHandle

	cmdline, err := syscall.UTF16PtrFromString(commandLine(o.Path, o.Args))
	if err != nil {
		return err
	}
	var dir *uint16
	if o.Dir != "" {
		if dir, err = syscall.UTF16PtrFromString(o.Dir); err != nil {
			return err
		}
	}
	var env *uint16
	if o.Env != nil {
		block := environmentBlock(o.Env)
		env = &block[0]
	}
	var pi syscall.ProcessInformation
	err = syscall.CreateProcess(nil, cmdline, nil, nil, false, extendedStartupInfoPresent|createUnicodeEnvironment|createSuspended, env, dir, &si.StartupInfo, &pi)
	if err != nil {
		return fmt.Errorf("pty: starting %s: %w", o.Path, err)
	}
	p.process, p.pid = pi.Process, int(pi.ProcessId)
	p.job = newJob(pi.Process)
	procResumeThread.Call(uintptr(pi.Thread))
	syscall.CloseHandle(pi.Thread)
	return nil
}

// newJob puts a process in a job of its own that ends everything in it as
// it closes, 0 when it can't.
func newJob(process syscall.Handle) syscall.Handle {
	h, _, _ := procCreateJobObject.Call(0, 0)
	if h == 0 {
		return 0
	}
	var info jobExtendedLimit
	info.basic.limitFlags = jobObjectLimitKillOnJobClose
	r, _, _ := procSetInformationJobObject.Call(h, jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if r != 0 {
		r, _, _ = procAssignProcessToJobObject.Call(h, uintptr(process))
	}
	if r == 0 {
		syscall.CloseHandle(syscall.Handle(h))
		return 0
	}
	return syscall.Handle(h)
}

// jobExtendedLimit is JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
type jobExtendedLimit struct {
	basic struct {
		perProcessUserTimeLimit, perJobUserTimeLimit int64
		limitFlags                                   uint32
		minimumWorkingSetSize, maximumWorkingSetSize uintptr
		activeProcessLimit                           uint32
		affinity                                     uintptr
		priorityClass, schedulingClass               uint32
	}
	io                                    [6]uint64
	processMemoryLimit, jobMemoryLimit    uintptr
	peakProcessMemoryUsed, peakJobMemUsed uintptr
}

// wait waits for the program to exit, then closes the console, which ends
// what is still attached to it; Read returns io.EOF once it wrote what it
// had, as on Unix once nothing has the terminal open.
func (p *Proc) wait() {
	syscall.WaitForSingleObject(p.process, syscall.INFINITE)
	var code uint32
	syscall.GetExitCodeProcess(p.process, &code)
	p.code = int(code)
	close(p.exited)
	p.Close()
}

// Read reads the program's output; io.EOF once the console is closed.
func (p *Proc) Read(b []byte) (int, error) {
	n, err := p.out.Read(b)
	if err != nil && n == 0 && errors.Is(err, os.ErrClosed) {
		err = io.EOF
	}
	return n, err
}

// Write writes to the program's input.
func (p *Proc) Write(b []byte) (int, error) {
	n, err := p.in.Write(b)
	if errors.Is(err, os.ErrClosed) {
		err = io.ErrClosedPipe
	}
	return n, err
}

// Resize resizes the console; a console knows no pixels.
func (p *Proc) Resize(cols, rows, cellW, cellH int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.console == 0 {
		return io.ErrClosedPipe
	}
	if r, _, _ := procResizePseudoConsole.Call(p.console, coord(cols, rows)); r != 0 {
		return fmt.Errorf("pty: ResizePseudoConsole: %w", syscall.Errno(r&0xFFFF))
	}
	return nil
}

// PID is the program's process.
func (p *Proc) PID() int { return p.pid }

// Exited is closed once the program has exited; Code is then its status.
func (p *Proc) Exited() <-chan struct{} { return p.exited }
func (p *Proc) Code() int               { <-p.exited; return p.code }

// Close closes the console, as a hangup: the processes attached to it are
// told to close (CTRL_CLOSE_EVENT). Read returns io.EOF once it wrote what
// it had.
func (p *Proc) Close() error {
	p.mu.Lock()
	console := p.console
	p.console = 0
	p.mu.Unlock()
	if console == 0 {
		return nil
	}
	p.in.Close()
	// ClosePseudoConsole waits for the output to be read on Windows
	// before 11 24H2: Read keeps reading meanwhile, until the end of the
	// output, then the pipe goes.
	go func() {
		procClosePseudoConsole.Call(console)
		p.out.Close()
	}()
	return nil
}

// Kill kills the program and everything of its job.
func (p *Proc) Kill() {
	p.mu.Lock()
	job := p.job
	p.mu.Unlock()
	if job != 0 {
		if r, _, _ := procTerminateJobObject.Call(uintptr(job), 1); r != 0 {
			return
		}
	}
	select {
	case <-p.exited:
	default:
		syscall.TerminateProcess(p.process, 1)
	}
}

// Stop ends the program and everything of its job: what the console's
// close didn't end within grace is killed. The job then goes.
func (p *Proc) Stop(grace time.Duration) {
	p.mu.Lock()
	job := p.job
	p.mu.Unlock()
	if job == 0 {
		select {
		case <-p.exited:
		case <-time.After(grace):
			p.Kill()
		}
		return
	}
	for deadline := time.Now().Add(grace); p.jobActive() > 0 && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	p.Kill()
	p.mu.Lock()
	p.job = 0
	p.mu.Unlock()
	syscall.CloseHandle(job)
}

// jobActive is how many processes the job has left.
func (p *Proc) jobActive() int {
	p.mu.Lock()
	job := p.job
	p.mu.Unlock()
	var info struct {
		totalUserTime, totalKernelTime, thisPeriodUserTime, thisPeriodKernelTime int64
		totalPageFaults, totalProcesses, activeProcesses, totalTerminated        uint32
	}
	r, _, _ := procQueryInformationJobObject.Call(uintptr(job), jobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0)
	if r == 0 {
		return 0
	}
	return int(info.activeProcesses)
}

// jobPIDs lists the processes of the job, nil when it has none.
func (p *Proc) jobPIDs() map[int]bool {
	p.mu.Lock()
	job := p.job
	p.mu.Unlock()
	if job == 0 {
		return nil
	}
	buf := make([]uintptr, 2+256)
	r, _, _ := procQueryInformationJobObject.Call(uintptr(job), jobObjectBasicProcessIDList, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))*unsafe.Sizeof(buf[0]), 0)
	if r == 0 {
		return nil
	}
	// Two 32-bit counts share the first word on 64-bit Windows, and take
	// one word each on 32-bit.
	n := int(uint32(buf[0] >> 32))
	ids := buf[1:]
	if unsafe.Sizeof(buf[0]) == 4 {
		n, ids = int(buf[1]), buf[2:]
	}
	set := make(map[int]bool, n)
	for _, id := range ids[:min(n, len(ids))] {
		set[int(id)] = true
	}
	return set
}

// Foreground returns the program the pane runs (see foreground), 0 once
// the program has exited.
func (p *Proc) Foreground() int {
	select {
	case <-p.exited:
		return 0
	default:
	}
	all := snapshot()
	if in := p.jobPIDs(); in != nil {
		procs := all[:0:0]
		for _, q := range all {
			if in[q.pid] {
				procs = append(procs, q)
			}
		}
		all = procs
	}
	return foreground(p.pid, all, ProcArgs)
}

// ClearScreen tells the console that the terminal cleared its screen, as
// Windows Terminal's ClearPseudoConsole does: the console clears its
// buffer and repaints with the cursor's line at the top. Without it, the
// console keeps the prompt on its old row and, as it redraws with
// absolute cursor moves (PSReadLine's predictions), takes the cursor back
// down there. It does nothing before Windows 11 24H2, whose console may
// shut down on a signal it doesn't know, or once the console is closed.
func (p *Proc) ClearScreen() error {
	if windowsBuild() < 26100 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.console == 0 {
		return nil
	}
	// kernel32 doesn't export ClearPseudoConsole. The HPCON points at the
	// console's handles, first the pipe of its signals, which take a
	// 16-bit code: PTY_SIGNAL_CLEAR_WINDOW is 2.
	signals := **(**syscall.Handle)(unsafe.Pointer(&p.console))
	var n uint32
	if err := syscall.WriteFile(signals, []byte{2, 0}, &n, nil); err != nil {
		return fmt.Errorf("pty: clearing the console: %w", err)
	}
	return nil
}

// windowsBuild returns the build of Windows 10 or 11, the maximum on a
// later major version. RtlGetVersion tells the version whatever the
// executable's manifest declares.
var windowsBuild = sync.OnceValue(func() uint32 {
	var v struct {
		size, major, minor, build, platform uint32
		servicePack                         [128]uint16
	}
	v.size = uint32(unsafe.Sizeof(v))
	procRtlGetVersion.Call(uintptr(unsafe.Pointer(&v)))
	switch {
	case v.major > 10:
		return ^uint32(0)
	case v.major < 10:
		return 0
	}
	return v.build
})

// coord packs a COORD, which ConPTY takes by value.
func coord(cols, rows int) uintptr {
	return uintptr(uint16(max(1, min(cols, 0x7FFF)))) | uintptr(uint16(max(1, min(rows, 0x7FFF))))<<16
}

// environmentBlock encodes env as CreateProcess takes it: NUL-terminated
// UTF-16 strings, then another NUL.
func environmentBlock(env []string) []uint16 {
	var b []uint16
	for _, kv := range env {
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	if len(b) == 0 {
		b = append(b, 0)
	}
	return append(b, 0)
}
