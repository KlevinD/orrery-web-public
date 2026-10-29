package ingest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/ingest"
)

func TestOpenAIEmbeddingProviderSendsEmbeddingsRequest(t *testing.T) {
	var gotAuth string
	var gotPath string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"text-embedding-3-small","data":[{"embedding":[0.1,0.2,0.3]},{"embedding":[0.4,0.5,0.6]}]}`))
	}))
	defer server.Close()

	provider := ingest.NewOpenAIEmbeddingProvider(server.Client(), server.URL, "secret-key")
	resp, err := provider.Embed(context.Background(), ingest.EmbedRequest{
		Model:  "text-embedding-3-small",
		Inputs: []string{"alpha", "beta"},
	})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	if gotPath != "/embeddings" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotPayload["model"] != "text-embedding-3-small" {
		t.Fatalf("payload model = %#v", gotPayload)
	}
	inputs, ok := gotPayload["input"].([]any)
	if !ok || len(inputs) != 2 || inputs[0] != "alpha" || inputs[1] != "beta" {
		t.Fatalf("payload input = %#v", gotPayload["input"])
	}
	if resp.Model != "text-embedding-3-small" || resp.Dimensions != 3 || len(resp.Vectors) != 2 || resp.Vectors[1][2] != 0.6 {
		t.Fatalf("response mismatch: %#v", resp)
	}
}

func TestOpenAIEmbeddingProviderRejectsMismatchedVectorCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,2]}]}`))
	}))
	defer server.Close()

	provider := ingest.NewOpenAIEmbeddingProvider(server.Client(), server.URL, "")
	_, err := provider.Embed(context.Background(), ingest.EmbedRequest{Model: "m", Inputs: []string{"a", "b"}})
	if !errors.Is(err, core.ErrEmbeddingFailed) {
		t.Fatalf("expected embedding failed, got %v", err)
	}
}

func TestOpenAIEmbeddingProviderMapsNon2xxErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer server.Close()

	provider := ingest.NewOpenAIEmbeddingProvider(server.Client(), server.URL, "")
	_, err := provider.Embed(context.Background(), ingest.EmbedRequest{Model: "m", Inputs: []string{"a"}})
	if !errors.Is(err, core.ErrEmbeddingFailed) {
		t.Fatalf("expected embedding failed, got %v", err)
	}
}
