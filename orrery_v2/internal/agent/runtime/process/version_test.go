package process

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDetectVersionReturnsTrimmedVersionOutput(t *testing.T) {
	version, err := DetectVersion(context.Background(), versionExecutable(t))
	if err != nil {
		t.Fatalf("detect version: %v", err)
	}
	if strings.TrimSpace(version) != "runtime version 1.2.3" {
		t.Fatalf("version = %q", version)
	}
}

func versionExecutable(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		path := filepath.Join(t.TempDir(), "version.cmd")
		if err := os.WriteFile(path, []byte("@echo runtime version 1.2.3\r\n"), 0o755); err != nil {
			t.Fatalf("write version helper: %v", err)
		}
		return path
	}
	path := filepath.Join(t.TempDir(), "version")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho runtime version 1.2.3\n"), 0o755); err != nil {
		t.Fatalf("write version helper: %v", err)
	}
	return path
}
