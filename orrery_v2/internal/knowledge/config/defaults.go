package config

import (
	"time"

	doccore "orrery_v2/internal/platform/document/core"
)

const (
	defaultRoot            = "data/knowledge"
	defaultVectorBackend   = "lancedb"
	defaultMaxDocumentSize = int64(50 * 1024 * 1024)
)

var defaultSupportedContentTypes = []string{
	"text/plain",
	"text/markdown",
	"text/html",
	"application/pdf",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
}

func Default() Config {
	return Config{
		Root:          defaultRoot,
		VectorBackend: defaultVectorBackend,
		DefaultChunkPolicy: doccore.ChunkPolicy{
			MaxRunes:      2400,
			OverlapRunes:  240,
			MinRunes:      400,
			PreserveHeads: true,
		},
		Embedding: EmbeddingConfig{
			BatchSize:      64,
			RequestTimeout: 30 * time.Second,
		},
		Ingest: IngestConfig{
			MaxDocumentBytes:      defaultMaxDocumentSize,
			SupportedContentTypes: cloneStrings(defaultSupportedContentTypes),
		},
	}
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
