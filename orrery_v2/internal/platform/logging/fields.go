package logging

import (
	"fmt"
	"log/slog"
	"reflect"
	"strings"
)

type Field = slog.Attr

func String(key, value string) Field {
	return slog.String(key, value)
}

func Int(key string, value int) Field {
	return slog.Int(key, value)
}

func Any(key string, value any) Field {
	return slog.Any(key, value)
}

func redactField(field Field) Field {
	if isSensitiveKey(field.Key) {
		return slog.String(field.Key, "[REDACTED]")
	}
	if field.Value.Kind() == slog.KindGroup {
		attrs := field.Value.Group()
		redacted := make([]slog.Attr, 0, len(attrs))
		for _, attr := range attrs {
			redacted = append(redacted, redactField(attr))
		}
		return slog.Group(field.Key, attrsToAny(redacted)...)
	}
	if field.Value.Kind() == slog.KindAny {
		return slog.Any(field.Key, redactValue(field.Value.Any()))
	}
	return field
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	for _, marker := range []string{
		"secret",
		"token",
		"api_key",
		"apikey",
		"password",
		"authorization",
		"credential",
		"private_key",
		"access_key",
		"refresh",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func redactValue(value any) any {
	if value == nil {
		return nil
	}
	if stringer, ok := value.(fmt.Stringer); ok && strings.Contains(stringer.String(), "[REDACTED]") {
		return "[REDACTED]"
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if isSensitiveKey(key) {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = redactValue(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, redactValue(item))
		}
		return out
	}
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Map && reflected.Type().Key().Kind() == reflect.String {
		out := map[string]any{}
		iter := reflected.MapRange()
		for iter.Next() {
			key := iter.Key().String()
			if isSensitiveKey(key) {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = redactValue(iter.Value().Interface())
		}
		return out
	}
	return value
}

func attrsToAny(attrs []slog.Attr) []any {
	out := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		out = append(out, attr)
	}
	return out
}
