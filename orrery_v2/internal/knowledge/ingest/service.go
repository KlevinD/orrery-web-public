package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/knowledge/vector"
	docchunker "orrery_v2/internal/platform/document/chunker"
	doccore "orrery_v2/internal/platform/document/core"
	docextractor "orrery_v2/internal/platform/document/extractor"
	"orrery_v2/internal/platform/graphprotocol"
)

type Clock interface {
	Now() time.Time
}

type Options struct {
	BeforeChunk           func(context.Context, DocumentIngestPayload) error
	Artifacts             ArtifactReader
	Extractors            *docextractor.Registry
	Chunker               docchunker.Chunker
	Embedding             EmbeddingProvider
	VectorStore           vector.Store
	DescriptorForBase     func(core.KnowledgeBase) (vector.Descriptor, error)
	SupportedContentTypes []string
	MaxDocumentBytes      int64
	Metrics               MetricsRecorder
	DocumentStatusSink    DocumentStatusSink
}

type ArtifactReader interface {
	GetReader(ctx context.Context, id string) (io.ReadCloser, error)
}

type MetricsRecorder interface {
	RecordVectorUpsert(duration time.Duration, chunks int)
}

type DocumentStatusSink interface {
	PublishKnowledgeDocumentStatus(ctx context.Context, event graphprotocol.DomainEvent) error
}

type KnowledgeDocumentMetadataSink interface {
	PublishKnowledgeDocumentMetadata(ctx context.Context, event graphprotocol.DomainEvent) error
}

type KnowledgeChunkMetadataSink interface {
	PublishKnowledgeChunkMetadata(ctx context.Context, event graphprotocol.DomainEvent) error
}

type VectorDescriptor = vector.Descriptor

type Service struct {
	repo  store.Repository
	clock Clock
	opts  Options
}

func NewService(repo store.Repository, clock Clock, opts Options) *Service {
	return &Service{repo: repo, clock: clock, opts: opts}
}

func (s *Service) HandleDocumentIngest(ctx context.Context, payload DocumentIngestPayload) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.repo == nil || s.clock == nil {
		return fmt.Errorf("%w: ingest service dependency missing", core.ErrDependencyUnavailable)
	}
	now := s.clock.Now()
	if err := s.repo.UpdateIngestJobStatus(ctx, payload.JobID, core.JobRunning, "", now); err != nil {
		return err
	}
	if err := s.updateDocumentStatus(ctx, payload.DocumentID, core.DocumentParsing, "", now); err != nil {
		return err
	}
	if s.opts.BeforeChunk != nil {
		if err := s.opts.BeforeChunk(ctx, payload); err != nil {
			return s.fail(ctx, payload, fmt.Errorf("%w: %v", core.ErrIngestFailed, err))
		}
	}
	if err := s.requireFullPipeline(); err != nil {
		return s.fail(ctx, payload, err)
	}
	if err := s.updateDocumentStatus(ctx, payload.DocumentID, core.DocumentChunking, "", now); err != nil {
		return err
	}
	if err := s.indexDocument(ctx, payload, now); err != nil {
		return s.fail(ctx, payload, err)
	}
	return nil
}

