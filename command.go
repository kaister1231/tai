package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type ShellExecutor struct {
	Dir string
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		keep := len(value)
		if keep > remaining {
			keep = remaining
		}
		_, _ = b.buffer.Write(value[:keep])
	}
	if len(value) > remaining {
		b.truncated = true
	}
	return len(value), nil
}

func (b *boundedBuffer) String() string {
	return b.buffer.String()
}

func (e ShellExecutor) Execute(ctx context.Context, command string, timeout time.Duration, outputLimit int) (CommandResult, error) {
	if err := validateCommand(command); err != nil {
		return CommandResult{}, err
	}
	if timeout <= 0 || timeout > 10*time.Minute {
		return CommandResult{}, fmt.Errorf("command timeout is outside the supported range")
	}
	if outputLimit <= 0 || outputLimit > maxOutputLimit {
		return CommandResult{}, fmt.Errorf("output limit is outside the supported range")
	}
	if err := ctx.Err(); err != nil {
		return CommandResult{}, err
	}
	shell, err := configuredShell()
	if err != nil {
		return CommandResult{}, err
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return CommandResult{}, fmt.Errorf("could not open null input")
	}
	defer devNull.Close()
	stdout := &boundedBuffer{limit: outputLimit}
	stderr := &boundedBuffer{limit: outputLimit}
	cmd := exec.Command(shell, "-c", command)
	cmd.Dir = e.Dir
	cmd.Stdin = devNull
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return CommandResult{}, fmt.Errorf("could not start shell: %s", safeTerminal(err.Error()))
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	result := CommandResult{}
	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		waitErr = stopProcessGroup(cmd.Process.Pid, done)
	case <-timer.C:
		result.TimedOut = true
		waitErr = stopProcessGroup(cmd.Process.Pid, done)
	}
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	result.Truncated = stdout.truncated || stderr.truncated
	result.ExitCode = 0
	if errors.Is(waitErr, exec.ErrWaitDelay) {
		result.Truncated = true
		waitErr = nil
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else if ctx.Err() == nil && !result.TimedOut {
			return result, fmt.Errorf("command did not finish cleanly: %s", safeTerminal(waitErr.Error()))
		} else {
			result.ExitCode = -1
		}
	}
	return result, nil
}

func configuredShell() (string, error) {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/sh"
	}
	switch strings.ToLower(filepath.Base(shell)) {
	case "sh", "dash", "bash", "zsh", "ksh":
	default:
		return "", fmt.Errorf("SHELL must name a supported POSIX shell (sh, dash, bash, zsh, or ksh)")
	}
	path, err := exec.LookPath(shell)
	if err != nil {
		return "", fmt.Errorf("configured shell is not available")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("configured shell is not executable")
	}
	return path, nil
}

func stopProcessGroup(pid int, done <-chan error) error {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	grace := time.NewTimer(150 * time.Millisecond)
	defer grace.Stop()
	select {
	case err := <-done:
		if syscall.Kill(-pid, 0) == nil {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
		return err
	case <-grace.C:
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		return <-done
	}
}
