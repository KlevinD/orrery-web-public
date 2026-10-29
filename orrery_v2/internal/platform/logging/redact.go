package logging

import (
	"regexp"
	"strings"
)

var diagnosticSecrets = regexp.MustCompile(`(?i)(["']?(?:password|passwd|token|access_token|refresh_token|secret|api[_-]?key|authorization|cookie|credential)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)`)
var diagnosticBearer = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[a-z0-9._~+/=-]+`)
var diagnosticURL = regexp.MustCompile(`(?i)(https?://)[^\s]+`)
var diagnosticAPIKey = regexp.MustCompile(`\bsk-[a-zA-Z0-9_-]{12,}`)

// RedactDiagnostic keeps a bounded diagnostic; request bodies and headers
// must never be supplied to it. URL credentials, queries and fragments are removed.
func RedactDiagnostic(value string, limit int) string {
	value = diagnosticBearer.ReplaceAllString(value, "Bearer [REDACTED]")
	value = diagnosticSecrets.ReplaceAllString(value, "${1}[REDACTED]")
	value = diagnosticAPIKey.ReplaceAllString(value, "[REDACTED]")
	value = diagnosticURL.ReplaceAllStringFunc(value, func(s string) string {
		if i := strings.IndexAny(s, "?#"); i >= 0 {
			s = s[:i] + "?[REDACTED]"
		}
		start := strings.Index(s, "://") + 3
		if i := strings.Index(s[start:], "@"); i >= 0 {
			s = s[:start] + "[REDACTED]@" + s[start+i+1:]
		}
		return s
	})
	r := []rune(strings.TrimSpace(value))
	if limit > 0 && len(r) > limit {
		r = r[:limit]
	}
	return string(r)
}
