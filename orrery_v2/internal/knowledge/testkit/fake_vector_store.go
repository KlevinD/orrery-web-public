package testkit

import (
	"context"
	"sync"

	"orrery_v2/internal/knowledge/vector"
)

type FakeVectorStore struct {
	mu               sync.Mutex
	Root             string
	Ensured          []vector.Descriptor
	Dropped          []vector.Descriptor
	Upserts          []VectorUpsert
	DeletedDocuments []VectorDocumentDelete
	SearchCalls      []VectorSearchCall
	SearchPage       vector.SearchResultPage
	SearchPages      map[string]vector.SearchResultPage
}

type VectorUpsert struct {
	Descriptor vector.Descriptor
	Chunks     []vector.ChunkVector
}

type VectorDocumentDelete struct {
	Descriptor vector.Descriptor
	DocumentID string
}

type VectorSearchCall struct {
	Descriptor vector.Descriptor
	Query      vector.SearchQuery
}

func NewFakeVectorStore(root string) *FakeVectorStore {
	return &FakeVectorStore{Root: root}
}

func (s *FakeVectorStore) EnsureKnowledgeBase(_ context.Context, descriptor vector.Descriptor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Ensured = append(s.Ensured, descriptor)
	return nil
}

func (s *FakeVectorStore) DropKnowledgeBase(_ context.Context, descriptor vector.Descriptor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Dropped = append(s.Dropped, descriptor)
	return nil
}

func (s *FakeVectorStore) UpsertChunks(_ context.Context, descriptor vector.Descriptor, chunks []vector.ChunkVector) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Upserts = append(s.Upserts, VectorUpsert{Descriptor: descriptor, Chunks: cloneChunkVectors(chunks)})
	return nil
}

func (s *FakeVectorStore) DeleteDocument(_ context.Context, descriptor vector.Descriptor, documentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.DeletedDocuments = append(s.DeletedDocuments, VectorDocumentDelete{Descriptor: descriptor, DocumentID: documentID})
	return nil
}

func (s *FakeVectorStore) Search(_ context.Context, descriptor vector.Descriptor, query vector.SearchQuery) (vector.SearchResultPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SearchCalls = append(s.SearchCalls, VectorSearchCall{Descriptor: descriptor, Query: query})
	if s.SearchPages != nil {
		if page, ok := s.SearchPages[descriptor.KnowledgeBaseID]; ok {
			return cloneSearchPage(page), nil
		}
	}
	return cloneSearchPage(s.SearchPage), nil
}

func (s *FakeVectorStore) SearchCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.SearchCalls)
}

func (s *FakeVectorStore) SearchCallKnowledgeBaseIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.SearchCalls))
	for _, call := range s.SearchCalls {
		out = append(out, call.Descriptor.KnowledgeBaseID)
	}
	return out
}

func cloneSearchPage(page vector.SearchResultPage) vector.SearchResultPage {
	if len(page.Results) == 0 {
		return vector.SearchResultPage{Results: []vector.SearchResult{}}
	}
	out := vector.SearchResultPage{Results: make([]vector.SearchResult, len(page.Results))}
	for i, result := range page.Results {
		out.Results[i] = result
		out.Results[i].HeadingPath = append([]string(nil), result.HeadingPath...)
	}
	return out
}

func cloneChunkVectors(in []vector.ChunkVector) []vector.ChunkVector {
	if len(in) == 0 {
		return nil
	}
	out := make([]vector.ChunkVector, len(in))
	for i, chunk := range in {
		out[i] = chunk
		out[i].HeadingPath = append([]string(nil), chunk.HeadingPath...)
		out[i].Vector = append([]float32(nil), chunk.Vector...)
		if len(chunk.Metadata) > 0 {
			out[i].Metadata = make(map[string]string, len(chunk.Metadata))
			for key, value := range chunk.Metadata {
				out[i].Metadata[key] = value
			}
		}
	}
	return out
}
