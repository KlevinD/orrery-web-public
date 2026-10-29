package extractor

import (
	"context"

	"orrery_v2/internal/platform/document/core"
)

type Extractor interface {
	Supports(contentType, filename string) bool
	Extract(ctx context.Context, source core.Source) (core.ExtractedDocument, error)
}
