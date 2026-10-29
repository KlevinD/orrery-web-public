package logging

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureObserverSurvivesWithReloadAndDoesNotRecurse(t *testing.T) {
	var console bytes.Buffer
	path := filepath.Join(t.TempDir(), "app.jsonl")
	logger, closeLog, err := NewConfiguredLogger(Config{Level: "info", Console: true, File: path}, &console)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	child := logger.With(String("component", "worker"))
	count := 0
	if !SetObserver(logger, func(ctx context.Context, event Observation) {
		count++
		if len(event.Fields) != 2 || event.Fields[0].Key != "component" {
			t.Errorf("fields = %+v", event.Fields)
		}
		logger.Error(ctx, "capture's own diagnostic")
	}) {
		t.Fatal("logger cannot be observed")
	}
	child.Error(context.Background(), "failed", String("error", "timeout"))
	if count != 1 {
		t.Fatalf("observations = %d; expected one for both sinks", count)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "failed") || !strings.Contains(console.String(), "failed") {
		t.Fatal("original sinks lost log")
	}
	replacement, closeNew, err := NewConfiguredLogger(Config{Level: "info", Console: true}, &console)
	if err != nil {
		t.Fatal(err)
	}
	defer closeNew()
	CarryObserver(logger, replacement)
	replacement.With(String("component", "worker")).Error(context.Background(), "after reload", String("error", "timeout"))
	if count != 2 {
		t.Fatalf("observer lost on reload: %d", count)
	}
}

func TestJournalPrioritiesFollowStructuredLevel(t *testing.T) {
	t.Setenv("JOURNAL_STREAM", "1:2")
	var out bytes.Buffer
	logger, closeLog, err := NewConfiguredLogger(Config{Level: "debug", Console: true}, &out)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLog()
	logger.Info(context.Background(), "message mentioning level=ERROR")
	logger.Warn(context.Background(), "warning")
	logger.Error(context.Background(), "failure")
	logger.Debug(context.Background(), "debug")
	for i, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		prefix := []string{"<6>", "<4>", "<3>", "<7>"}[i]
		if !strings.HasPrefix(line, prefix) {
			t.Errorf("line %q lacks %s", line, prefix)
		}
	}
}

func TestDiagnosticRedactionPreservesUsefulCause(t *testing.T) {
	value := RedactDiagnostic(`database timeout {"password":"has spaces", "api_key":"secret-key"} token=private Authorization: Bearer abc.def Basic YWJjZA== https://user:pass@host/path?signature=hidden#fragment sk-abcdefghijklmnop`, 4096)
	for _, secret := range []string{"has spaces", "secret-key", "private", "abc.def", "YWJjZA==", "user:pass", "signature=hidden", "abcdefghijklmnop"} {
		if strings.Contains(value, secret) {
			t.Errorf("secret %q survived: %s", secret, value)
		}
	}
	if !strings.Contains(value, "database timeout") || !strings.Contains(value, "host/path") {
		t.Fatal(value)
	}
	if len([]rune(RedactDiagnostic(strings.Repeat("界", 100), 7))) != 7 {
		t.Fatal("rune limit not enforced")
	}
}
