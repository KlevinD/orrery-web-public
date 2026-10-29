package testkit

import (
	"context"

	"orrery_v2/internal/knowledge/ingest"
)

type FakeEmbeddingProvider struct {
	Response ingest.EmbedResponse
}

func (p FakeEmbeddingProvider) Embed(context.Context, ingest.EmbedRequest) (ingest.EmbedResponse, error) {
	return p.Response, nil
}
