package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/secret"
)

func TestLoggerIncludesStructuredContextFields(t *testing.T) {
	var out bytes.Buffer
	logger := logging.NewJSONLogger(&out, logging.LevelDebug).With(logging.String("component", "stage0"))
	ctx := logging.WithCorrelationID(context.Background(), "corr-123")

	logger.Info(ctx, "ready", logging.String("operation", "boot"), logging.Int("duration_ms", 12))

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &record); err != nil {
		t.Fatalf("decode log json: %v\n%s", err, out.String())
	}
	if record["level"] != "INFO" {
		t.Fatalf("level = %v, want INFO", record["level"])
	}
	if record["msg"] != "ready" {
		t.Fatalf("msg = %v, want ready", record["msg"])
	}
	if record["component"] != "stage0" {
		t.Fatalf("component = %v, want stage0", record["component"])
	}
	if record["correlation_id"] != "corr-123" {
		t.Fatalf("correlation_id = %v, want corr-123", record["correlation_id"])
	}
	if record["operation"] != "boot" {
		t.Fatalf("operation = %v, want boot", record["operation"])
	}
	if record["duration_ms"] != float64(12) {
		t.Fatalf("duration_ms = %v, want 12", record["duration_ms"])
	}
}

func TestLoggerRedactsSecretLikeFields(t *testing.T) {
	var out bytes.Buffer
	logger := logging.NewJSONLogger(&out, logging.LevelDebug)

	logger.Debug(context.Background(), "credentials", logging.String("api_key", "sk-secret"), logging.String("token", "tok-secret"))

	got := out.String()
	if strings.Contains(got, "sk-secret") || strings.Contains(got, "tok-secret") {
		t.Fatalf("log leaked secret values: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("log did not include redaction marker: %s", got)
	}
}

func TestLoggerRedactsSensitiveNestedValues(t *testing.T) {
	var out bytes.Buffer
	logger := logging.NewJSONLogger(&out, logging.LevelDebug)

	logger.Info(context.Background(), "request",
		logging.String("authorization", "Bearer real-token"),
		logging.Any("oauth_credential", map[string]any{
			"client_id":     "public-client",
			"client_secret": "client-secret",
		}),
		logging.Any("payload", map[string]any{
			"safe": "visible",
			"headers": map[string]any{
				"Authorization": "Bearer nested-token",
			},
			"items": []any{
				map[string]any{"webhook_secret": "nested-secret"},
			},
		}),
		logging.Any("secret_value", secret.NewValue("plain-secret")),
	)

	got := out.String()
	for _, leaked := range []string{"real-token", "client-secret", "nested-token", "nested-secret", "plain-secret"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("log leaked sensitive value %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "visible") {
		t.Fatalf("log redacted non-sensitive values unexpectedly: %s", got)
	}
	if strings.Count(got, "[REDACTED]") < 4 {
		t.Fatalf("log did not redact all sensitive values: %s", got)
	}
}

func TestLoggerWritesWarnAndErrorLevels(t *testing.T) {
	var out bytes.Buffer
	logger := logging.NewJSONLogger(&out, logging.LevelDebug)

	logger.Warn(context.Background(), "recoverable", logging.String("error_code", "dependency_unavailable"))
	logger.Error(context.Background(), "failed", logging.String("error_code", "internal_error"))

	got := out.String()
	if !strings.Contains(got, `"level":"WARN"`) || !strings.Contains(got, `"level":"ERROR"`) {
		t.Fatalf("warn/error levels missing from logs: %s", got)
	}
	if !strings.Contains(got, "dependency_unavailable") || !strings.Contains(got, "internal_error") {
		t.Fatalf("error_code fields missing from logs: %s", got)
	}
}

func TestTextLoggerSupportsDeveloperReadableOutput(t *testing.T) {
	var out bytes.Buffer
	logger := logging.NewTextLogger(&out, logging.LevelDebug).With(logging.String("component", "stage0"))

	logger.Info(context.Background(), "started", logging.String("operation", "dev-log"))

	got := out.String()
	if !strings.Contains(got, "started") || !strings.Contains(got, "component=stage0") || !strings.Contains(got, "operation=dev-log") {
		t.Fatalf("text log missing readable fields: %s", got)
	}
}

func TestConsoleAndFileLoggerWritesReadableConsoleAndStructuredFile(t *testing.T) {
	var console bytes.Buffer
	var file bytes.Buffer
	logger := logging.NewConsoleAndFileLogger(&console, &file, logging.LevelDebug).With(logging.String("component", "stage0"))
	ctx := logging.WithCorrelationID(context.Background(), "corr-console-file")

	logger.Info(ctx, "booted", logging.String("operation", "startup"))

	consoleOut := console.String()
	if !strings.Contains(consoleOut, "booted") ||
		!strings.Contains(consoleOut, "component=stage0") ||
		!strings.Contains(consoleOut, "operation=startup") ||
		!strings.Contains(consoleOut, "correlation_id=corr-console-file") {
		t.Fatalf("console log missing readable fields: %s", consoleOut)
	}

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(file.Bytes()), &record); err != nil {
		t.Fatalf("decode file log json: %v\n%s", err, file.String())
	}
	if record["msg"] != "booted" ||
		record["component"] != "stage0" ||
		record["operation"] != "startup" ||
		record["correlation_id"] != "corr-console-file" {
		t.Fatalf("file log missing structured fields: %+v", record)
	}
}

func TestFileLoggerCreatesParentDirectoryAndAppendsJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "platform", "stage0.jsonl")
	logger, closeLog, err := logging.NewFileLogger(path, logging.LevelDebug)
	if err != nil {
		t.Fatalf("NewFileLogger returned error: %v", err)
	}

	logger.Warn(context.Background(), "recovering", logging.String("operation", "fallback"), logging.String("token", "must-not-leak"))
	if err := closeLog(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &record); err != nil {
		t.Fatalf("decode log file json: %v\n%s", err, string(raw))
	}
	if record["level"] != "WARN" || record["msg"] != "recovering" || record["operation"] != "fallback" {
		t.Fatalf("log record = %+v", record)
	}
	if record["token"] != "[REDACTED]" {
		t.Fatalf("token was not redacted in file log: %+v", record)
	}
}

