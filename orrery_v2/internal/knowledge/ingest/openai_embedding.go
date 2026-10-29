package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"orrery_v2/internal/knowledge/core"
)

type OpenAIEmbeddingProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

func NewOpenAIEmbeddingProvider(client *http.Client, baseURL string, apiKey string) *OpenAIEmbeddingProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &OpenAIEmbeddingProvider{
		client:  client,
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  apiKey,
	}
}

func (p *OpenAIEmbeddingProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	if err := ctx.Err(); err != nil {
		return EmbedResponse{}, err
	}
	if p == nil || p.client == nil || p.baseURL == "" {
		return EmbedResponse{}, fmt.Errorf("%w: embedding provider dependency missing", core.ErrEmbeddingFailed)
	}
	if len(req.Inputs) == 0 {
		return EmbedResponse{}, fmt.Errorf("%w: embedding inputs are required", core.ErrEmbeddingFailed)
	}
	body, err := json.Marshal(openAIEmbeddingRequest{Model: req.Model, Input: req.Inputs})
	if err != nil {
		return EmbedResponse{}, fmt.Errorf("%w: encode embedding request: %v", core.ErrEmbeddingFailed, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return EmbedResponse{}, fmt.Errorf("%w: build embedding request: %v", core.ErrEmbeddingFailed, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.apiKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return EmbedResponse{}, fmt.Errorf("%w: send embedding request: %v", core.ErrEmbeddingFailed, err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return EmbedResponse{}, fmt.Errorf("%w: embedding provider status %d", core.ErrEmbeddingFailed, httpResp.StatusCode)
	}
	var decoded openAIEmbeddingResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&decoded); err != nil {
		return EmbedResponse{}, fmt.Errorf("%w: decode embedding response: %v", core.ErrEmbeddingFailed, err)
	}
	if len(decoded.Data) != len(req.Inputs) {
		return EmbedResponse{}, fmt.Errorf("%w: embedding count mismatch", core.ErrEmbeddingFailed)
	}
	vectors := make([][]float32, 0, len(decoded.Data))
	dimensions := 0
	for _, item := range decoded.Data {
		if dimensions == 0 {
			dimensions = len(item.Embedding)
		}
		if len(item.Embedding) == 0 || len(item.Embedding) != dimensions {
			return EmbedResponse{}, fmt.Errorf("%w: embedding dimensions mismatch", core.ErrEmbeddingFailed)
		}
		vectors = append(vectors, append([]float32(nil), item.Embedding...))
	}
	model := decoded.Model
	if model == "" {
		model = req.Model
	}
	return EmbedResponse{Model: model, Dimensions: dimensions, Vectors: vectors}, nil
}

type openAIEmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbeddingResponse struct {
	Model string `json:"model"`
	Data  []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}
