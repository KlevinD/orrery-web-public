package gitworkspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const outputLimit = 256 * 1024

type gitRunner struct {
	path    string
	hooks   string
	timeout time.Duration
}

type commandResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func (g gitRunner) run(ctx context.Context, dir string, env []string, args ...string) (commandResult, error) {
	if g.path == "" {
		return commandResult{}, ErrGitUnavailable
	}
	commandCtx := ctx
	cancel := func() {}
	if g.timeout > 0 {
		commandCtx, cancel = context.WithTimeout(ctx, g.timeout)
	}
	defer cancel()
	gitArgs := make([]string, 0, len(args)+4)
	gitArgs = append(gitArgs, "--no-optional-locks", "-c", "core.hooksPath="+g.hooks)
	gitArgs = append(gitArgs, args...)
	cmd := exec.CommandContext(commandCtx, g.path, gitArgs...)
	cmd.Dir = dir
	cmd.Env = append(sanitizedEnvironment(), env...)
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+nullDevice())
	var stdout, stderr limitedBuffer
	stdout.remaining, stderr.remaining = outputLimit, outputLimit
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := commandResult{stdout: strings.TrimSpace(stdout.String()), stderr: strings.TrimSpace(stderr.String())}
	if stdout.exceeded || stderr.exceeded {
		return result, fmt.Errorf("%w: git output exceeded %d bytes", ErrRepository, outputLimit)
	}
	if err == nil {
		return result, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return result, ErrGitUnavailable
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.exitCode = exitErr.ExitCode()
		return result, fmt.Errorf("%w: git exited %d: %s", ErrRepository, result.exitCode, redactGitError(result.stderr))
	}
	return result, fmt.Errorf("%w: %v", ErrRepository, err)
}

func (g gitRunner) output(ctx context.Context, dir string, args ...string) (string, error) {
	result, err := g.run(ctx, dir, nil, args...)
	return result.stdout, err
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	exceeded  bool
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	if len(p) > w.remaining {
		p = p[:w.remaining]
		w.exceeded = true
	}
	if len(p) > 0 {
		_, _ = w.buffer.Write(p)
		w.remaining -= len(p)
	}
	return original, nil
}

func (w *limitedBuffer) String() string { return w.buffer.String() }

func sanitizedEnvironment() []string {
	allowed := map[string]bool{
		"PATH": true, "PATHEXT": true, "SYSTEMROOT": true, "WINDIR": true,
		"TEMP": true, "TMP": true, "TMPDIR": true,
		"LANG": true, "LC_ALL": true, "TZ": true, "COMSPEC": true,
	}
	out := make([]string, 0, len(allowed))
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if ok && allowed[strings.ToUpper(name)] {
			out = append(out, entry)
		}
	}
	return out
}

func redactGitError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 {
		value = value[:512]
	}
	for _, marker := range []string{"https://", "http://", "ssh://"} {
		for {
			start := strings.Index(strings.ToLower(value), marker)
			if start < 0 {
				break
			}
			end := start
			for end < len(value) && !strings.ContainsRune(" \t\r\n'\"", rune(value[end])) {
				end++
			}
			value = value[:start] + "[REDACTED_REMOTE]" + value[end:]
		}
	}
	return value
}

func parseCommandLine(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n;&|<>`$%") {
		return nil, ErrPolicyViolation
	}
	var args []string
	var current strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			args = append(args, current.String())
			current.Reset()
		}
	}
	for _, r := range raw {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if escaped || quote != 0 {
		return nil, ErrPolicyViolation
	}
	flush()
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, ErrPolicyViolation
	}
	return args, nil
}

func runCheck(ctx context.Context, dir string, raw string, timeout time.Duration, isolatedHome string) (CheckResult, error) {
	args, err := parseCommandLine(raw)
	if err != nil {
		return CheckResult{}, err
	}
	started := time.Now().UTC()
	commandCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		commandCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	cmd := exec.CommandContext(commandCtx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(sanitizedEnvironment(),
		"HOME="+isolatedHome, "USERPROFILE="+isolatedHome,
		"XDG_CONFIG_HOME="+isolatedHome, "XDG_CACHE_HOME="+filepath.Join(isolatedHome, "cache"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+nullDevice(),
	)
	var output limitedBuffer
	output.remaining = outputLimit
	cmd.Stdout, cmd.Stderr = &output, &output
	err = cmd.Run()
	ended := time.Now().UTC()
	result := CheckResult{Command: append([]string(nil), args...), Succeeded: err == nil && !output.exceeded, Summary: strings.TrimSpace(output.String()), StartedAt: started, EndedAt: ended}
	if len(result.Summary) > 4096 {
		result.Summary = result.Summary[:4096]
	}
	if output.exceeded {
		result.ExitCode = -1
		return result, fmt.Errorf("%w: check output exceeded limit", ErrPolicyViolation)
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = -1
	}
	return result, fmt.Errorf("%w: check command failed with exit code %s", ErrPolicyViolation, strconv.Itoa(result.ExitCode))
}

func copyFile(dst string, src io.Reader, mode os.FileMode) error {
	file, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, src)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func nullDevice() string {
	if filepath.Separator == '\\' {
		return "NUL"
	}
	return "/dev/null"
}
