package improvement

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DockerTaskRunner runs untrusted candidates with no network, host credentials,
// Docker socket or writable source mount. WorkRoot must be an operator-owned
// project temp directory; Docker images are preloaded and pinned, never pulled.
type DockerTaskRunner struct{ WorkRoot string }

type boundedOutput struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func dockerCreateArgs(root string, p ReplayPolicy) []string {
	args := []string{"create", "--interactive", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=64", "--memory=" + strconv.Itoa(p.MemoryMB) + "m", "--memory-swap=" + strconv.Itoa(p.MemoryMB) + "m", "--cpus=" + strconv.FormatFloat(p.CPUs, 'f', -1, 64), "--user=65534:65534", "--tmpfs=/tmp:rw,exec,nosuid,nodev,size=256m,mode=1777", "--workdir=/src", "--mount=type=bind,src=" + root + ",dst=/src,readonly", "--env=HOME=/tmp", "--env=TMPDIR=/tmp", "--env=GOCACHE=/tmp/go-cache", "--env=GOPATH=/tmp/go-path", "--env=GOPROXY=off", "--env=GOTOOLCHAIN=local", "--env=GOMAXPROCS=2", "--entrypoint=" + p.Entrypoint, p.Image}
	return append(args, p.Args...)
}

func (r *DockerTaskRunner) Run(ctx context.Context, sources []SourceFile, policy ReplayPolicy, task ReplayTask) (result TaskExecution, returnedErr error) {
	if r == nil || !filepath.IsAbs(r.WorkRoot) || strings.ContainsAny(r.WorkRoot, ",\n\r") {
		return result, ErrInvalid
	}
	if err := validateSources(sources); err != nil {
		return result, err
	}
	if _, err := os.Stat(r.WorkRoot); err != nil {
		return result, fmt.Errorf("%w: project experiment temp root is unavailable", ErrUnavailable)
	}
	root, err := os.MkdirTemp(r.WorkRoot, "task-replay-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0755); err != nil {
		return result, err
	}
	for _, source := range sources {
		name := filepath.Join(root, filepath.FromSlash(source.Path))
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			return result, err
		}
		if err := os.WriteFile(name, []byte(source.Content), 0444); err != nil {
			return result, err
		}
	}
	// A missing daemon/image is an infrastructure error, never a candidate fail.
	createCtx, createCancel := context.WithTimeout(ctx, 15*time.Second)
	rawID, err := exec.CommandContext(createCtx, "docker", dockerCreateArgs(root, policy)...).Output()
	createCancel()
	if err != nil {
		return result, fmt.Errorf("%w: create isolated task container failed", ErrUnavailable)
	}
	id := strings.TrimSpace(string(rawID))
	if !hashOK(id) {
		return result, fmt.Errorf("%w: Docker returned an invalid container receipt", ErrUnavailable)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := exec.CommandContext(cleanupCtx, "docker", "rm", "--force", id).Run(); err != nil {
			returnedErr = fmt.Errorf("%w: isolated task container cleanup failed", ErrUnavailable)
		}
	}()
	runCtx, runCancel := context.WithTimeout(ctx, time.Duration(policy.TimeoutMS)*time.Millisecond)
	defer runCancel()
	stdout, stderr := &boundedOutput{limit: policy.MaxOutputBytes}, &boundedOutput{limit: policy.MaxOutputBytes}
	command := exec.CommandContext(runCtx, "docker", "start", "--attach", "--interactive", id)
	command.Stdin = bytes.NewReader(task.Input)
	command.Stdout, command.Stderr = stdout, stderr
	command.WaitDelay = 2 * time.Second
	started := time.Now()
	runErr := command.Run()
	duration := time.Since(started).Milliseconds()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	timedOut := runCtx.Err() != nil
	if timedOut {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = exec.CommandContext(stopCtx, "docker", "kill", id).Run()
		stopCancel()
	}
	inspectCtx, inspectCancel := context.WithTimeout(ctx, 10*time.Second)
	defer inspectCancel()
	rawCode, inspectErr := exec.CommandContext(inspectCtx, "docker", "inspect", "--format", "{{.State.Status}} {{.State.ExitCode}}", id).Output()
	parts := strings.Fields(string(rawCode))
	if inspectErr != nil || len(parts) != 2 || parts[0] != "exited" {
		return result, fmt.Errorf("%w: isolated execution has no terminal container receipt", ErrUnavailable)
	}
	exitCode, err := strconv.Atoi(parts[1])
	if err != nil {
		return result, ErrUnavailable
	}
	if runErr != nil && !timedOut && exitCode == 0 {
		return result, fmt.Errorf("%w: task output transport failed", ErrUnavailable)
	}
	inputHash, err := canonicalJSON(task.Input)
	if err != nil {
		return result, err
	}
	result = TaskExecution{TaskID: task.ID, InputHash: inputHash, SourceHash: Digest(sources), PolicyHash: Digest(policy), ContainerID: id, Output: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode, DurationMS: duration, TimedOut: timedOut, Truncated: stdout.truncated || stderr.truncated}
	return result, nil
}

var _ io.Writer = (*boundedOutput)(nil)
