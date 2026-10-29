package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

type Runner struct {
	stderrTailBytes int
	stderrMirror    io.Writer
}

type RunnerOptions struct {
	StderrTailBytes int
	StderrMirror    io.Writer
}

func NewRunner(opts RunnerOptions) *Runner {
	return &Runner{
		stderrTailBytes: opts.StderrTailBytes,
		stderrMirror:    opts.StderrMirror,
	}
}

func (r *Runner) Run(ctx context.Context, command Command) (Result, error) {
	if command.Executable == "" {
		return Result{}, fmt.Errorf("process executable is required")
	}
	started := time.Now().UTC()
	runCtx := ctx
	cancel := func() {}
	if command.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, command.Timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, command.Executable, command.Args...)
	if command.Dir != "" {
		cmd.Dir = command.Dir
	}
	cmd.Env = buildEnv(command.Env)
	if command.Stdin != "" {
		cmd.Stdin = bytes.NewBufferString(command.Stdin)
	}
	var stdout bytes.Buffer
	stderr := newStderrTail(r.stderrMirror, r.stderrTailBytes)
	cmd.Stdout = &stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	completed := time.Now().UTC()
	result := Result{
		Stdout:      stdout.String(),
		StderrTail:  stderr.Tail(),
		StartedAt:   started,
		CompletedAt: completed,
		Duration:    completed.Sub(started),
	}
	if runCtx.Err() != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			result.Status = StatusTimeout
			result.Error = runCtx.Err().Error()
			return result, nil
		}
		result.Status = StatusCancelled
		result.Error = runCtx.Err().Error()
		return result, nil
	}
	if err != nil {
		result.Status = StatusFailed
		result.Error = err.Error()
		if exitErr := new(exec.ExitError); errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, err
	}
	result.Status = StatusCompleted
	return result, nil
}
