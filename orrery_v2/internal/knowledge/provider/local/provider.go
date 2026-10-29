package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/ingest"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/knowledge/vector"
	"orrery_v2/internal/knowledge/vector/jsonstore"
)

type IDGenerator interface {
	NewID(prefix string) string
}

type Clock interface {
	Now() time.Time
}

type ArtifactRef struct {
	ID          string
	Name        string
	ContentType string
	SizeBytes   int64
	Checksum    string
}

type ArtifactWriter interface {
	PutArtifact(ctx context.Context, name, contentType string, reader io.Reader) (ArtifactRef, error)
}

type ExecutionSubmitter interface {
	Submit(ctx context.Context, topic string, payload []byte, maxAttempts int) error
}

type Options struct {
	Repository store.Repository
	Vector     vector.Store
	Embedding  ingest.EmbeddingProvider
	IDs        IDGenerator
	Clock      Clock
	Artifacts  ArtifactWriter
	Execution  ExecutionSubmitter
	MaxBytes   int64
}

type Provider struct {
	repo      store.Repository
	vector    vector.Store
	embedding ingest.EmbeddingProvider
	ids       IDGenerator
	clock     Clock
	artifacts ArtifactWriter
	execution ExecutionSubmitter
	maxBytes  int64
}

func New(opts Options) *Provider {
	return &Provider{
		repo:      opts.Repository,
		vector:    opts.Vector,
		embedding: opts.Embedding,
		ids:       opts.IDs,
		clock:     opts.Clock,
		artifacts: opts.Artifacts,
		execution: opts.Execution,
		maxBytes:  opts.MaxBytes,
	}
}

func (p *Provider) Name() core.KnowledgeProvider {
	return core.ProviderLocal
}

func (p *Provider) EnsureKnowledgeBase(ctx context.Context, req provider.EnsureKnowledgeBaseRequest) (provider.EnsureKnowledgeBaseResult, error) {
	if p == nil || p.vector == nil {
		return provider.EnsureKnowledgeBaseResult{}, dependencyError("local vector store")
	}
	base := req.Base
	if base.VectorBackend == "" {
		base.VectorBackend = core.VectorBackendLanceDB
	}
	descriptor, err := jsonstore.DescriptorForKnowledgeBase(req.VectorRoot, base.ID, base.EmbeddingDimensions)
	if err != nil {
		return provider.EnsureKnowledgeBaseResult{}, err
	}
	if err := p.vector.EnsureKnowledgeBase(ctx, descriptor); err != nil {
		return provider.EnsureKnowledgeBaseResult{}, err
	}
	return provider.EnsureKnowledgeBaseResult{
		ProviderResourceID: descriptor.StorePath,
		VectorStorePath:    descriptor.StorePath,
		Metadata:           map[string]string{"local_backend": string(base.VectorBackend)},
	}, nil
}

func (p *Provider) DeleteKnowledgeBase(ctx context.Context, req provider.DeleteKnowledgeBaseRequest) error {
	if p == nil || p.vector == nil {
		return dependencyError("local vector store")
	}
	return p.vector.DropKnowledgeBase(ctx, vectorDescriptorForBase(req.Base))
}

func (p *Provider) UploadDocument(ctx context.Context, req provider.UploadDocumentRequest) (provider.UploadDocumentResult, error) {
	if p == nil || p.artifacts == nil {
		return provider.UploadDocumentResult{}, dependencyError("local artifact store")
	}
	if req.Reader == nil {
		return provider.UploadDocumentResult{}, fmt.Errorf("%w: document reader is required", core.ErrInvalid)
	}
	reader := req.Reader
	if p.maxBytes > 0 {
		limited, err := uploadReaderWithinLimit(reader, p.maxBytes)
		if err != nil {
			return provider.UploadDocumentResult{}, err
		}
		reader = limited
	}
	ref, err := p.artifacts.PutArtifact(ctx, req.Filename, req.ContentType, reader)
	if err != nil {
		return provider.UploadDocumentResult{}, err
	}
	result := provider.UploadDocumentResult{
		ArtifactID:  ref.ID,
		Filename:    ref.Name,
		ContentType: ref.ContentType,
		SizeBytes:   ref.SizeBytes,
		Checksum:    ref.Checksum,
		Status:      core.DocumentUploaded,
	}
	jobID, queued, err := p.queueIngest(ctx, req.Base.ID, req.DocumentID)
	if err != nil {
		return provider.UploadDocumentResult{}, err
	}
	result.IngestJobID = jobID
	result.IngestJobQueued = queued
	return result, nil
}

