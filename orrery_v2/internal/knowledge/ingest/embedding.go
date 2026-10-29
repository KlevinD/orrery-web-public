package ingest

import "context"

type EmbeddingProvider interface {
	Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error)
}

type EmbedRequest struct {
	ProfileRef string
	Model      string
	Inputs     []string
}

type EmbedResponse struct {
	Model      string
	Dimensions int
	Vectors    [][]float32
}
