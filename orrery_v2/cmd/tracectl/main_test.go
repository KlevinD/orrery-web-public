package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTraceAuditReportsRulesWithoutSecretValuesAndIsReadOnly(t *testing.T) {
	root := t.TempDir()
	writeAuditFixture(t, root, "agent-runs/run-valid/runtime-trace.json", `{"run_id":"run-valid","messages":[]}`, 0o600)
	writeAuditFixture(t, root, "agent-runs/run-invalid/runtime-trace.json", `{invalid`, 0o600)
	writeAuditFixture(t, root, "agent-runs/run-missing/trace-v2/runtime-trace.json", `{"trace_version":2,"run_id":"run-missing"}`, 0o600)
	writeAuditFixture(t, root, "agent-runs/run-secret/runtime-trace.json", `{"run_id":"run-secret","api_key":"super-secret-value"}`, 0o644)
	writeAuditFixture(t, root, "agent-runs/run-bearer/runtime-trace.json", `{"run_id":"run-bearer","message":"Bearer synthetic-token-value"}`, 0o600)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"audit", "--root", root, "--format", "json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	output := stdout.String()
	for _, rule := range []string{"invalid_json", "missing_terminal", "sensitive_key", "credential_pattern", "insecure_mode"} {
		if !strings.Contains(output, `"rule":"`+rule+`"`) {
			t.Fatalf("missing rule %s: %s", rule, output)
		}
	}
	for _, secret := range []string{"super-secret-value", "synthetic-token-value"} {
		if strings.Contains(output, secret) {
			t.Fatalf("secret leaked in report: %s", output)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"audit", "--root", root, "--apply"}, &stdout, &stderr); code == 0 {
		t.Fatal("audit accepted mutating flag")
	}
}

func writeAuditFixture(t *testing.T, root, relative, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