func (p *Provider) CreateDocumentMetadata(ctx context.Context, req provider.CreateDocumentMetadataRequest) (provider.UploadDocumentResult, error) {
	if p.maxBytes > 0 && req.Document.SizeBytes > p.maxBytes {
		return provider.UploadDocumentResult{}, fmt.Errorf("%w: document exceeds max_document_bytes", core.ErrInvalid)
	}
	result := provider.UploadDocumentResult{
		ArtifactID:         req.Document.ArtifactID,
		ProviderDocumentID: req.Document.ProviderDocumentID,
		Filename:           req.Document.Filename,
		ContentType:        req.Document.ContentType,
		SizeBytes:          req.Document.SizeBytes,
		Checksum:           req.Document.Checksum,
		Status:             core.DocumentUploaded,
		ProviderMetadata:   cloneMetadata(req.Document.ProviderMetadata),
	}
	jobID, queued, err := p.queueIngest(ctx, req.Base.ID, req.Document.ID)
	if err != nil {
		return provider.UploadDocumentResult{}, err
	}
	result.IngestJobID = jobID
	result.IngestJobQueued = queued
	return result, nil
}

func (p *Provider) DeleteDocument(ctx context.Context, req provider.DeleteDocumentRequest) error {
	if p == nil || p.vector == nil || p.repo == nil {
		return dependencyError("local delete dependencies")
	}
	if err := p.vector.DeleteDocument(ctx, vectorDescriptorForBase(req.Base), string(req.Document.ID)); err != nil {
		return err
	}
	return p.repo.ReplaceChunks(ctx, req.Document.ID, nil)
}

func (p *Provider) RefreshDocumentStatus(ctx context.Context, req provider.DocumentStatusRequest) (provider.DocumentStatusResult, error) {
	if err := ctx.Err(); err != nil {
		return provider.DocumentStatusResult{}, err
	}
	return provider.DocumentStatusResult{
		ProviderDocumentID: req.Document.ProviderDocumentID,
		Filename:           req.Document.Filename,
		ContentType:        req.Document.ContentType,
		SizeBytes:          req.Document.SizeBytes,
		Checksum:           req.Document.Checksum,
		Status:             req.Document.Status,
		ErrorMessage:       req.Document.ErrorMessage,
		ProviderMetadata:   cloneMetadata(req.Document.ProviderMetadata),
	}, nil
}

func (p *Provider) ReprocessDocument(ctx context.Context, req provider.ReprocessDocumentRequest) (provider.ReprocessDocumentResult, error) {
	if err := ctx.Err(); err != nil {
		return provider.ReprocessDocumentResult{}, err
	}
	return provider.ReprocessDocumentResult{}, fmt.Errorf("%w: local provider does not support document reprocess", core.ErrInvalid)
}

func (p *Provider) GetDocumentProcessingResult(ctx context.Context, req provider.DocumentProcessingResultRequest) (provider.DocumentProcessingResult, error) {
	if err := ctx.Err(); err != nil {
		return provider.DocumentProcessingResult{}, err
	}
	return provider.DocumentProcessingResult{}, fmt.Errorf("%w: local provider does not expose document processing preview", core.ErrInvalid)
}

func (p *Provider) ReindexKnowledgeBase(ctx context.Context, req provider.ReindexKnowledgeBaseRequest) (provider.ReindexKnowledgeBaseResult, error) {
	if p == nil || p.ids == nil {
		return provider.ReindexKnowledgeBaseResult{}, dependencyError("local id generator")
	}
	jobID := core.KnowledgeJobID(p.ids.NewID("kjob"))
	if err := p.submitKnowledgeJob(ctx, ingest.TopicReindexBase, ingest.ReindexBasePayload{
		JobID:           jobID,
		KnowledgeBaseID: req.Base.ID,
	}, 1); err != nil {
		return provider.ReindexKnowledgeBaseResult{}, err
	}
	return provider.ReindexKnowledgeBaseResult{JobID: jobID, Queued: true, LocalIngestJob: true, Supported: true}, nil
}

func (p *Provider) Search(ctx context.Context, req provider.SearchRequest) (provider.SearchResultPage, error) {
	if p == nil || p.repo == nil || p.vector == nil || p.embedding == nil {
		return provider.SearchResultPage{}, dependencyError("local search dependencies")
	}
	if len(req.Bases) == 0 {
		return provider.SearchResultPage{Results: []provider.SearchResult{}}, nil
	}
	hasIndexedDocuments, err := p.hasIndexedDocuments(ctx, req.Bases)
	if err != nil {
		return provider.SearchResultPage{}, err
	}
	if !hasIndexedDocuments {
		return provider.SearchResultPage{Results: []provider.SearchResult{}}, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 8
	}
	embed, err := p.embedding.Embed(ctx, ingest.EmbedRequest{Model: req.Bases[0].EmbeddingModel, Inputs: []string{req.Query}})
	if err != nil {
		return provider.SearchResultPage{}, err
	}
	if len(embed.Vectors) != 1 {
		return provider.SearchResultPage{}, fmt.Errorf("%w: search embedding count mismatch", core.ErrEmbeddingFailed)
	}
	allowed := make(map[core.KnowledgeBaseID]core.KnowledgeBase, len(req.Bases))
	for _, base := range req.Bases {
		allowed[base.ID] = base
	}
	results, err := p.searchBases(ctx, req.Bases, allowed, embed.Vectors[0], limit)
	if err != nil {
		return provider.SearchResultPage{}, err
	}
	return provider.SearchResultPage{Results: results}, nil
}

