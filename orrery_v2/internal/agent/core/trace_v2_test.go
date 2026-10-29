package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSanitizeTraceValueRedactsSensitiveKeysAndCredentialPatterns(t *testing.T) {
	secret := "configured-secret-value"
	input := map[string]any{
		"nested": map[string]any{
			"api_key":       "api-key-fixture-not-live",
			"authorization": "Bearer bearer-secret-value",
			"jwt":           "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature-value",
			"private":       "-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY-----",
		},
		"exact":   secret,
		"base64":  base64.StdEncoding.EncodeToString([]byte(secret)),
		"escaped": url.QueryEscape(secret),
		"safe":    "release approved",
	}

	got := SanitizeTraceValue(input, TraceSanitizerOptions{ExactSecrets: []string{secret}})
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{
		"api-key-fixture-not-live", "bearer-secret-value", "eyJhbGciOiJIUzI1NiJ9",
		"private-material", secret, base64.StdEncoding.EncodeToString([]byte(secret)),
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("secret %q leaked into trace: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "release approved") || !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("sanitized trace = %s", text)
	}
}

func TestSanitizeTraceValueDoesNotMutateCallerData(t *testing.T) {
	nested := map[string]any{"password": "secret", "safe": "value"}
	input := map[string]any{"nested": nested, "items": []any{"Bearer token-value-123"}}

	_ = SanitizeTraceValue(input, TraceSanitizerOptions{})

	if nested["password"] != "secret" || input["items"].([]any)[0] != "Bearer token-value-123" {
		t.Fatalf("input mutated: %#v", input)
	}
}

func TestSanitizeTraceValueRedactsBeyondDepthLimit(t *testing.T) {
	root := map[string]any{}
	cursor := root
	for index := 0; index < traceSanitizerMaxDepth+2; index++ {
		next := map[string]any{}
		cursor["next"] = next
		cursor = next
	}
	cursor["value"] = "must-not-survive"

	encoded, err := json.Marshal(SanitizeTraceValue(root, TraceSanitizerOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "must-not-survive") {
		t.Fatalf("over-depth value survived: %s", encoded)
	}
}

func TestSanitizeTraceValuePreservesTokenUsageCounts(t *testing.T) {
	got := SanitizeTraceValue(map[string]any{
		"input_tokens": 12, "output_tokens": 3, "total_tokens": 15,
		"access_token": "private-access-token",
	}, TraceSanitizerOptions{})
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"input_tokens":12`) || !strings.Contains(text, `"total_tokens":15`) {
		t.Fatalf("usage counts were redacted: %s", text)
	}
	if strings.Contains(text, "private-access-token") {
		t.Fatalf("access token leaked: %s", text)
	}
}

func TestDurableTraceContractsExposeRequestLifecycleRuntimeAndTerminal(t *testing.T) {
	var recorder DurableTraceRecorder
	var session DurableTraceSession
	_ = recorder
	_ = session

	terminal := TraceTerminal{
		Status:       RuntimeStatusFailed,
		ErrorCode:    "runtime_failed",
		ErrorMessage: "provider unavailable",
		CompletedAt:  time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC),
	}
	if terminal.Status != RuntimeStatusFailed || terminal.ErrorCode != "runtime_failed" {
		t.Fatal(terminal)
	}

	var _ interface {
		RecordRequest(context.Context, RuntimeRequest) error
		RecordLifecycle(context.Context, TraceLifecycleEvent) error
		RecordRuntime(context.Context, SessionEnvelope) error
		Finish(context.Context, TraceTerminal) error
	} = session
}
