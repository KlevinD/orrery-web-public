package jsonstore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/vector"
	"orrery_v2/internal/knowledge/vector/jsonstore"
)

func TestStoreUpsertSearchAndDeleteDocument(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := jsonstore.NewStore(root)
	descriptor, err := jsonstore.DescriptorForKnowledgeBase(root, core.KnowledgeBaseID("kb-search"), 3)
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	if err := store.EnsureKnowledgeBase(ctx, descriptor); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	chunks := []vector.ChunkVector{
		{
			ChunkID:         "chunk-a",
			KnowledgeBaseID: "kb-search",
			DocumentID:      "doc-a",
			Ordinal:         0,
			Text:            "alpha mission bootstrap",
			HeadingPath:     []string{"Mission"},
			ContentHash:     "hash-a",
			EmbeddingModel:  "test-embedding",
			Vector:          []float32{1, 0, 0},
		},
		{
			ChunkID:         "chunk-b",
			KnowledgeBaseID: "kb-search",
			DocumentID:      "doc-b",
			Ordinal:         0,
			Text:            "beta unrelated",
			HeadingPath:     []string{"Other"},
			ContentHash:     "hash-b",
			EmbeddingModel:  "test-embedding",
			Vector:          []float32{0, 1, 0},
		},
	}
	if err := store.UpsertChunks(ctx, descriptor, chunks); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	page, err := store.Search(ctx, descriptor, vector.SearchQuery{Vector: []float32{0.9, 0.1, 0}, Limit: 2})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(page.Results) != 2 {
		t.Fatalf("result count = %d, want 2: %#v", len(page.Results), page.Results)
	}
	if page.Results[0].ChunkID != "chunk-a" || page.Results[0].DocumentID != "doc-a" || page.Results[0].Score <= page.Results[1].Score {
		t.Fatalf("results not ranked by similarity: %#v", page.Results)
	}
	if page.Results[0].Text != "alpha mission bootstrap" || page.Results[0].HeadingPath[0] != "Mission" {
		t.Fatalf("payload not returned: %#v", page.Results[0])
	}

	if err := store.DeleteDocument(ctx, descriptor, "doc-a"); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	page, err = store.Search(ctx, descriptor, vector.SearchQuery{Vector: []float32{1, 0, 0}, Limit: 10})
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	for _, hit := range page.Results {
		if hit.DocumentID == "doc-a" {
			t.Fatalf("deleted document returned in search: %#v", page.Results)
		}
	}
}

func TestStoreDropRemovesPersistedChunks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := jsonstore.NewStore(root)
	descriptor, err := jsonstore.DescriptorForKnowledgeBase(root, core.KnowledgeBaseID("kb-drop"), 2)
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	if err := store.EnsureKnowledgeBase(ctx, descriptor); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := store.UpsertChunks(ctx, descriptor, []vector.ChunkVector{{
		ChunkID:         "chunk-drop",
		KnowledgeBaseID: "kb-drop",
		DocumentID:      "doc-drop",
		Text:            "drop me",
		Vector:          []float32{1, 0},
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := os.Stat(filepath.Join(descriptor.StorePath, "chunks.json")); err != nil {
		t.Fatalf("expected persisted chunks: %v", err)
	}

	if err := store.DropKnowledgeBase(ctx, descriptor); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "kb_kb-drop")); !os.IsNotExist(err) {
		t.Fatalf("expected per-base directory removed, got %v", err)
	}
}

func TestStoreSearchReportsMissingVectorDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := jsonstore.NewStore(root)
	descriptor, err := jsonstore.DescriptorForKnowledgeBase(root, core.KnowledgeBaseID("kb-missing"), 2)
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}

	_, err = store.Search(ctx, descriptor, vector.SearchQuery{Vector: []float32{1, 0}, Limit: 1})
	if !errors.Is(err, vector.ErrStoreUnavailable) {
		t.Fatalf("missing directory search error = %v, want store unavailable", err)
	}
}

func TestStoreSearchReturnsEmptyWhenDirectoryExistsWithoutChunks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := jsonstore.NewStore(root)
	descriptor, err := jsonstore.DescriptorForKnowledgeBase(root, core.KnowledgeBaseID("kb-empty"), 2)
	if err != nil {
		t.Fatalf("descriptor: %v", err)
	}
	if err := store.EnsureKnowledgeBase(ctx, descriptor); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	page, err := store.Search(ctx, descriptor, vector.SearchQuery{Vector: []float32{1, 0}, Limit: 1})
	if err != nil {
		t.Fatalf("empty directory search: %v", err)
	}
	if len(page.Results) != 0 {
		t.Fatalf("empty directory results = %#v", page.Results)
	}
}
