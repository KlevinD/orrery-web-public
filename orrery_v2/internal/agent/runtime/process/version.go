package process

import (
	"context"
	"strings"
	"time"
)

func DetectVersion(ctx context.Context, executable string) (string, error) {
	return DetectVersionWithEnvironment(ctx, executable, nil)
}

func DetectVersionWithEnvironment(ctx context.Context, executable string, environment map[string]string) (string, error) {
	return DetectVersionWithEnvironmentTimeout(ctx, executable, environment, 10*time.Second)
}

func DetectVersionWithEnvironmentTimeout(ctx context.Context, executable string, environment map[string]string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	runner := NewRunner(RunnerOptions{StderrTailBytes: 1024})
	result, err := runner.Run(ctx, Command{
		Executable: executable,
		Args:       []string{"--version"},
		Env:        environment,
		Timeout:    timeout,
	})
	if err != nil {
		return "", err
	}
	if result.Status != StatusCompleted {
		if result.StderrTail != "" {
			return "", &VersionError{Executable: executable, Message: result.StderrTail}
		}
		return "", &VersionError{Executable: executable, Message: result.Error}
	}
	return strings.TrimSpace(result.Stdout), nil
}

type VersionError struct {
	Executable string
	Message    string
}

func (e *VersionError) Error() string {
	if e.Message == "" {
		return "detect version failed for " + e.Executable
	}
	return "detect version failed for " + e.Executable + ": " + e.Message
}
