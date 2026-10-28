package worktree

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// command is git in dir, set up as omaterm runs it: it never takes the
// index's optional lock, and never asks for a password.
func command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-c", "core.quotepath=false", "-c", "color.ui=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	hide(cmd)
	return cmd
}

// out runs git and returns what it printed, its error what git said.
func out(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := command(ctx, dir, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return string(b), said(stderr.String(), err)
	}
	return strings.TrimRight(string(b), "\n"), nil
}

// do runs git for what it changes; its error is the end of what git said,
// where a hook's output ends with its verdict.
func do(ctx context.Context, dir string, args ...string) error {
	b, err := command(ctx, dir, args...).CombinedOutput()
	if err != nil {
		return said(string(b), err)
	}
	return nil
}

// test runs a git command that answers by its exit status: 0 is yes, 1
// is no, and anything else an error.
func test(ctx context.Context, dir string, args ...string) (bool, error) {
	cmd := command(ctx, dir, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, said(stderr.String(), err)
}

// said makes git's last lines the error, or err when it said nothing.
func said(msg string, err error) error {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return err
	}
	lines := strings.Split(msg, "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(strings.TrimPrefix(l, "fatal: "), "error: ")
	}
	return errors.New(strings.Join(lines, "\n"))
}
