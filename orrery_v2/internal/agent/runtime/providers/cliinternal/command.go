package cliinternal

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/process"
)

type CommandSpec struct {
	Context     context.Context
	Executable  string
	Args        []string
	Cwd         string
	Env         map[string]string
	Provider    string
	StderrLabel string
	Timeout     time.Duration
	NewParser   func() Parser
	Stdin       []byte
}

func StartCommandSession(spec CommandSpec) (*runtimecore.RuntimeSession, error) {
	if spec.Executable == "" {
		return nil, fmt.Errorf("runtime executable is required")
	}
	if _, err := exec.LookPath(spec.Executable); err != nil {
		return nil, fmt.Errorf("%s executable not found at %q: %w", spec.Provider, spec.Executable, err)
	}
	ctx := spec.Context
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	cmd := exec.CommandContext(runCtx, spec.Executable, spec.Args...)
	if spec.Cwd != "" {
		cmd.Dir = spec.Cwd
	}
	cmd.Env = process.BuildEnv(spec.Env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%s stdout pipe: %w", spec.Provider, err)
	}
	if len(spec.Stdin) > 0 {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			cancel()
			return nil, fmt.Errorf("%s stdin pipe: %w", spec.Provider, err)
		}
		go func() {
			_, _ = stdin.Write(spec.Stdin)
			_ = stdin.Close()
		}()
	}
	stderrTail := process.NewStderrTail(nil, 2048)
	cmd.Stderr = stderrTail
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %s: %w", spec.Provider, err)
	}
	return StreamSession(StreamOptions{
		Context:     runCtx,
		Cancel:      cancel,
		Command:     cmd,
		Stdout:      closeOnCancel(runCtx, stdout),
		StderrTail:  stderrTail,
		Provider:    spec.Provider,
		StderrLabel: spec.StderrLabel,
		Timeout:     timeout,
		NewParser:   spec.NewParser,
	}), nil
}

func closeOnCancel(ctx context.Context, reader io.ReadCloser) io.Reader {
	go func() {
		<-ctx.Done()
		_ = reader.Close()
	}()
	return reader
}
