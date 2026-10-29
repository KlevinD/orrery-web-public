package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	runtimeprocess "orrery_v2/internal/agent/runtime/process"
	mcptransport "orrery_v2/internal/transport/mcp"
)

const (
	defaultPersonalAssistantShellTimeout = 30 * time.Second
	maxPersonalAssistantShellTimeout     = 2 * time.Minute
	defaultPersonalAssistantOutputLimit  = 64 * 1024
)

type personalAssistantShellExecutor interface {
	ExecutePersonalAssistantShell(ctx context.Context, toolID string, req mcptransport.PersonalAssistantShellRequest) (mcptransport.PersonalAssistantShellResult, error)
}

type personalAssistantShellRequest = mcptransport.PersonalAssistantShellRequest

type personalAssistantShellResult = mcptransport.PersonalAssistantShellResult

// TEMPORARY(personal-assistant-shell): this executor is only a bridge for the
// personal assistant experience until the full Agent runtime runs inside the
// DinD sandbox. Do not extend this into the final group-Agent shell model.
type productionPersonalAssistantShellExecutor struct{}

func (productionPersonalAssistantShellExecutor) ExecutePersonalAssistantShell(ctx context.Context, _ string, req mcptransport.PersonalAssistantShellRequest) (mcptransport.PersonalAssistantShellResult, error) {
	if err := ctx.Err(); err != nil {
		return mcptransport.PersonalAssistantShellResult{}, err
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return mcptransport.PersonalAssistantShellResult{}, fmt.Errorf("personal assistant shell command is required")
	}
	root, cwd, err := resolvePersonalAssistantShellCwd(req)
	if err != nil {
		return mcptransport.PersonalAssistantShellResult{}, err
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return mcptransport.PersonalAssistantShellResult{}, err
	}
	timeout := personalAssistantShellTimeout(req.TimeoutMs, req.MaximumTimeoutMs)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now().UTC()
	cmd := personalAssistantShellCommand(runCtx, command)
	runtimeprocess.ConfigureProcessTree(cmd)
	cmd.Cancel = func() error { return runtimeprocess.TerminateProcessTree(cmd) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = cwd
	cmd.Env = personalAssistantShellEnv(req.EnvironmentPolicy)
	store, err := newPersonalAssistantOutputStore(root, cwd)
	if err != nil {
		return mcptransport.PersonalAssistantShellResult{}, err
	}
	defer store.root.Close()
	limit := personalAssistantOutputLimit(req.MaxOutputLen)
	stdout := newPersonalAssistantOutputCapture(store, "stdout", limit)
	stderr := newPersonalAssistantOutputCapture(store, "stderr", limit)
	defer stdout.close()
	defer stderr.close()
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err = cmd.Start(); err == nil {
		if attachErr := runtimeprocess.AttachProcessTree(cmd); attachErr != nil {
			_ = runtimeprocess.TerminateProcessTree(cmd)
			_ = cmd.Wait()
			err = attachErr
		} else {
			err = cmd.Wait()
			runtimeprocess.ReleaseProcessTree(cmd)
		}
	}
	durationMs := time.Since(started).Milliseconds()
	exitCode := 0
	if err != nil {
		exitCode = 1
		if runCtx.Err() != nil {
			return mcptransport.PersonalAssistantShellResult{}, runCtx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return mcptransport.PersonalAssistantShellResult{}, err
		}
	}
	output, err := personalAssistantShellOutput(map[string]any{
		"exit_code":   exitCode,
		"cwd":         cwd,
		"workspace":   root,
		"duration_ms": int(durationMs),
	}, stdout, stderr, limit)
	if err != nil {
		return mcptransport.PersonalAssistantShellResult{}, err
	}
	if err := errors.Join(stdout.close(), stderr.close()); err != nil {
		return mcptransport.PersonalAssistantShellResult{}, err
	}
	return mcptransport.PersonalAssistantShellResult{Output: output}, nil
}

func resolvePersonalAssistantShellCwd(req mcptransport.PersonalAssistantShellRequest) (string, string, error) {
	root := strings.TrimSpace(req.WorkspaceRoot)
	if root == "" {
		return "", "", fmt.Errorf("personal assistant shell workspace root is required")
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return "", "", fmt.Errorf("personal assistant shell workspace root must be absolute")
	}
	base := strings.TrimSpace(req.WorkingDirectory)
	if base == "" {
		base = root
	}
	base = filepath.Clean(base)
	if !filepath.IsAbs(base) {
		base = filepath.Join(root, base)
	}
	if !pathWithin(root, base) {
		return "", "", fmt.Errorf("personal assistant shell working directory escaped workspace")
	}
	cwd := base
	if requested := strings.TrimSpace(req.Cwd); requested != "" && requested != "." {
		if filepath.IsAbs(requested) {
			cwd = filepath.Clean(requested)
		} else {
			cwd = filepath.Join(base, requested)
		}
	}
	cwd = filepath.Clean(cwd)
	if !pathWithin(root, cwd) {
		return "", "", fmt.Errorf("personal assistant shell cwd escaped workspace")
	}
	return root, cwd, nil
}

func personalAssistantShellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func personalAssistantShellEnv(policy string) []string {
	if strings.EqualFold(strings.TrimSpace(policy), "empty") {
		return []string{}
	}
	allowed := []string{"PATH", "SystemRoot", "ComSpec", "PATHEXT", "TMP", "TEMP", "HOME", "USERPROFILE"}
	env := os.Environ()
	out := make([]string, 0, len(allowed))
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		for _, allow := range allowed {
			if strings.EqualFold(key, allow) {
				out = append(out, item)
				break
			}
		}
	}
	return out
}

func personalAssistantShellTimeout(timeoutMs, maximumTimeoutMs int) time.Duration {
	maximum := maxPersonalAssistantShellTimeout
	if maximumTimeoutMs > 0 {
		maximum = time.Duration(maximumTimeoutMs) * time.Millisecond
	}
	if timeoutMs <= 0 {
		return defaultPersonalAssistantShellTimeout
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout > maximum {
		return maximum
	}
	return timeout
}

func personalAssistantOutputLimit(maxOutputLen int) int {
	if maxOutputLen <= 0 || maxOutputLen > defaultPersonalAssistantOutputLimit {
		return defaultPersonalAssistantOutputLimit
	}
	return maxOutputLen
}

func truncatePersonalAssistantOutput(value string, limit int) (string, bool) {
	if limit < 0 || len(value) <= limit {
		return value, false
	}
	// The limit is in UTF-8 bytes. Retreat to the start of the next rune so a
	// preview and a subsequent byte-range read meet at a character boundary.
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit], true
}

func pathWithin(root, child string) bool {
	root = filepath.Clean(root)
	child = filepath.Clean(child)
	rel, err := filepath.Rel(root, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}