func (p *Provider) hasIndexedDocuments(ctx context.Context, bases []core.KnowledgeBase) (bool, error) {
	for _, base := range bases {
		page, err := p.repo.ListDocuments(ctx, core.DocumentQuery{
			KnowledgeBaseID: base.ID,
			Status:          core.DocumentIndexed,
			Limit:           1,
		})
		if err != nil {
			return false, err
		}
		for _, doc := range page.Items {
			if doc.KnowledgeBaseID == base.ID && doc.Status == core.DocumentIndexed {
				return true, nil
			}
		}
	}
	return false, nil
}

func (p *Provider) queueIngest(ctx context.Context, baseID core.KnowledgeBaseID, documentID core.KnowledgeDocumentID) (core.KnowledgeJobID, bool, error) {
	if p == nil || p.ids == nil {
		return "", false, dependencyError("local id generator")
	}
	jobID := core.KnowledgeJobID(p.ids.NewID("kjob"))
	if err := p.submitKnowledgeJob(ctx, ingest.TopicIngestDocument, ingest.DocumentIngestPayload{
		JobID:           jobID,
		KnowledgeBaseID: baseID,
		DocumentID:      documentID,
	}, 3); err != nil {
		return "", false, err
	}
	return jobID, true, nil
}

func (p *Provider) submitKnowledgeJob(ctx context.Context, topic string, payload any, maxAttempts int) error {
	if p.execution == nil {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return p.execution.Submit(ctx, topic, data, maxAttempts)
}

func (p *Provider) searchBases(ctx context.Context, bases []core.KnowledgeBase, allowed map[core.KnowledgeBaseID]core.KnowledgeBase, queryVector []float32, limit int) ([]provider.SearchResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make([]provider.SearchResult, 0)
	var firstErr error
	for _, base := range bases {
		base := base
		wg.Add(1)
		go func() {
			defer wg.Done()
			page, err := p.vector.Search(ctx, vectorDescriptorForBase(base), vector.SearchQuery{Vector: queryVector, Limit: limit})
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
				return
			}
			mapped := p.mapHits(ctx, page.Results, allowed)
			mu.Lock()
			results = append(results, mapped...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

func (p *Provider) mapHits(ctx context.Context, hits []vector.SearchResult, allowed map[core.KnowledgeBaseID]core.KnowledgeBase) []provider.SearchResult {
	results := make([]provider.SearchResult, 0, len(hits))
	for _, hit := range hits {
		if _, ok := allowed[core.KnowledgeBaseID(hit.KnowledgeBaseID)]; !ok {
			continue
		}
		doc, err := p.repo.GetDocument(ctx, core.KnowledgeDocumentID(hit.DocumentID))
		if err != nil || doc.KnowledgeBaseID != core.KnowledgeBaseID(hit.KnowledgeBaseID) || doc.Status != core.DocumentIndexed {
			continue
		}
		results = append(results, provider.SearchResult{
			KnowledgeBaseID: hit.KnowledgeBaseID,
			DocumentID:      hit.DocumentID,
			ChunkID:         hit.ChunkID,
			Score:           hit.Score,
			Snippet:         hit.Text,
			HeadingPath:     append([]string(nil), hit.HeadingPath...),
			DocumentName:    doc.Filename,
		})
	}
	return results
}

func vectorDescriptorForBase(base core.KnowledgeBase) vector.Descriptor {
	root := ""
	if strings.TrimSpace(base.VectorStorePath) != "" {
		root = filepath.Dir(filepath.Dir(base.VectorStorePath))
	}
	return vector.Descriptor{
		KnowledgeBaseID: string(base.ID),
		Backend:         string(base.VectorBackend),
		RootPath:        root,
		StorePath:       base.VectorStorePath,
		TableName:       jsonstore.DefaultTableName,
		Dimensions:      base.EmbeddingDimensions,
	}
}

func dependencyError(name string) error {
	return fmt.Errorf("%w: %s is not configured", core.ErrDependencyUnavailable, name)
}

func uploadReaderWithinLimit(reader io.Reader, maxBytes int64) (io.Reader, error) {
	if maxBytes <= 0 {
		return reader, nil
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read document: %v", core.ErrInvalid, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: document exceeds max_document_bytes", core.ErrInvalid)
	}
	return bytes.NewReader(data), nil
}

func cloneMetadata(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}