func (s *Service) HandleReindexBase(ctx context.Context, payload ReindexBasePayload) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.repo == nil || s.clock == nil {
		return fmt.Errorf("%w: ingest service dependency missing", core.ErrDependencyUnavailable)
	}
	if err := s.requireFullPipeline(); err != nil {
		return s.failReindex(ctx, payload, err)
	}
	now := s.clock.Now()
	if err := s.repo.UpdateIngestJobStatus(ctx, payload.JobID, core.JobRunning, "", now); err != nil {
		return err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return s.failReindex(ctx, payload, fmt.Errorf("%w: load knowledge base: %v", core.ErrIngestFailed, err))
	}
	descriptor, err := s.opts.DescriptorForBase(base)
	if err != nil {
		return s.failReindex(ctx, payload, fmt.Errorf("%w: build vector descriptor: %v", core.ErrIngestFailed, err))
	}
	page, err := s.repo.ListDocuments(ctx, core.DocumentQuery{KnowledgeBaseID: payload.KnowledgeBaseID})
	if err != nil {
		return s.failReindex(ctx, payload, err)
	}
	for _, doc := range page.Items {
		if doc.Status == core.DocumentDeleting || doc.Status == core.DocumentFailed {
			continue
		}
		if err := s.opts.VectorStore.DeleteDocument(ctx, descriptor, string(doc.ID)); err != nil {
			return s.failReindex(ctx, payload, fmt.Errorf("%w: delete document vectors: %v", core.ErrVectorStoreUnavailable, err))
		}
		if err := s.updateDocumentStatus(ctx, doc.ID, core.DocumentParsing, "", now); err != nil {
			return s.failReindex(ctx, payload, err)
		}
		if err := s.updateDocumentStatus(ctx, doc.ID, core.DocumentChunking, "", now); err != nil {
			return s.failReindex(ctx, payload, err)
		}
		if err := s.indexDocument(ctx, DocumentIngestPayload{JobID: payload.JobID, KnowledgeBaseID: payload.KnowledgeBaseID, DocumentID: doc.ID}, now); err != nil {
			return s.failReindex(ctx, payload, err)
		}
	}
	return s.repo.UpdateIngestJobStatus(ctx, payload.JobID, core.JobCompleted, "", now)
}

func (s *Service) hasFullPipeline() bool {
	return s.opts.Artifacts != nil &&
		s.opts.Extractors != nil &&
		s.opts.Chunker != nil &&
		s.opts.Embedding != nil &&
		s.opts.VectorStore != nil &&
		s.opts.DescriptorForBase != nil
}

func (s *Service) requireFullPipeline() error {
	if s == nil || !s.hasFullPipeline() {
		return fmt.Errorf("%w: ingest pipeline dependency missing", core.ErrDependencyUnavailable)
	}
	return nil
}