func TestNewConfiguredLoggerBuildsConsoleAndFileSinks(t *testing.T) {
	var console bytes.Buffer
	path := filepath.Join(t.TempDir(), "logs", "platform.jsonl")
	logger, closeLog, err := logging.NewConfiguredLogger(logging.Config{
		Level:   "warn",
		Console: true,
		File:    path,
	}, &console)
	if err != nil {
		t.Fatalf("NewConfiguredLogger returned error: %v", err)
	}
	defer closeLog()

	logger.Info(context.Background(), "hidden", logging.String("operation", "below-level"))
	logger.Warn(context.Background(), "visible", logging.String("operation", "configured"))
	if err := closeLog(); err != nil {
		t.Fatalf("close log: %v", err)
	}

	if strings.Contains(console.String(), "hidden") || !strings.Contains(console.String(), "visible") || !strings.Contains(console.String(), "operation=configured") {
		t.Fatalf("console output did not honor configured level/sink: %s", console.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read configured log file: %v", err)
	}
	if strings.Contains(string(raw), "hidden") {
		t.Fatalf("file output included below-level log: %s", string(raw))
	}
	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &record); err != nil {
		t.Fatalf("decode configured log file: %v\n%s", err, string(raw))
	}
	if record["level"] != "WARN" || record["operation"] != "configured" {
		t.Fatalf("configured log record = %+v", record)
	}
}

func TestNewConfiguredLoggerRejectsDisabledSinks(t *testing.T) {
	_, _, err := logging.NewConfiguredLogger(logging.Config{Level: "info"}, &bytes.Buffer{})
	if err == nil {
		t.Fatalf("NewConfiguredLogger with no sinks returned nil error")
	}
}
