package config

import (
	"time"

	doccore "orrery_v2/internal/platform/document/core"
)

type Config struct {
	Root               string
	VectorBackend      string
	DefaultChunkPolicy doccore.ChunkPolicy
	Embedding          EmbeddingConfig
	Ingest             IngestConfig
}

type EmbeddingConfig struct {
	DefaultProfileRef string
	BatchSize         int
	RequestTimeout    time.Duration
}

type IngestConfig struct {
	MaxDocumentBytes      int64
	SupportedContentTypes []string
}
