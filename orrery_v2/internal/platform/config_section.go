package platform

import (
	"fmt"
	"time"

	"orrery_v2/internal/platform/config"
)

type ConfigSection struct {
	name   string
	config config.Config
}

func (s ConfigSection) Decode(target any) error {
	if s.name == "" {
		return fmt.Errorf("config section name is required")
	}
	return s.config.DecodeSection(s.name, target)
}

func (s ConfigSection) Invalid(message string) error {
	return config.SectionValidationError(s.name, message)
}

func (s ConfigSection) Lookup(path string) (any, bool) {
	if s.name == "" {
		return nil, false
	}
	if path == "" {
		return s.config.Lookup(s.name)
	}
	return s.config.Lookup(s.name + "." + path)
}

func (s ConfigSection) String(path string) (string, bool) {
	value, ok := s.Lookup(path)
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

func (s ConfigSection) Int(path string) (int, bool) {
	value, ok := s.Lookup(path)
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
	default:
		return 0, false
	}
}

func (s ConfigSection) Bool(path string) (bool, bool) {
	value, ok := s.Lookup(path)
	if !ok {
		return false, false
	}
	v, ok := value.(bool)
	return v, ok
}

func (s ConfigSection) Duration(path string) (time.Duration, bool) {
	raw, ok := s.String(path)
	if !ok {
		return 0, false
	}
	duration, err := time.ParseDuration(raw)
	return duration, err == nil
}

type ConfigValidator func(ConfigSection) error
