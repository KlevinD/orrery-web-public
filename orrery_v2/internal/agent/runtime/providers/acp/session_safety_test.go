package acp

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeACPDiagnosticRedactsPathsAndCredentials(t *testing.T) {
	input := `failed in C:\Users\operator\workspace --token=top-secret ACCESS_TOKEN=env-secret Bearer abcdefghijklmnop sk-abcdefghijklmnopqrstuvwxyz`
	got := sanitizeACPDiagnostic(input)
	for _, secret := range []string{`C:\Users\operator\workspace`, "top-secret", "env-secret", "abcdefghijklmnop", "sk-abcdefghijklmnopqrstuvwxyz"} {
		if strings.Contains(got, secret) {
			t.Fatalf("sanitizeACPDiagnostic leaked %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, "[local-path-redacted]") || !strings.Contains(got, "[credential-redacted]") {
		t.Fatalf("sanitizeACPDiagnostic = %q", got)
	}
}

func TestRedactValueRemovesAbsolutePathsAndSecretFields(t *testing.T) {
	value := redactValue("", map[string]any{
		"file":   `/var/lib/orrery/private.txt`,
		"nested": []any{`C:\Users\operator\secret.txt`, map[string]any{"api_token": "secret"}},
	}).(map[string]any)
	if value["file"] != "[redacted-path]" {
		t.Fatalf("file = %#v", value["file"])
	}
	nested := value["nested"].([]any)
	if nested[0] != "[redacted-path]" || nested[1].(map[string]any)["api_token"] != "[redacted]" {
		t.Fatalf("nested = %#v", nested)
	}
}

func TestBoundedTextKeepsValidUTF8(t *testing.T) {
	got := boundedText(strings.Repeat("a", 239)+"界", "")
	if len(got) > 240 || !utf8.ValidString(got) {
		t.Fatalf("boundedText returned invalid UTF-8: %q", got)
	}
}

func TestConnectionCapabilitiesUseConfiguredPayloadLimit(t *testing.T) {
	connection, err := NewConnection(Config{MaxRecordBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if got := connection.Capabilities(context.Background()).PayloadLimitBytes; got != 4096 {
		t.Fatalf("PayloadLimitBytes = %d", got)
	}
}