func (s *Service) indexDocument(ctx context.Context, payload DocumentIngestPayload, at time.Time) error {
	base, err := s.repo.GetKnowledgeBase(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return fmt.Errorf("%w: load knowledge base: %v", core.ErrIngestFailed, err)
	}
	doc, err := s.repo.GetDocument(ctx, payload.DocumentID)
	if err != nil {
		return fmt.Errorf("%w: load document: %v", core.ErrIngestFailed, err)
	}
	if !isSupportedDocumentType(s.opts.SupportedContentTypes, doc.ContentType, doc.Filename) {
		return fmt.Errorf("%w: %s", core.ErrDocumentUnsupported, normalizeContentType(doc.ContentType))
	}
	reader, err := s.opts.Artifacts.GetReader(ctx, doc.ArtifactID)
	if err != nil {
		return fmt.Errorf("%w: open artifact: %v", core.ErrIngestFailed, err)
	}
	defer reader.Close()
	sourceReader := io.Reader(reader)
	if s.opts.MaxDocumentBytes > 0 {
		sourceReader = &maxBytesReader{reader: reader, maxBytes: s.opts.MaxDocumentBytes}
	}
	extracted, err := s.opts.Extractors.Extract(ctx, doccore.Source{
		Name:        doc.Filename,
		ContentType: doc.ContentType,
		Reader:      sourceReader,
	})
	if err != nil {
		if errors.Is(err, doccore.ErrUnsupported) {
			return fmt.Errorf("%w: %v", core.ErrDocumentUnsupported, err)
		}
		return fmt.Errorf("%w: extract document: %w", core.ErrIngestFailed, err)
	}
	chunks, err := s.opts.Chunker.Chunk(ctx, extracted, base.ChunkPolicy)
	if err != nil {
		return fmt.Errorf("%w: chunk document: %v", core.ErrIngestFailed, err)
	}
	if len(chunks) == 0 {
		return fmt.Errorf("%w: document produced no chunks", core.ErrIngestFailed)
	}
	if err := s.updateDocumentStatus(ctx, payload.DocumentID, core.DocumentEmbedding, "", at); err != nil {
		return err
	}
	inputs := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		inputs = append(inputs, chunk.Text)
	}
	embeddings, err := s.opts.Embedding.Embed(ctx, EmbedRequest{
		ProfileRef: base.EmbeddingProfileRef,
		Model:      base.EmbeddingModel,
		Inputs:     inputs,
	})
	if err != nil {
		return fmt.Errorf("%w: embed chunks: %v", core.ErrEmbeddingFailed, err)
	}
	if len(embeddings.Vectors) != len(chunks) {
		return fmt.Errorf("%w: embedding count mismatch", core.ErrEmbeddingFailed)
	}
	metadataChunks := make([]core.KnowledgeChunk, 0, len(chunks))
	vectorChunks := make([]vector.ChunkVector, 0, len(chunks))
	model := embeddings.Model
	if model == "" {
		model = base.EmbeddingModel
	}
	for i, chunk := range chunks {
		chunkID := core.KnowledgeChunkID(fmt.Sprintf("%s:%d", doc.ID, chunk.Ordinal))
		metadataChunks = append(metadataChunks, core.KnowledgeChunk{
			ID:              chunkID,
			KnowledgeBaseID: doc.KnowledgeBaseID,
			DocumentID:      doc.ID,
			Ordinal:         chunk.Ordinal,
			HeadingPath:     append([]string(nil), chunk.HeadingPath...),
			StartOffset:     chunk.StartOffset,
			EndOffset:       chunk.EndOffset,
			ContentHash:     chunk.ContentHash,
			VectorID:        string(chunkID),
			TokenEstimate:   len([]rune(chunk.Text)),
			CreatedAt:       at,
		})
		vectorChunks = append(vectorChunks, vector.ChunkVector{
			ChunkID:         string(chunkID),
			KnowledgeBaseID: string(doc.KnowledgeBaseID),
			DocumentID:      string(doc.ID),
			Ordinal:         chunk.Ordinal,
			Text:            chunk.Text,
			HeadingPath:     append([]string(nil), chunk.HeadingPath...),
			ContentHash:     chunk.ContentHash,
			Metadata: map[string]string{
				"filename": doc.Filename,
			},
			EmbeddingModel: model,
			Vector:         append([]float32(nil), embeddings.Vectors[i]...),
		})
	}
	descriptor, err := s.opts.DescriptorForBase(base)
	if err != nil {
		return fmt.Errorf("%w: build vector descriptor: %v", core.ErrIngestFailed, err)
	}
	upsertStarted := s.clock.Now()
	if err := s.opts.VectorStore.UpsertChunks(ctx, descriptor, vectorChunks); err != nil {
		return fmt.Errorf("%w: upsert vectors: %v", core.ErrVectorStoreUnavailable, err)
	}
	if s.opts.Metrics != nil {
		s.opts.Metrics.RecordVectorUpsert(s.clock.Now().Sub(upsertStarted), len(vectorChunks))
	}
	if err := s.repo.ReplaceChunks(ctx, doc.ID, metadataChunks); err != nil {
		return err
	}
	if err := s.publishKnowledgeDocumentMetadata(ctx, base, doc, at); err != nil {
		return err
	}
	if err := s.publishKnowledgeChunkMetadata(ctx, base, metadataChunks, at); err != nil {
		return err
	}
	if err := s.updateDocumentStatus(ctx, payload.DocumentID, core.DocumentIndexed, "", at); err != nil {
		return err
	}
	if err := s.repo.UpdateIngestJobStatus(ctx, payload.JobID, core.JobCompleted, "", at); err != nil {
		return err
	}
	return nil
}

