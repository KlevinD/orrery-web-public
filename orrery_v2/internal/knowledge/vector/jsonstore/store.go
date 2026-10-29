package jsonstore

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"orrery_v2/internal/knowledge/vector"
)

type Store struct {
	root string
}

const chunksFilename = "chunks.json"

type persistedChunk struct {
	ChunkID         string            `json:"chunk_id"`
	KnowledgeBaseID string            `json:"knowledge_base_id"`
	DocumentID      string            `json:"document_id"`
	Ordinal         int               `json:"ordinal"`
	Text            string            `json:"text"`
	HeadingPath     []string          `json:"heading_path"`
	ContentHash     string            `json:"content_hash"`
	Metadata        map[string]string `json:"metadata"`
	EmbeddingModel  string            `json:"embedding_model"`
	Vector          []float32         `json:"vector"`
}

type persistedChunks struct {
	Chunks []persistedChunk `json:"chunks"`
}

func NewStore(root string) *Store {
	return &Store{root: root}
}

func (s *Store) EnsureKnowledgeBase(ctx context.Context, descriptor vector.Descriptor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.validateDescriptor(descriptor); err != nil {
		return err
	}
	return os.MkdirAll(descriptor.StorePath, 0o755)
}

func (s *Store) DropKnowledgeBase(ctx context.Context, descriptor vector.Descriptor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.validateDescriptor(descriptor); err != nil {
		return err
	}
	baseDir := filepath.Dir(descriptor.StorePath)
	if err := ensureUnderRoot(descriptor.RootPath, baseDir); err != nil {
		return err
	}
	if baseDir == descriptor.RootPath {
		return fmt.Errorf("%w: refusing to delete vector root", vector.ErrUnsafePath)
	}
	return os.RemoveAll(baseDir)
}

func (s *Store) UpsertChunks(ctx context.Context, descriptor vector.Descriptor, chunks []vector.ChunkVector) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.EnsureKnowledgeBase(ctx, descriptor); err != nil {
		return err
	}
	existing, err := s.loadChunks(descriptor)
	if err != nil {
		return err
	}
	byID := make(map[string]persistedChunk, len(existing)+len(chunks))
	for _, chunk := range existing {
		byID[chunk.ChunkID] = chunk
	}
	for _, chunk := range chunks {
		if chunk.ChunkID == "" {
			return fmt.Errorf("%w: chunk_id is required", vector.ErrStoreUnavailable)
		}
		if descriptor.Dimensions > 0 && len(chunk.Vector) > 0 && len(chunk.Vector) != descriptor.Dimensions {
			return fmt.Errorf("%w: vector dimensions mismatch", vector.ErrStoreUnavailable)
		}
		byID[chunk.ChunkID] = persistedChunk{
			ChunkID:         chunk.ChunkID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			DocumentID:      chunk.DocumentID,
			Ordinal:         chunk.Ordinal,
			Text:            chunk.Text,
			HeadingPath:     append([]string(nil), chunk.HeadingPath...),
			ContentHash:     chunk.ContentHash,
			Metadata:        cloneMetadata(chunk.Metadata),
			EmbeddingModel:  chunk.EmbeddingModel,
			Vector:          append([]float32(nil), chunk.Vector...),
		}
	}
	next := make([]persistedChunk, 0, len(byID))
	for _, chunk := range byID {
		next = append(next, chunk)
	}
	sort.Slice(next, func(i, j int) bool {
		if next[i].DocumentID == next[j].DocumentID {
			if next[i].Ordinal == next[j].Ordinal {
				return next[i].ChunkID < next[j].ChunkID
			}
			return next[i].Ordinal < next[j].Ordinal
		}
		return next[i].DocumentID < next[j].DocumentID
	})
	return s.writeChunks(descriptor, next)
}

