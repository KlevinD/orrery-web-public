package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	TraceVersion2          = 2
	traceSanitizerMaxDepth = 64
	traceRedactedValue     = "[REDACTED]"
)

type DurableTraceRecorder interface {
	BeginTrace(context.Context, AgentInput) (DurableTraceSession, error)
}

// RuntimeAttemptTraceSession provides host-generated execution identity.
type RuntimeAttemptTraceSession interface{ RuntimeAttemptID() string }

type DurableTraceSession interface {
	Ref() string
	RecordLifecycle(context.Context, TraceLifecycleEvent) error
	RecordRequest(context.Context, RuntimeRequest) error
	RecordRuntime(context.Context, SessionEnvelope) error
	Finish(context.Context, TraceTerminal) error
}

type TraceLifecycleEvent struct {
	Stage    string            `json:"stage"`
	Status   string            `json:"status,omitempty"`
	At       time.Time         `json:"at"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type TraceTerminal struct {
	Status       RuntimeStatus   `json:"status"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	Retryable    bool            `json:"retryable,omitempty"`
	Partial      bool            `json:"partial_output,omitempty"`
	Response     RuntimeResponse `json:"response,omitempty"`
	Result       AgentResult     `json:"result,omitempty"`
	CompletedAt  time.Time       `json:"completed_at"`
}

type TraceIndexRecord struct {
	TenantID       string
	RunID          string
	TraceRef       string
	TerminalStatus string
	TraceVersion   int
	EventCount     int
	ByteCount      int64
	StartedAt      time.Time
	LatestEventAt  time.Time
	CompletedAt    time.Time
	RetainUntil    time.Time
}

type TraceIndexSink interface {
	SaveTraceIndex(context.Context, TraceIndexRecord) error
}

type TraceSanitizerOptions struct {
	ExactSecrets []string
}

type TracePersistenceError struct {
	Stage string
	Err   error
}

func (e TracePersistenceError) Error() string {
	if e.Err == nil {
		return "durable trace persistence failed at " + strings.TrimSpace(e.Stage)
	}
	return fmt.Sprintf("durable trace persistence failed at %s: %v", strings.TrimSpace(e.Stage), e.Err)
}

func (e TracePersistenceError) Unwrap() error { return e.Err }

var traceCredentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`\beyJ[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\b`),
	regexp.MustCompile(`(?s)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?-----END [^-\r\n]*PRIVATE KEY-----`),
	regexp.MustCompile(`\bsk-[a-zA-Z0-9][a-zA-Z0-9._-]{7,}\b`),
	regexp.MustCompile(`\bgh[pousr]_[a-zA-Z0-9]{12,}\b`),
	regexp.MustCompile(`\bxox[a-zA-Z]-[a-zA-Z0-9-]{8,}\b`),
}

// SanitizeTraceValue returns a detached JSON-compatible value and fails closed
// when the input cannot be represented safely.
func SanitizeTraceValue(value any, options TraceSanitizerOptions) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return traceRedactedValue
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var detached any
	if err := decoder.Decode(&detached); err != nil {
		return traceRedactedValue
	}
	return sanitizeTraceNode(detached, traceSecretVariants(options.ExactSecrets), 0)
}

func sanitizeTraceNode(value any, secrets []string, depth int) any {
	if depth >= traceSanitizerMaxDepth {
		return traceRedactedValue
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSensitiveTraceKey(key) {
				out[key] = traceRedactedValue
				continue
			}
			out[key] = sanitizeTraceNode(item, secrets, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = sanitizeTraceNode(item, secrets, depth+1)
		}
		return out
	case string:
		return sanitizeTraceString(typed, secrets)
	default:
		return typed
	}
}

func isSensitiveTraceKey(key string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
	for _, marker := range []string{
		"secret", "password", "passwd", "apikey", "authorization",
		"credential", "cookie", "privatekey", "accesskey", "refreshtoken", "jwt",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	if strings.Contains(normalized, "token") && !isTraceTokenMetricKey(normalized) {
		return true
	}
	return false
}

func isTraceTokenMetricKey(normalized string) bool {
	switch normalized {
	case "inputtokens", "outputtokens", "totaltokens", "cachereadtokens", "cachewritetokens",
		"reasoningtokens", "cachedtokens", "cachedinputtokens", "cachedinputtokensknown", "reasoningtokensknown", "maxtokens", "maxoutputtokens", "contexttokens",
		"contexttokenlimit", "prompttokenbudget", "tokenconsumed", "evaluationtokenconsumed", "tokenvalueper10k":
		return true
	default:
		return false
	}
}

func sanitizeTraceString(value string, secrets []string) string {
	for _, secret := range secrets {
		value = strings.ReplaceAll(value, secret, traceRedactedValue)
	}
	for _, pattern := range traceCredentialPatterns {
		value = pattern.ReplaceAllString(value, traceRedactedValue)
	}
	return value
}

func traceSecretVariants(secrets []string) []string {
	seen := make(map[string]struct{}, len(secrets)*5)
	variants := make([]string, 0, len(secrets)*5)
	add := func(value string) {
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		variants = append(variants, value)
	}
	for _, secret := range secrets {
		if strings.TrimSpace(secret) == "" {
			continue
		}
		add(secret)
		add(url.QueryEscape(secret))
		add(base64.StdEncoding.EncodeToString([]byte(secret)))
		add(base64.RawStdEncoding.EncodeToString([]byte(secret)))
		add(base64.URLEncoding.EncodeToString([]byte(secret)))
		add(base64.RawURLEncoding.EncodeToString([]byte(secret)))
	}
	return variants
}
