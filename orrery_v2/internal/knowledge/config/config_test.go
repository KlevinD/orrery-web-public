package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	knowledgeconfig "orrery_v2/internal/knowledge/config"
	"orrery_v2/internal/platform"
)

func TestFromSectionAppliesKnowledgeDefaults(t *testing.T) {
	cfg, err := readKnowledgeConfig(t, ``)
	if err != nil {
		t.Fatalf("read default knowledge config: %v", err)
	}
	if cfg.Root != "data/knowledge" || cfg.VectorBackend != "lancedb" {
		t.Fatalf("root/backend defaults mismatch: %#v", cfg)
	}
	if cfg.DefaultChunkPolicy.MaxRunes != 2400 || cfg.DefaultChunkPolicy.OverlapRunes != 240 || cfg.DefaultChunkPolicy.MinRunes != 400 || !cfg.DefaultChunkPolicy.PreserveHeads {
		t.Fatalf("chunk policy defaults mismatch: %#v", cfg.DefaultChunkPolicy)
	}
	if cfg.Embedding.BatchSize != 64 || cfg.Embedding.RequestTimeout != 30*time.Second {
		t.Fatalf("embedding defaults mismatch: %#v", cfg.Embedding)
	}
	if cfg.Ingest.MaxDocumentBytes != 52428800 {
		t.Fatalf("ingest max bytes = %d", cfg.Ingest.MaxDocumentBytes)
	}
	wantTypes := []string{
		"text/plain",
		"text/markdown",
		"text/html",
		"application/pdf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	}
	if len(cfg.Ingest.SupportedContentTypes) != len(wantTypes) {
		t.Fatalf("supported content type count mismatch: %#v", cfg.Ingest.SupportedContentTypes)
	}
	for i, want := range wantTypes {
		if cfg.Ingest.SupportedContentTypes[i] != want {
			t.Fatalf("supported content type %d = %q, want %q", i, cfg.Ingest.SupportedContentTypes[i], want)
		}
	}
}

func TestFromSectionDecodesKnowledgeYAML(t *testing.T) {
	root := filepath.Join(t.TempDir(), "knowledge")
	cfg, err := readKnowledgeConfig(t, `
knowledge:
  root: `+root+`
  vector_backend: lancedb
  default_chunk_policy:
    max_runes: 1000
    overlap_runes: 100
    min_runes: 50
    preserve_heads: false
  embedding:
    default_profile_ref: embed-main
    batch_size: 12
    request_timeout: 9s
  ingest:
    max_document_bytes: 1024
    supported_content_types:
      - text/plain
`)
	if err != nil {
		t.Fatalf("read custom knowledge config: %v", err)
	}
	if cfg.Root != root || cfg.Embedding.DefaultProfileRef != "embed-main" || cfg.Embedding.RequestTimeout != 9*time.Second {
		t.Fatalf("custom config mismatch: %#v", cfg)
	}
	if cfg.DefaultChunkPolicy.MaxRunes != 1000 || cfg.DefaultChunkPolicy.OverlapRunes != 100 || cfg.DefaultChunkPolicy.MinRunes != 50 || cfg.DefaultChunkPolicy.PreserveHeads {
		t.Fatalf("custom chunk policy mismatch: %#v", cfg.DefaultChunkPolicy)
	}
	if cfg.Ingest.MaxDocumentBytes != 1024 || len(cfg.Ingest.SupportedContentTypes) != 1 || cfg.Ingest.SupportedContentTypes[0] != "text/plain" {
		t.Fatalf("custom ingest mismatch: %#v", cfg.Ingest)
	}
}

func TestFromSectionRejectsInvalidKnowledgeConfig(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{name: "backend", yaml: "knowledge:\n  vector_backend: qdrant\n"},
		{name: "chunk policy", yaml: "knowledge:\n  default_chunk_policy:\n    max_runes: 10\n    overlap_runes: 10\n"},
		{name: "batch size", yaml: "knowledge:\n  embedding:\n    batch_size: 0\n"},
		{name: "timeout", yaml: "knowledge:\n  embedding:\n    request_timeout: 0s\n"},
		{name: "max document bytes", yaml: "knowledge:\n  ingest:\n    max_document_bytes: 0\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readKnowledgeConfig(t, tc.yaml); err == nil {
				t.Fatalf("expected invalid config for %s", tc.name)
			}
		})
	}
}

func readKnowledgeConfig(t *testing.T, extraYAML string) (knowledgeconfig.Config, error) {
	t.Helper()
	base := t.TempDir()
	path := filepath.Join(base, "orrery.yaml")
	writeKnowledgeRuntimeYAML(t, path, base)
	appendKnowledgeRuntimeYAML(t, path, extraYAML)
	var got knowledgeconfig.Config
	rt, err := platform.OpenFromYAMLWithModules(context.Background(), path, platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return registrar.RegisterConfigValidator("knowledge", func(section platform.ConfigSection) error {
			cfg, err := knowledgeconfig.FromSection(section)
			if err != nil {
				return err
			}
			got = cfg
			return nil
		})
	}))
	if rt != nil {
		defer rt.Close()
	}
	if err != nil {
		return knowledgeconfig.Config{}, err
	}
	return got, nil
}

func writeKnowledgeRuntimeYAML(t *testing.T, path, base string) {
	t.Helper()
	raw := `
environment: test
paths:
  base_dir: ` + base + `
database:
  urls:
    core: file://data/core.db
    audit: file://data/audit.db
    events: file://data/events.db
    jobs: file://data/jobs.db
    runtime: file://data/runtime.db
    observability: file://data/observability.db
filestore:
  backend: local
  root: artifacts
logging:
  console: false
  file: logs/platform.jsonl
health:
  readiness_cache_ttl: 1s
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write runtime yaml: %v", err)
	}
}

func appendKnowledgeRuntimeYAML(t *testing.T, path, body string) {
	t.Helper()
	if body == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open yaml for append: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString(body); err != nil {
		t.Fatalf("append yaml: %v", err)
	}
}

func TestRegisterKnowledgeConfigValidatorRejectsInvalidYAML(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "orrery.yaml")
	writeKnowledgeRuntimeYAML(t, path, base)
	appendKnowledgeRuntimeYAML(t, path, `
knowledge:
  vector_backend: qdrant
`)
	_, err := platform.OpenFromYAMLWithModules(context.Background(), path, platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		return knowledgeconfig.Register(registrar)
	}))
	if err == nil {
		t.Fatalf("expected platform startup to reject invalid knowledge config")
	}
}
