package chunker

import (
	"context"

	"orrery_v2/internal/platform/document/core"
)

type Chunker interface {
	Chunk(ctx context.Context, doc core.ExtractedDocument, policy core.ChunkPolicy) ([]core.Chunk, error)
}
