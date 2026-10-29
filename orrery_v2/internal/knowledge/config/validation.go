package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/platform"
)

func Register(registrar *platform.ModuleRegistrar) error {
	if registrar == nil {
		return fmt.Errorf("knowledge config registrar is required")
	}
	return registrar.RegisterConfigValidator("knowledge", func(section platform.ConfigSection) error {
		_, err := FromSection(section)
		return err
	})
}

func validate(cfg Config) error {
	if err := validateRoot(cfg.Root); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.VectorBackend) != string(core.VectorBackendLanceDB) {
		return fmt.Errorf("vector_backend must be %q", core.VectorBackendLanceDB)
	}
	if err := core.ValidateChunkPolicy(cfg.DefaultChunkPolicy); err != nil {
		return fmt.Errorf("default_chunk_policy invalid: %w", err)
	}
	if cfg.Embedding.BatchSize <= 0 {
		return fmt.Errorf("embedding.batch_size must be positive")
	}
	if cfg.Embedding.RequestTimeout <= 0 {
		return fmt.Errorf("embedding.request_timeout must be positive")
	}
	if cfg.Ingest.MaxDocumentBytes <= 0 {
		return fmt.Errorf("ingest.max_document_bytes must be positive")
	}
	if len(cfg.Ingest.SupportedContentTypes) == 0 {
		return fmt.Errorf("ingest.supported_content_types must not be empty")
	}
	for _, contentType := range cfg.Ingest.SupportedContentTypes {
		if strings.TrimSpace(contentType) == "" {
			return fmt.Errorf("ingest.supported_content_types must not contain blanks")
		}
	}
	return nil
}

func validateRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("root is required")
	}
	if _, err := filepath.Abs(root); err != nil {
		return fmt.Errorf("root must resolve to a local path: %w", err)
	}
	return nil
}
