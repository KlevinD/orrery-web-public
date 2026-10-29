package pi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

var piPathPattern = regexp.MustCompile(`(?i)(?:\b[a-z]:[\\/][^\s\x00]+|\\\\[^\s\\/]+[\\/][^\s\x00]+|(^|[\s("'=])/(?:[^\s/'"<>]+/)*[^\s'"<>]+)`)
var piSecretFlagPattern = regexp.MustCompile(`(?i)(--?(?:api[-_]?key|token|password|secret|credential)\s*(?:=|\s)\s*)([^\s]+)`)
var piSecretEnvPattern = regexp.MustCompile(`(?i)\b([a-z0-9_]*(?:token|api_key|apikey|secret|password|credential)[a-z0-9_]*=)([^\s]+)`)
var piBearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`)
var piKeyPattern = regexp.MustCompile(`\b(?:sk|rk)-[A-Za-z0-9_-]{20,}\b`)

func sanitizePiText(value string) string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\x00", ""), "\r", "")
	value = piPathPattern.ReplaceAllString(value, "${1}[local-path-redacted]")
	value = piSecretFlagPattern.ReplaceAllString(value, "${1}[credential-redacted]")
	value = piSecretEnvPattern.ReplaceAllString(value, "${1}[credential-redacted]")
	value = piBearerPattern.ReplaceAllString(value, "Bearer [credential-redacted]")
	return piKeyPattern.ReplaceAllString(value, "[credential-redacted]")
}

func sanitizePiDiagnostic(value string) string {
	return boundedPiText(strings.Join(strings.Fields(sanitizePiText(value)), " "), 200)
}

func boundedPiText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	value = value[:limit]
	for len(value) != 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func safePiIdentifier(value, prefix string) string {
	value = strings.TrimSpace(value)
	if value != "" && len(value) <= 160 && !strings.ContainsAny(value, "\x00\r\n/\\") {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	return prefix + "-" + hex.EncodeToString(digest[:16])
}

func unsafePiJSON(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	if len(raw) > 1<<20 || !json.Valid(raw) {
		return true
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	return unsafePiValue("", value)
}

func unsafePiValue(key string, value any) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.TrimSpace(key)))
	if piSensitiveKey(normalized) || normalized == "argv" || normalized == "env" || normalized == "environment" {
		return true
	}
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			if unsafePiValue(childKey, child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if unsafePiValue(key, child) {
				return true
			}
		}
	case string:
		if (strings.Contains(normalized, "path") || normalized == "cwd") && piPathPattern.MatchString(typed) {
			return true
		}
	}
	return false
}

func piSensitiveKey(key string) bool {
	switch key {
	case "authorization", "proxyauthorization", "cookie", "setcookie", "header", "headers", "apikey", "accesstoken", "refreshtoken", "authtoken", "credential", "credentials", "password", "passwd", "secret", "clientsecret":
		return true
	}
	return strings.HasSuffix(key, "password") || strings.HasSuffix(key, "secret") || strings.HasSuffix(key, "credential")
}
