package vector

import "context"

type Store interface {
	EnsureKnowledgeBase(ctx context.Context, descriptor Descriptor) error
	DropKnowledgeBase(ctx context.Context, descriptor Descriptor) error
	UpsertChunks(ctx context.Context, descriptor Descriptor, chunks []ChunkVector) error
	DeleteDocument(ctx context.Context, descriptor Descriptor, documentID string) error
	Search(ctx context.Context, descriptor Descriptor, query SearchQuery) (SearchResultPage, error)
}

type Descriptor struct {
	KnowledgeBaseID string
	Backend         string
	RootPath        string
	StorePath       string
	TableName       string
	Dimensions      int
}

type ChunkVector struct {
	ChunkID         string
	KnowledgeBaseID string
	DocumentID      string
	Ordinal         int
	Text            string
	HeadingPath     []string
	ContentHash     string
	Metadata        map[string]string
	EmbeddingModel  string
	Vector          []float32
}

type SearchQuery struct {
	Vector []float32
	Limit  int
}

type SearchResult struct {
	ChunkID         string
	KnowledgeBaseID string
	DocumentID      string
	Score           float32
	Text            string
	HeadingPath     []string
}

type SearchResultPage struct {
	Results []SearchResult
}
