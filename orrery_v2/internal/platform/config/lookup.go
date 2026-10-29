package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func (c Config) Lookup(path string) (any, bool) {
	if path == "" {
		return nil, false
	}
	var current any = c.raw
	for _, part := range strings.Split(path, ".") {
		node, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = node[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func (c Config) String(path string) (string, bool) {
	value, ok := c.Lookup(path)
	if !ok || value == nil {
		return "", false
	}
	switch v := value.(type) {
	case string:
		return v, true
	default:
		return fmt.Sprint(v), true
	}
}

func (c Config) Duration(path string) (time.Duration, bool) {
	value, ok := c.String(path)
	if !ok {
		return 0, false
	}
	duration, err := time.ParseDuration(value)
	return duration, err == nil
}

func (c Config) Int(path string) (int, bool) {
	value, ok := c.Lookup(path)
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		parsed, err := strconv.Atoi(v)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (c Config) Bool(path string) (bool, bool) {
	value, ok := c.Lookup(path)
	if !ok {
		return false, false
	}
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		parsed, err := strconv.ParseBool(v)
		return parsed, err == nil
	default:
		return false, false
	}
}

func (c Config) DatabaseURLFor(role string) (string, bool) {
	value, ok := c.DatabaseURLs[role]
	return value, ok && value != ""
}

func (c Config) DecodeSection(path string, target any) error {
	value, ok := c.Lookup(path)
	if !ok {
		return fmt.Errorf("config section %q not found", path)
	}
	encoded, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode config section %q: %w", path, err)
	}
	if err := yaml.Unmarshal(encoded, target); err != nil {
		return fmt.Errorf("decode config section %q: %w", path, err)
	}
	return nil
}
