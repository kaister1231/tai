package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestShellExecutorBoundsCapturedOutput(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	result, err := (ShellExecutor{Dir: t.TempDir()}).Execute(context.Background(), "head -c 65536 /dev/zero", 3*time.Second, 128)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Stdout) != 128 || !result.Truncated {
		t.Fatalf("output was not bounded: bytes=%d truncated=%v", len(result.Stdout), result.Truncated)
	}
}

func TestShellExecutorUsesNullInputAndProvidedDirectory(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := t.TempDir()
	result, err := (ShellExecutor{Dir: dir}).Execute(context.Background(), "pwd; if read value; then echo unexpected; else echo stdin-closed; fi", 3*time.Second, 1024)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Stdout, dir) || !strings.Contains(result.Stdout, "stdin-closed") || strings.Contains(result.Stdout, "unexpected") {
		t.Fatalf("unexpected shell output: %q", result.Stdout)
	}
}

func TestTimeoutKillsProcessGroupChildren(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	command := "sleep 10 & child=$!; printf '%s\\n' \"$child\"; wait"
	start := time.Now()
	result, err := (ShellExecutor{Dir: t.TempDir()}).Execute(context.Background(), command, 500*time.Millisecond, 1024)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.TimedOut {
		t.Fatalf("expected timeout, got %#v", result)
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) == 0 {
		t.Fatalf("child PID was not captured: %q", result.Stdout)
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("invalid child PID %q: %v", fields[0], err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("timeout cleanup took too long: %s", time.Since(start))
	}
	deadline := time.Now().Add(time.Second)
	for processIsRunning(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processIsRunning(pid) {
		t.Fatalf("timed-out child process %d is still running", pid)
	}
}

func TestShellExecutorCancellationIsBounded(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	startedFile := filepath.Join(dir, "started")
	pidFile := filepath.Join(dir, "child.pid")
	command := fmt.Sprintf("sleep 30 & child=$!; printf '%%s' \"$child\" > %s; printf ready > %s; wait",
		shellQuote(pidFile), shellQuote(startedFile))
	done := make(chan struct {
		result CommandResult
		err    error
	}, 1)
	go func() {
		result, err := (ShellExecutor{Dir: dir}).Execute(ctx, command, time.Minute, 1024)
		done <- struct {
			result CommandResult
			err    error
		}{result, err}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not start before cancellation")
		}
		time.Sleep(10 * time.Millisecond)
	}
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read child PID: %v", err)
	}
	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		t.Fatalf("invalid child PID %q: %v", pidBytes, err)
	}
	start := time.Now()
	cancel()
	select {
	case outcome := <-done:
		if outcome.err != nil {
			t.Fatalf("Execute after cancellation: %v", outcome.err)
		}
		if time.Since(start) > 2*time.Second {
			t.Fatalf("cancellation took too long: %s", time.Since(start))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Execute did not return after cancellation")
	}
	deadline = time.Now().Add(time.Second)
	for processIsRunning(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processIsRunning(pid) {
		t.Fatalf("cancelled child process %d is still running", pid)
	}
}

func TestWaitDelayBoundsEscapedDescendantPipes(t *testing.T) {
	setsid, err := exec.LookPath("setsid")
	if err != nil {
		t.Skip("setsid is unavailable")
	}
	t.Setenv("SHELL", "/bin/sh")
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	command := fmt.Sprintf("%s /bin/sh -c 'sleep 30' & child=$!; printf '%%s' \"$child\" > %s; printf ready",
		shellQuote(setsid), shellQuote(pidFile))
	start := time.Now()
	result, err := (ShellExecutor{Dir: t.TempDir()}).Execute(context.Background(), command, 3*time.Second, 1024)
	pidBytes, pidErr := os.ReadFile(pidFile)
	if pidErr == nil {
		pid, parseErr := strconv.Atoi(string(pidBytes))
		if parseErr != nil {
			t.Fatalf("invalid detached child PID %q: %v", pidBytes, parseErr)
		}
		t.Cleanup(func() {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		})
	}
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("inherited output pipe delayed completion: %s", time.Since(start))
	}
	if !result.Truncated {
		t.Fatal("output closed by WaitDelay was not reported as truncated")
	}
	if pidErr != nil {
		t.Fatalf("read detached child PID: %v", pidErr)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func processIsRunning(pid int) bool {
	if data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		if end := strings.LastIndex(string(data), ") "); end >= 0 {
			fields := strings.Fields(string(data)[end+2:])
			if len(fields) > 0 && fields[0] == "Z" {
				return false
			}
		}
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}
