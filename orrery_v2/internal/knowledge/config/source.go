package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/platform"
	platformconfig "orrery_v2/internal/platform/config"
)

type valueSource interface {
	String(string) (string, bool)
	Int(string) (int, bool)
	Bool(string) (bool, bool)
	Duration(string) (time.Duration, bool)
	Lookup(string) (any, bool)
}

func FromSection(section platform.ConfigSection) (Config, error) {
	cfg, err := build(Default(), section)
	if err != nil {
		return Config{}, section.Invalid(err.Error())
	}
	if err := validate(cfg); err != nil {
		return Config{}, section.Invalid(err.Error())
	}
	return cfg, nil
}

func FromPlatformConfig(cfg platformconfig.Config) (Config, error) {
	return FromPlatformConfigWithDefault(cfg, Default())
}

func FromPlatformConfigWithDefault(cfg platformconfig.Config, defaults Config) (Config, error) {
	source := prefixedConfigSource{config: cfg, prefix: "knowledge"}
	out, err := build(defaults, source)
	if err != nil {
		return Config{}, err
	}
	if err := validate(out); err != nil {
		return Config{}, err
	}
	return out, nil
}

func build(defaults Config, source valueSource) (Config, error) {
	cfg := defaults
	cfg.Ingest.SupportedContentTypes = cloneStrings(defaults.Ingest.SupportedContentTypes)
	if value, ok := source.String("root"); ok {
		cfg.Root = strings.TrimSpace(value)
	}
	if value, ok := source.String("vector_backend"); ok {
		cfg.VectorBackend = strings.TrimSpace(value)
	}
	if value, ok := source.Int("default_chunk_policy.max_runes"); ok {
		cfg.DefaultChunkPolicy.MaxRunes = value
	}
	if value, ok := source.Int("default_chunk_policy.overlap_runes"); ok {
		cfg.DefaultChunkPolicy.OverlapRunes = value
	}
	if value, ok := source.Int("default_chunk_policy.min_runes"); ok {
		cfg.DefaultChunkPolicy.MinRunes = value
	}
	if value, ok := source.Bool("default_chunk_policy.preserve_heads"); ok {
		cfg.DefaultChunkPolicy.PreserveHeads = value
	}
	if value, ok := source.String("embedding.default_profile_ref"); ok {
		cfg.Embedding.DefaultProfileRef = strings.TrimSpace(value)
	}
	if value, ok := source.Int("embedding.batch_size"); ok {
		cfg.Embedding.BatchSize = value
	}
	if value, ok := source.Duration("embedding.request_timeout"); ok {
		cfg.Embedding.RequestTimeout = value
	} else if _, exists := source.Lookup("embedding.request_timeout"); exists {
		return Config{}, fmt.Errorf("embedding.request_timeout must be a valid duration")
	}
	if value, ok := source.Int("ingest.max_document_bytes"); ok {
		cfg.Ingest.MaxDocumentBytes = int64(value)
	}
	if values, ok, err := stringSlice(source, "ingest.supported_content_types"); err != nil {
		return Config{}, err
	} else if ok {
		cfg.Ingest.SupportedContentTypes = values
	}
	return cfg, nil
}

func stringSlice(source valueSource, path string) ([]string, bool, error) {
	value, ok := source.Lookup(path)
	if !ok {
		return nil, false, nil
	}
	switch items := value.(type) {
	case []string:
		return cloneStrings(items), true, nil
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, false, fmt.Errorf("%s must contain only strings", path)
			}
			out = append(out, text)
		}
		return out, true, nil
	default:
		return nil, false, fmt.Errorf("%s must be a string list", path)
	}
}

type prefixedConfigSource struct {
	config platformconfig.Config
	prefix string
}

func (s prefixedConfigSource) Lookup(path string) (any, bool) {
	if strings.TrimSpace(s.prefix) == "" {
		return s.config.Lookup(path)
	}
	if path == "" {
		return s.config.Lookup(s.prefix)
	}
	return s.config.Lookup(s.prefix + "." + path)
}

func (s prefixedConfigSource) String(path string) (string, bool) {
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

func (s prefixedConfigSource) Int(path string) (int, bool) {
	value, ok := s.Lookup(path)
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case int32:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (s prefixedConfigSource) Bool(path string) (bool, bool) {
	value, ok := s.Lookup(path)
	if !ok {
		return false, false
	}
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		return parsed, err == nil
	default:
		return false, false
	}
}

func (s prefixedConfigSource) Duration(path string) (time.Duration, bool) {
	raw, ok := s.String(path)
	if !ok {
		return 0, false
	}
	duration, err := time.ParseDuration(raw)
	return duration, err == nil
}
