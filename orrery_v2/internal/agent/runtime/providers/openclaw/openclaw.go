package openclaw

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/process"
	"orrery_v2/internal/agent/runtime/providers/cliinternal"
)

type Config struct {
	Executable     string
	Env            map[string]string
	ApprovalBroker runtimecore.ApprovalBroker
}

type Backend struct {
	config Config
}

func NewBackend(config Config) *Backend {
	return &Backend{config: config}
}

func (b *Backend) Execute(ctx context.Context, prompt string, opts runtimecore.ExecOptions) (*runtimecore.RuntimeSession, error) {
	if err := b.ValidateProviderConfig(opts.ProviderConfig); err != nil {
		return nil, err
	}
	cfg := b.config
	runtimeCfg := parseRuntimeConfig(opts.ProviderConfig)
	opts = applyRuntimeConfig(opts, &cfg)
	if runtimeCfg.isRemoteHTTP() {
		return executeRemoteHTTP(ctx, prompt, opts, runtimeCfg)
	}
	if runtimeCfg.isRemoteWS() {
		return executeRemoteWS(ctx, prompt, opts, runtimeCfg, cfg.ApprovalBroker)
	}
	execPath := b.config.Executable
	if cfg.Executable != "" {
		execPath = cfg.Executable
	}
	if execPath == "" {
		execPath = "openclaw"
	}
	if err := checkVersion(ctx, execPath); err != nil {
		return nil, err
	}
	sessionID := opts.ResumeSessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("orrery-%d", time.Now().UnixNano())
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	return startOpenClawSession(ctx, execPath, BuildArgs(prompt, sessionID, opts), opts.Cwd, cliinternal.MergeEnv(cfg.Env, opts.Environment), timeout, cfg.ApprovalBroker)
}

func startOpenClawSession(ctx context.Context, execPath string, args []string, cwd string, env map[string]string, timeout time.Duration, broker runtimecore.ApprovalBroker) (*runtimecore.RuntimeSession, error) {
	if _, err := exec.LookPath(execPath); err != nil {
		return nil, fmt.Errorf("openclaw executable not found at %q: %w", execPath, err)
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	cmd := exec.CommandContext(runCtx, execPath, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = process.BuildEnv(env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("openclaw stdout pipe: %w", err)
	}
	stderrTail := process.NewStderrTail(nil, 2048)
	cmd.Stderr = stderrTail
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start openclaw: %w", err)
	}
	return cliinternal.StreamSession(cliinternal.StreamOptions{
		Context:        runCtx,
		Cancel:         cancel,
		Command:        cmd,
		Stdout:         closeStdoutOnCancel(runCtx, stdout),
		StderrTail:     stderrTail,
		Provider:       "openclaw",
		StderrLabel:    "openclaw",
		Timeout:        timeout,
		ApprovalBroker: broker,
		NewParser: func() cliinternal.Parser {
			return NewParser()
		},
	}), nil
}

func closeStdoutOnCancel(ctx context.Context, reader io.ReadCloser) io.Reader {
	go func() {
		<-ctx.Done()
		_ = reader.Close()
	}()
	return reader
}
