package main

import (
	"fmt"
	"strings"
)

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func firstToolInputString(input map[string]any, names ...string) string {
	for _, name := range names {
		value, ok := input[name]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case string:
			if text := strings.TrimSpace(v); text != "" {
				return text
			}
		case fmt.Stringer:
			if text := strings.TrimSpace(v.String()); text != "" {
				return text
			}
		default:
			if value != nil {
				text := strings.TrimSpace(fmt.Sprint(value))
				if text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func firstToolInputInt(input map[string]any, names ...string) int {
	for _, name := range names {
		value, ok := input[name]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		case float32:
			return int(v)
		default:
			var out int
			if _, err := fmt.Sscan(fmt.Sprint(value), &out); err == nil {
				return out
			}
		}
	}
	return 0
}
