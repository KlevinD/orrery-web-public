package process

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunnerCapturesOutputAndBoundedStderrTail(t *testing.T) {
	runner := NewRunner(RunnerOptions{StderrTailBytes: 12})
	cmd := Command{
		Executable: shellExecutable(),
		Args:       shellArgs("echo hello && echo long-stderr-message 1>&2"),
	}

	result, err := runner.Run(context.Background(), cmd)
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("status = %q, stderr=%q", result.Status, result.StderrTail)
	}
	if strings.TrimSpace(result.Stdout) != "hello" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
	if len(result.StderrTail) > 12 {
		t.Fatalf("stderr tail should be bounded, got %q", result.StderrTail)
	}
	if !strings.HasSuffix(result.StderrTail, "message") {
		t.Fatalf("stderr tail = %q", result.StderrTail)
	}
}

func TestRunnerReturnsTimeoutStatus(t *testing.T) {
	runner := NewRunner(RunnerOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cmd := Command{
		Executable: shellExecutable(),
		Args:       shellArgs("sleep 2"),
		Timeout:    time.Second,
	}

	result, err := runner.Run(ctx, cmd)
	if err != nil {
		t.Fatalf("timeout should be represented as a process result, got error: %v", err)
	}
	if result.Status != StatusTimeout {
		t.Fatalf("status = %q", result.Status)
	}
	if result.Duration <= 0 {
		t.Fatalf("expected duration to be captured")
	}
}

func shellExecutable() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "sh"
}

func shellArgs(script string) []string {
	if runtime.GOOS == "windows" {
		script = strings.ReplaceAll(script, "sleep 2", "ping -n 3 127.0.0.1 >NUL")
		return []string{"/C", script}
	}
	return []string{"-c", script}
}