func (s *Service) publishKnowledgeDocumentMetadata(ctx context.Context, base core.KnowledgeBase, doc core.KnowledgeDocument, at time.Time) error {
	if s == nil || s.opts.DocumentStatusSink == nil {
		return nil
	}
	sink, ok := s.opts.DocumentStatusSink.(KnowledgeDocumentMetadataSink)
	if !ok || sink == nil {
		return nil
	}
	revision, err := s.nextKnowledgeDocumentStatusEventRevision(ctx, doc.ID)
	if err != nil {
		return err
	}
	return sink.PublishKnowledgeDocumentMetadata(ctx, graphprotocol.DomainEvent{
		ID:         "knowledge-document-metadata:" + string(doc.ID) + ":rev-" + strconv.FormatInt(revision, 10),
		TenantID:   documentStatusTenantID(base),
		Type:       "knowledge.document.metadata.updated",
		StreamID:   string(doc.ID),
		Version:    revision,
		OccurredAt: at,
		Payload: map[string]any{
			"id":                   string(doc.ID),
			"knowledge_base_id":    string(doc.KnowledgeBaseID),
			"title":                doc.Filename,
			"source_uri":           knowledgeDocumentSourceURI(base, doc),
			"provider":             string(base.EffectiveProvider()),
			"provider_document_id": doc.ProviderDocumentID,
			"content_type":         doc.ContentType,
			"size_bytes":           doc.SizeBytes,
			"checksum":             doc.Checksum,
			"status":               string(core.DocumentIndexed),
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge.ingest"},
	})
}

func (s *Service) publishKnowledgeChunkMetadata(ctx context.Context, base core.KnowledgeBase, chunks []core.KnowledgeChunk, at time.Time) error {
	if s == nil || s.opts.DocumentStatusSink == nil {
		return nil
	}
	sink, ok := s.opts.DocumentStatusSink.(KnowledgeChunkMetadataSink)
	if !ok || sink == nil {
		return nil
	}
	for _, chunk := range chunks {
		revision, err := s.nextKnowledgeChunkEventRevision(ctx, chunk.ID)
		if err != nil {
			return err
		}
		if err := sink.PublishKnowledgeChunkMetadata(ctx, graphprotocol.DomainEvent{
			ID:         "knowledge-chunk-metadata:" + string(chunk.ID) + ":rev-" + strconv.FormatInt(revision, 10),
			TenantID:   documentStatusTenantID(base),
			Type:       "knowledge.chunk.metadata.updated",
			StreamID:   string(chunk.ID),
			Version:    revision,
			OccurredAt: at,
			Payload: map[string]any{
				"id":                string(chunk.ID),
				"knowledge_base_id": string(chunk.KnowledgeBaseID),
				"document_id":       string(chunk.DocumentID),
				"ordinal":           chunk.Ordinal,
				"heading_path":      append([]string(nil), chunk.HeadingPath...),
				"start_offset":      chunk.StartOffset,
				"end_offset":        chunk.EndOffset,
				"content_hash":      chunk.ContentHash,
				"vector_id":         chunk.VectorID,
				"token_estimate":    chunk.TokenEstimate,
			},
			PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge.ingest"},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) fail(ctx context.Context, payload DocumentIngestPayload, cause error) error {
	message := cause.Error()
	now := s.clock.Now()
	_ = s.updateDocumentStatus(ctx, payload.DocumentID, core.DocumentFailed, message, now)
	_ = s.repo.UpdateIngestJobStatus(ctx, payload.JobID, core.JobFailed, message, now)
	return cause
}

func (s *Service) updateDocumentStatus(ctx context.Context, documentID core.KnowledgeDocumentID, status core.DocumentStatus, message string, at time.Time) error {
	if err := s.repo.UpdateDocumentStatus(ctx, documentID, status, message, at); err != nil {
		return err
	}
	if s == nil || s.opts.DocumentStatusSink == nil {
		return nil
	}
	doc, err := s.repo.GetDocument(ctx, documentID)
	if err != nil {
		return err
	}
	base, err := s.repo.GetKnowledgeBase(ctx, doc.KnowledgeBaseID)
	if err != nil {
		return err
	}
	revision, err := s.nextKnowledgeDocumentStatusEventRevision(ctx, doc.ID)
	if err != nil {
		return err
	}
	return s.opts.DocumentStatusSink.PublishKnowledgeDocumentStatus(ctx, graphprotocol.DomainEvent{
		ID:         "knowledge-document-status:" + string(doc.ID) + ":" + string(status) + ":rev-" + strconv.FormatInt(revision, 10),
		TenantID:   documentStatusTenantID(base),
		Type:       "knowledge.document.status.updated",
		StreamID:   string(doc.ID),
		Version:    revision,
		OccurredAt: at,
		Payload: map[string]any{
			"id":     string(doc.ID),
			"status": string(status),
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge.ingest"},
	})
}

func (s *Service) nextKnowledgeDocumentStatusEventRevision(ctx context.Context, documentID core.KnowledgeDocumentID) (int64, error) {
	if s == nil || s.repo == nil {
		return 0, fmt.Errorf("%w: ingest service dependency missing", core.ErrDependencyUnavailable)
	}
	return s.repo.NextEventRevision(ctx, knowledgeDocumentStatusEventStreamID(documentID))
}

func (s *Service) nextKnowledgeChunkEventRevision(ctx context.Context, chunkID core.KnowledgeChunkID) (int64, error) {
	if s == nil || s.repo == nil {
		return 0, fmt.Errorf("%w: ingest service dependency missing", core.ErrDependencyUnavailable)
	}
	return s.repo.NextEventRevision(ctx, knowledgeChunkEventStreamID(chunkID))
}

func knowledgeDocumentStatusEventStreamID(documentID core.KnowledgeDocumentID) string {
	return "knowledge_document:" + string(documentID)
}

func knowledgeChunkEventStreamID(chunkID core.KnowledgeChunkID) string {
	return "knowledge_chunk:" + string(chunkID)
}

func documentStatusTenantID(base core.KnowledgeBase) string {
	if base.OwnerID != "" {
		return base.OwnerID
	}
	return string(base.ID)
}

func knowledgeDocumentSourceURI(base core.KnowledgeBase, doc core.KnowledgeDocument) string {
	providerName := base.EffectiveProvider()
	switch providerName {
	case core.ProviderPageIndex:
		if doc.ProviderDocumentID != "" {
			return "pageindex://document/" + doc.ProviderDocumentID
		}
	case core.ProviderLocal:
		if doc.ArtifactID != "" {
			return "local://artifact/" + doc.ArtifactID
		}
	}
	if doc.ArtifactID != "" {
		return "artifact://" + doc.ArtifactID
	}
	return ""
}

func (s *Service) failReindex(ctx context.Context, payload ReindexBasePayload, cause error) error {
	message := cause.Error()
	now := s.clock.Now()
	_ = s.repo.UpdateIngestJobStatus(ctx, payload.JobID, core.JobFailed, message, now)
	return cause
}

func isSupportedDocumentType(supported []string, contentType, filename string) bool {
	if len(supported) == 0 {
		return true
	}
	allowed := make(map[string]struct{}, len(supported))
	for _, item := range supported {
		item = normalizeContentType(item)
		if item != "" {
			allowed[item] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return true
	}
	if _, ok := allowed[normalizeContentType(contentType)]; ok {
		return true
	}
	if inferred := contentTypeFromFilename(filename); inferred != "" {
		_, ok := allowed[inferred]
		return ok
	}
	return false
}

func normalizeContentType(contentType string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
}

func contentTypeFromFilename(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".txt", ".text":
		return "text/plain"
	case ".md", ".markdown":
		return "text/markdown"
	case ".html", ".htm":
		return "text/html"
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	default:
		return ""
	}
}

type maxBytesReader struct {
	reader    io.Reader
	maxBytes  int64
	bytesRead int64
	atLimit   bool
}

func (r *maxBytesReader) Read(p []byte) (int, error) {
	if r.maxBytes <= 0 {
		return r.reader.Read(p)
	}
	remaining := r.maxBytes - r.bytesRead
	if remaining <= 0 {
		if r.atLimit {
			var probe [1]byte
			n, err := r.reader.Read(probe[:])
			if n > 0 {
				return 0, fmt.Errorf("%w: document exceeds max_document_bytes", core.ErrInvalid)
			}
			return 0, err
		}
		return 0, fmt.Errorf("%w: document exceeds max_document_bytes", core.ErrInvalid)
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := r.reader.Read(p)
	r.bytesRead += int64(n)
	if r.bytesRead > r.maxBytes {
		return n, fmt.Errorf("%w: document exceeds max_document_bytes", core.ErrInvalid)
	}
	r.atLimit = r.bytesRead == r.maxBytes
	return n, err
}
