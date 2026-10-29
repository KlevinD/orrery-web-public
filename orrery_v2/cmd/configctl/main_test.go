package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRendersExistingTypedPaths(t *testing.T) {
	input := filepath.Join(t.TempDir(), "input.yaml")
	output := filepath.Join(t.TempDir(), "output.yaml")
	if err := os.WriteFile(input, []byte("authz:\n  mode: local\n  openfga:\n    enforce_ready: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-input", input, "-output", output, "-set", "authz.mode=openfga_enforce", "-set", "authz.openfga.enforce_ready=true"}); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "mode: openfga_enforce") || !strings.Contains(text, "enforce_ready: true") {
		t.Fatalf("rendered config = %s", text)
	}
}

func TestRunRejectsUnknownPath(t *testing.T) {
	input := filepath.Join(t.TempDir(), "input.yaml")
	if err := os.WriteFile(input, []byte("authz:\n  mode: local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"-input", input, "-output", filepath.Join(t.TempDir(), "output.yaml"), "-set", "authz.typo=value"})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("run error = %v", err)
	}
}