func (s *Store) DeleteDocument(ctx context.Context, descriptor vector.Descriptor, documentID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.validateDescriptor(descriptor); err != nil {
		return err
	}
	existing, err := s.loadChunks(descriptor)
	if err != nil {
		return err
	}
	next := existing[:0]
	for _, chunk := range existing {
		if chunk.DocumentID != documentID {
			next = append(next, chunk)
		}
	}
	return s.writeChunks(descriptor, next)
}

func (s *Store) Search(ctx context.Context, descriptor vector.Descriptor, query vector.SearchQuery) (vector.SearchResultPage, error) {
	if err := ctx.Err(); err != nil {
		return vector.SearchResultPage{}, err
	}
	if err := s.validateDescriptor(descriptor); err != nil {
		return vector.SearchResultPage{}, err
	}
	if len(query.Vector) == 0 {
		return vector.SearchResultPage{}, fmt.Errorf("%w: query vector is required", vector.ErrUnsupportedSearch)
	}
	existing, err := s.loadChunks(descriptor)
	if err != nil {
		return vector.SearchResultPage{}, err
	}
	results := make([]vector.SearchResult, 0, len(existing))
	for _, chunk := range existing {
		score := cosine(query.Vector, chunk.Vector)
		results = append(results, vector.SearchResult{
			ChunkID:         chunk.ChunkID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			DocumentID:      chunk.DocumentID,
			Score:           score,
			Text:            chunk.Text,
			HeadingPath:     append([]string(nil), chunk.HeadingPath...),
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].ChunkID < results[j].ChunkID
		}
		return results[i].Score > results[j].Score
	})
	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}
	return vector.SearchResultPage{Results: results}, nil
}

func (s *Store) validateDescriptor(descriptor vector.Descriptor) error {
	root := descriptor.RootPath
	if root == "" {
		root = s.root
	}
	if root == "" {
		return fmt.Errorf("%w: vector root is required", vector.ErrUnsafePath)
	}
	if descriptor.StorePath == "" {
		return fmt.Errorf("%w: vector store path is required", vector.ErrUnsafePath)
	}
	if err := ensureUnderRoot(root, descriptor.StorePath); err != nil {
		return err
	}
	return nil
}

func (s *Store) chunksPath(descriptor vector.Descriptor) string {
	return filepath.Join(descriptor.StorePath, chunksFilename)
}

func (s *Store) loadChunks(descriptor vector.Descriptor) ([]persistedChunk, error) {
	if info, err := os.Stat(descriptor.StorePath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: vector store directory missing", vector.ErrStoreUnavailable)
		}
		return nil, fmt.Errorf("%w: stat vector store: %v", vector.ErrStoreUnavailable, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("%w: vector store path is not a directory", vector.ErrStoreUnavailable)
	}
	data, err := os.ReadFile(s.chunksPath(descriptor))
	if err != nil {
		if os.IsNotExist(err) {
			return []persistedChunk{}, nil
		}
		return nil, fmt.Errorf("%w: read chunks: %v", vector.ErrStoreUnavailable, err)
	}
	var file persistedChunks
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("%w: decode chunks: %v", vector.ErrStoreUnavailable, err)
	}
	return file.Chunks, nil
}

func (s *Store) writeChunks(descriptor vector.Descriptor, chunks []persistedChunk) error {
	if err := os.MkdirAll(descriptor.StorePath, 0o755); err != nil {
		return fmt.Errorf("%w: create vector store path: %v", vector.ErrStoreUnavailable, err)
	}
	data, err := json.MarshalIndent(persistedChunks{Chunks: chunks}, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: encode chunks: %v", vector.ErrStoreUnavailable, err)
	}
	path := s.chunksPath(descriptor)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("%w: write chunks: %v", vector.ErrStoreUnavailable, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%w: commit chunks: %v", vector.ErrStoreUnavailable, err)
	}
	return nil
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	next := make(map[string]string, len(metadata))
	for key, value := range metadata {
		next[key] = value
	}
	return next
}

func cosine(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(normA) * math.Sqrt(normB)))
}
