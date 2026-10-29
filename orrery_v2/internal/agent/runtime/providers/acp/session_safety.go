package acp

import (
	"regexp"
	"strings"
)

var acpPathPattern = regexp.MustCompile(`(?i)(?:\b[a-z]:[\\/][^\s\x00]+|\\\\[^\s\\/]+[\\/][^\s\x00]+|(^|[\s("'=])/(?:[^\s/'"<>]+/)*[^\s'"<>]+)`)
var acpSecretFlagPattern = regexp.MustCompile(`(?i)(^|\s)(--?(?:api[-_]?key|token|password|secret|credential)\s*(?:=|\s)\s*)([^\s]+)`)
var acpSecretEnvPattern = regexp.MustCompile(`(?i)\b([a-z0-9_]*(?:token|api_key|apikey|secret|password|credential)[a-z0-9_]*=)([^\s]+)`)
var acpBearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`)
var acpKeyPattern = regexp.MustCompile(`\b(?:sk|rk)-[A-Za-z0-9_-]{20,}\b`)

func sanitizeACPDiagnostic(value string) string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\x00", ""), "\r", "")
	value = acpPathPattern.ReplaceAllString(value, "${1}[local-path-redacted]")
	value = acpSecretFlagPattern.ReplaceAllString(value, "${1}${2}[credential-redacted]")
	value = acpSecretEnvPattern.ReplaceAllString(value, "${1}[credential-redacted]")
	value = acpBearerPattern.ReplaceAllString(value, "Bearer [credential-redacted]")
	value = acpKeyPattern.ReplaceAllString(value, "[credential-redacted]")
	return boundedText(strings.Join(strings.Fields(value), " "), "")
}
