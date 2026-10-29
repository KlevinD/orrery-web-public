package claude

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (s *NativeSession) claudeSafeToolInput(input map[string]any) map[string]any {
	value, ok := s.claudeScrubValue("", input)
	if !ok {
		return map[string]any{}
	}
	out, _ := value.(map[string]any)
	return out
}

func (s *NativeSession) claudeScrubValue(key string, value any) (any, bool) {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "env" || normalized == "environment" || normalized == "argv" || normalized == "cwd" || claudeSensitiveKey(normalized) {
		return nil, false
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any)
		for childKey, child := range typed {
			if safe, ok := s.claudeScrubValue(childKey, child); ok {
				out[childKey] = safe
			}
		}
		return out, true
	case []any:
		out := make([]any, 0, len(typed))
		for _, child := range typed {
			if safe, ok := s.claudeScrubValue(key, child); ok {
				out = append(out, safe)
			}
		}
		return out, true
	case string:
		if strings.Contains(normalized, "path") || normalized == "file" || normalized == "filename" {
			if relative, ok := s.claudeWorkspaceRelativePath(typed); ok {
				return relative, true
			}
			if claudeAbsolutePath(typed) {
				return nil, false
			}
		}
		return claudeCanonicalText(typed, 1024*1024), true
	case nil, bool, float64, json.Number:
		return typed, true
	default:
		return nil, false
	}
}

func (s *NativeSession) claudeWorkspaceRelativePath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) && !claudeAbsolutePath(path) {
		clean := filepath.Clean(path)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", false
		}
		return filepath.ToSlash(clean), true
	}
	relative, err := filepath.Rel(s.workspace.Directory, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

func claudeTag(value string) string {
	var out strings.Builder
	for _, char := range strings.TrimSpace(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_' || char == '-' || char == ':' || char == '/' {
			out.WriteRune(char)
		} else {
			out.WriteByte('_')
		}
		if out.Len() >= 120 {
			break
		}
	}
	if out.Len() == 0 {
		return "claude_unknown"
	}
	return out.String()
}

func claudeDiagnosticText(value, fallback string) string {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " "))
	if value == "" {
		value = fallback
	}
	value = claudeCanonicalText(value, 240)
	return value
}

func claudeCanonicalText(value string, limit int) string {
	value = claudePathPattern.ReplaceAllString(value, "[local-path]")
	value = claudeRedactSecrets(value)
	if limit > 0 && len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

var claudePathPattern = regexp.MustCompile(`(?i)(?:[a-z]:[\\/][^\s]+|/(?:[^\s/]+/)+[^\s]+)`)
var claudeSecretFlagPattern = regexp.MustCompile(`(?i)(--?(?:api[-_]?key|token|password|secret|credential)\s*(?:=|\s)\s*)([^\s]+)`)
var claudeSecretEnvPattern = regexp.MustCompile(`(?i)\b([a-z0-9_]*(?:token|api_key|apikey|secret|password|credential)[a-z0-9_]*=)([^\s]+)`)
var claudeURLUserPattern = regexp.MustCompile(`(?i)(https?://)[^/@\s]+@`)

func claudeSensitiveKey(key string) bool {
	compact := strings.NewReplacer("-", "", "_", "", ".", "").Replace(key)
	switch compact {
	case "authorization", "proxyauthorization", "cookie", "setcookie", "header", "headers", "apikey", "accesstoken", "refreshtoken", "authtoken", "credential", "credentials", "password", "passwd", "secret", "clientsecret":
		return true
	}
	return strings.HasSuffix(compact, "password") || strings.HasSuffix(compact, "secret") || strings.HasSuffix(compact, "credential")
}

func claudeRedactSecrets(value string) string {
	value = claudeSecretFlagPattern.ReplaceAllString(value, `${1}[redacted]`)
	value = claudeSecretEnvPattern.ReplaceAllString(value, `${1}[redacted]`)
	return claudeURLUserPattern.ReplaceAllString(value, `${1}[redacted]@`)
}

func claudeAbsolutePath(value string) bool {
	value = strings.TrimSpace(value)
	return filepath.IsAbs(value) || strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\\`) || (len(value) >= 3 && value[1] == ':' && (value[2] == '\\' || value[2] == '/'))
}
