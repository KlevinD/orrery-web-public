package ingest_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/ingest"
	"orrery_v2/internal/knowledge/testkit"
	"orrery_v2/internal/knowledge/vector/jsonstore"
	"orrery_v2/internal/platform/document/chunker/slidingwindow"
	doccore "orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/document/extractor"
	"orrery_v2/internal/platform/document/extractor/markdown"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestTopicNamesMatchSpec(t *testing.T) {
	if ingest.TopicIngestDocument != "knowledge.ingest_document" ||
		ingest.TopicReindexBase != "knowledge.reindex_base" ||
		ingest.TopicDeleteBase != "knowledge.delete_base" ||
		ingest.TopicDeleteDocument != "knowledge.delete_document" {
		t.Fatalf("topic names mismatch")
	}
}

func TestLockNameForDocumentIngest(t *testing.T) {
	if got := ingest.LockName(core.KnowledgeBaseID("kb-1"), "ingest"); got != "knowledge:kb-1:ingest" {
		t.Fatalf("lock name = %q", got)
	}
}

func TestHandlerFailsWhenPipelineDependenciesAreMissing(t *testing.T) {
	repo := testkit.NewFakeRepository()
	clock := testkit.NewFakeClock(time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC))
	doc := core.KnowledgeDocument{
		ID:              "doc-1",
		KnowledgeBaseID: "kb-1",
		ArtifactID:      "artifact-1",
		Filename:        "notes.md",
		Status:          core.DocumentUploaded,
	}
	if err := repo.SaveDocument(context.Background(), doc); err != nil {
		t.Fatalf("save doc: %v", err)
	}
	job := core.KnowledgeIngestJob{ID: "job-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Topic: ingest.TopicIngestDocument, Status: core.JobQueued}
	if err := repo.SaveIngestJob(context.Background(), job); err != nil {
		t.Fatalf("save job: %v", err)
	}
	service := ingest.NewService(repo, clock, ingest.Options{})

	err := service.HandleDocumentIngest(context.Background(), ingest.DocumentIngestPayload{JobID: "job-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1"})
	if !errors.Is(err, core.ErrDependencyUnavailable) {
		t.Fatalf("handle ingest error = %v, want dependency unavailable", err)
	}
	gotDoc, err := repo.GetDocument(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	if gotDoc.Status != core.DocumentFailed || gotDoc.ErrorMessage == "" {
		t.Fatalf("expected failed document, got %#v", gotDoc)
	}
	gotJob, err := repo.GetIngestJob(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if gotJob.Status != core.JobFailed || gotJob.ErrorMessage == "" {
		t.Fatalf("expected failed job, got %#v", gotJob)
	}
}

func TestHandlerFailureUpdatesDocumentAndJobStatus(t *testing.T) {
	repo := testkit.NewFakeRepository()
	clock := testkit.NewFakeClock(time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC))
	statusSink := &recordingDocumentStatusSink{}
	if err := repo.SaveKnowledgeBase(context.Background(), core.KnowledgeBase{
		ID:            "kb-1",
		Name:          "Docs",
		OwnerType:     core.OwnerOrganization,
		OwnerID:       "org-1",
		Visibility:    core.VisibilityScoped,
		VectorBackend: core.VectorBackendLanceDB,
		ChunkPolicy:   doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1},
		Status:        core.KnowledgeActive,
	}); err != nil {
		t.Fatalf("save base: %v", err)
	}
	doc := core.KnowledgeDocument{ID: "doc-1", KnowledgeBaseID: "kb-1", ArtifactID: "artifact-1", Filename: "notes.md", Status: core.DocumentUploaded}
	_ = repo.SaveDocument(context.Background(), doc)
	job := core.KnowledgeIngestJob{ID: "job-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Topic: ingest.TopicIngestDocument, Status: core.JobQueued}
	_ = repo.SaveIngestJob(context.Background(), job)
	service := ingest.NewService(repo, clock, ingest.Options{
		BeforeChunk: func(context.Context, ingest.DocumentIngestPayload) error {
			return errors.New("extract failed")
		},
		DocumentStatusSink: statusSink,
	})

	err := service.HandleDocumentIngest(context.Background(), ingest.DocumentIngestPayload{JobID: "job-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1"})
	if err == nil {
		t.Fatal("expected handler error")
	}
	gotDoc, _ := repo.GetDocument(context.Background(), "doc-1")
	if gotDoc.Status != core.DocumentFailed || gotDoc.ErrorMessage == "" {
		t.Fatalf("expected failed doc, got %#v", gotDoc)
	}
	gotJob, _ := repo.GetIngestJob(context.Background(), "job-1")
	if gotJob.Status != core.JobFailed || gotJob.ErrorMessage == "" {
		t.Fatalf("expected failed job, got %#v", gotJob)
	}
	assertDocumentStatusSequence(t, statusSink.events, "doc-1", []core.DocumentStatus{core.DocumentParsing, core.DocumentFailed})
}

func TestHandlerKeepsDocumentStatusRevisionsAcrossInstances(t *testing.T) {
	repo := testkit.NewFakeRepository()
	clock := testkit.NewFakeClock(time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC))
	statusSink := &recordingDocumentStatusSink{}
	if err := repo.SaveKnowledgeBase(context.Background(), core.KnowledgeBase{
		ID:            "kb-1",
		Name:          "Docs",
		OwnerType:     core.OwnerOrganization,
		OwnerID:       "org-1",
		Visibility:    core.VisibilityScoped,
		VectorBackend: core.VectorBackendLanceDB,
		ChunkPolicy:   doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1},
		Status:        core.KnowledgeActive,
	}); err != nil {
		t.Fatalf("save base: %v", err)
	}
	_ = repo.SaveDocument(context.Background(), core.KnowledgeDocument{ID: "doc-1", KnowledgeBaseID: "kb-1", ArtifactID: "artifact-1", Filename: "notes.md", Status: core.DocumentUploaded})
	_ = repo.SaveIngestJob(context.Background(), core.KnowledgeIngestJob{ID: "job-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Topic: ingest.TopicIngestDocument, Status: core.JobQueued})

	first := ingest.NewService(repo, clock, ingest.Options{DocumentStatusSink: statusSink})
	payload := ingest.DocumentIngestPayload{JobID: "job-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1"}
	if err := first.HandleDocumentIngest(context.Background(), payload); err == nil {
		t.Fatal("first ingest should fail without a full pipeline")
	}
	firstParsingEventID := knowledgeDocumentStatusEventID(t, statusSink.events, "doc-1", string(core.DocumentParsing))

	_ = repo.SaveIngestJob(context.Background(), core.KnowledgeIngestJob{ID: "job-2", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Topic: ingest.TopicIngestDocument, Status: core.JobQueued})
	second := ingest.NewService(repo, clock, ingest.Options{DocumentStatusSink: statusSink})
	payload.JobID = "job-2"
	if err := second.HandleDocumentIngest(context.Background(), payload); err == nil {
		t.Fatal("second ingest should fail without a full pipeline")
	}
	secondParsingEventID := knowledgeDocumentStatusEventID(t, statusSink.events[2:], "doc-1", string(core.DocumentParsing))

	if firstParsingEventID == secondParsingEventID {
		t.Fatalf("status events from separate service instances reused the same id %q", firstParsingEventID)
	}
	if !strings.HasSuffix(firstParsingEventID, ":rev-1") || !strings.HasSuffix(secondParsingEventID, ":rev-3") {
		t.Fatalf("status event ids = %q and %q, want rev-1 then rev-3", firstParsingEventID, secondParsingEventID)
	}
}

func TestHandlerIndexesDocument(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore(t.TempDir())
	clock := testkit.NewFakeClock(time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC))
	metrics := &fakeIngestMetrics{}
	statusSink := &recordingDocumentStatusSink{}
	base := core.KnowledgeBase{
		ID:                  "kb-1",
		Name:                "Docs",
		OwnerType:           core.OwnerOrganization,
		OwnerID:             "org-1",
		Visibility:          core.VisibilityScoped,
		VectorBackend:       core.VectorBackendLanceDB,
		VectorStorePath:     t.TempDir(),
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 3,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1, PreserveHeads: true},
		Status:              core.KnowledgeActive,
	}
	if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	doc := core.KnowledgeDocument{
		ID:              "doc-1",
		KnowledgeBaseID: "kb-1",
		ArtifactID:      "artifact-1",
		Filename:        "notes.md",
		ContentType:     "text/markdown",
		Status:          core.DocumentUploaded,
	}
	if err := repo.SaveDocument(context.Background(), doc); err != nil {
		t.Fatalf("save doc: %v", err)
	}
	job := core.KnowledgeIngestJob{ID: "job-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Topic: ingest.TopicIngestDocument, Status: core.JobQueued}
	if err := repo.SaveIngestJob(context.Background(), job); err != nil {
		t.Fatalf("save job: %v", err)
	}
	service := ingest.NewService(repo, clock, ingest.Options{
		Artifacts: fakeArtifactReader{content: "# Mission\nBootstrap the PM mission."},
		Extractors: extractor.NewRegistry(
			markdown.Extractor{},
		),
		Chunker: slidingwindow.New(),
		Embedding: testkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{
			Model:      "test-embedding",
			Dimensions: 3,
			Vectors:    [][]float32{{1, 0, 0}},
		}},
		VectorStore: vectorStore,
		DescriptorForBase: func(base core.KnowledgeBase) (ingest.VectorDescriptor, error) {
			return jsonstore.DescriptorForKnowledgeBase(vectorStore.Root, base.ID, base.EmbeddingDimensions)
		},
		Metrics:            metrics,
		DocumentStatusSink: statusSink,
	})

	if err := service.HandleDocumentIngest(context.Background(), ingest.DocumentIngestPayload{JobID: "job-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1"}); err != nil {
		t.Fatalf("handle ingest: %v", err)
	}
	gotDoc, err := repo.GetDocument(context.Background(), "doc-1")
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	if gotDoc.Status != core.DocumentIndexed || gotDoc.IndexedAt.IsZero() {
		t.Fatalf("expected indexed doc, got %#v", gotDoc)
	}
	gotJob, err := repo.GetIngestJob(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if gotJob.Status != core.JobCompleted {
		t.Fatalf("expected completed job, got %#v", gotJob)
	}
	chunks, err := repo.ListChunks(context.Background(), core.ChunkQuery{DocumentID: "doc-1"})
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks) != 1 || chunks[0].ID != "doc-1:0" || chunks[0].ContentHash == "" {
		t.Fatalf("chunks mismatch: %#v", chunks)
	}
	if len(vectorStore.Upserts) != 1 || len(vectorStore.Upserts[0].Chunks) != 1 {
		t.Fatalf("vector upserts mismatch: %#v", vectorStore.Upserts)
	}
	upserted := vectorStore.Upserts[0].Chunks[0]
	if upserted.ChunkID != "doc-1:0" || upserted.DocumentID != "doc-1" || !strings.Contains(upserted.Text, "Bootstrap") {
		t.Fatalf("upserted chunk mismatch: %#v", upserted)
	}
	if metrics.vectorUpsertCount != 1 || metrics.vectorChunkCount != 1 {
		t.Fatalf("vector upsert metrics mismatch: %#v", metrics)
	}
	assertDocumentStatusSequence(t, statusSink.events, "doc-1", []core.DocumentStatus{
		core.DocumentParsing,
		core.DocumentChunking,
		core.DocumentEmbedding,
		core.DocumentIndexed,
	})
	documentMetadataIndex := knowledgeDocumentMetadataEventIndex(t, statusSink.events, "doc-1")
	chunkMetadataIndex := knowledgeChunkMetadataEventIndex(t, statusSink.events, "doc-1:0")
	indexedStatusIndex := knowledgeDocumentStatusEventIndex(t, statusSink.events, "doc-1", string(core.DocumentIndexed))
	if !(documentMetadataIndex < chunkMetadataIndex && chunkMetadataIndex < indexedStatusIndex) {
		t.Fatalf("metadata event order = document:%d chunk:%d indexed:%d events=%+v", documentMetadataIndex, chunkMetadataIndex, indexedStatusIndex, statusSink.events)
	}
}

func TestHandlerFailsUnsupportedDocumentTypeWithStableError(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore(t.TempDir())
	clock := testkit.NewFakeClock(time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC))
	base := core.KnowledgeBase{
		ID:                  "kb-1",
		Name:                "Docs",
		OwnerType:           core.OwnerOrganization,
		OwnerID:             "org-1",
		Visibility:          core.VisibilityScoped,
		VectorBackend:       core.VectorBackendLanceDB,
		VectorStorePath:     t.TempDir(),
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 3,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1, PreserveHeads: true},
		Status:              core.KnowledgeActive,
	}
	if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	doc := core.KnowledgeDocument{
		ID:              "doc-unsupported",
		KnowledgeBaseID: "kb-1",
		ArtifactID:      "artifact-unsupported",
		Filename:        "notes.bin",
		ContentType:     "application/octet-stream",
		Status:          core.DocumentUploaded,
	}
	if err := repo.SaveDocument(context.Background(), doc); err != nil {
		t.Fatalf("save doc: %v", err)
	}
	job := core.KnowledgeIngestJob{ID: "job-unsupported", KnowledgeBaseID: "kb-1", DocumentID: doc.ID, Topic: ingest.TopicIngestDocument, Status: core.JobQueued}
	if err := repo.SaveIngestJob(context.Background(), job); err != nil {
		t.Fatalf("save job: %v", err)
	}
	service := ingest.NewService(repo, clock, ingest.Options{
		Artifacts: fakeArtifactReader{content: "raw bytes"},
		Extractors: extractor.NewRegistry(
			markdown.Extractor{},
		),
		Chunker: slidingwindow.New(),
		Embedding: testkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{
			Model:      "test-embedding",
			Dimensions: 3,
			Vectors:    [][]float32{{1, 0, 0}},
		}},
		VectorStore: vectorStore,
		DescriptorForBase: func(base core.KnowledgeBase) (ingest.VectorDescriptor, error) {
			return jsonstore.DescriptorForKnowledgeBase(vectorStore.Root, base.ID, base.EmbeddingDimensions)
		},
		SupportedContentTypes: []string{"text/markdown"},
	})

	err := service.HandleDocumentIngest(context.Background(), ingest.DocumentIngestPayload{JobID: job.ID, KnowledgeBaseID: base.ID, DocumentID: doc.ID})
	if !errors.Is(err, core.ErrDocumentUnsupported) {
		t.Fatalf("unsupported ingest error = %v, want document unsupported", err)
	}
	gotDoc, _ := repo.GetDocument(context.Background(), doc.ID)
	if gotDoc.Status != core.DocumentFailed || !strings.Contains(gotDoc.ErrorMessage, "knowledge document unsupported") {
		t.Fatalf("unsupported doc status = %#v", gotDoc)
	}
	gotJob, _ := repo.GetIngestJob(context.Background(), job.ID)
	if gotJob.Status != core.JobFailed || !strings.Contains(gotJob.ErrorMessage, "knowledge document unsupported") {
		t.Fatalf("unsupported job status = %#v", gotJob)
	}
	if len(vectorStore.Upserts) != 0 {
		t.Fatalf("unsupported document should not upsert vectors: %#v", vectorStore.Upserts)
	}
}

func TestHandlerFailsWhenArtifactExceedsConfiguredMaxDocumentBytes(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore(t.TempDir())
	clock := testkit.NewFakeClock(time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC))
	base := core.KnowledgeBase{
		ID:                  "kb-1",
		Name:                "Docs",
		OwnerType:           core.OwnerOrganization,
		OwnerID:             "org-1",
		Visibility:          core.VisibilityScoped,
		VectorBackend:       core.VectorBackendLanceDB,
		VectorStorePath:     t.TempDir(),
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 3,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1, PreserveHeads: true},
		Status:              core.KnowledgeActive,
	}
	if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	doc := core.KnowledgeDocument{
		ID:              "doc-oversize",
		KnowledgeBaseID: "kb-1",
		ArtifactID:      "artifact-oversize",
		Filename:        "oversize.md",
		ContentType:     "text/markdown",
		SizeBytes:       4,
		Status:          core.DocumentUploaded,
	}
	if err := repo.SaveDocument(context.Background(), doc); err != nil {
		t.Fatalf("save doc: %v", err)
	}
	job := core.KnowledgeIngestJob{ID: "job-oversize", KnowledgeBaseID: "kb-1", DocumentID: doc.ID, Topic: ingest.TopicIngestDocument, Status: core.JobQueued}
	if err := repo.SaveIngestJob(context.Background(), job); err != nil {
		t.Fatalf("save job: %v", err)
	}
	service := ingest.NewService(repo, clock, ingest.Options{
		Artifacts:  fakeArtifactReader{content: "12345"},
		Extractors: extractor.NewRegistry(markdown.Extractor{}),
		Chunker:    slidingwindow.New(),
		Embedding: testkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{
			Model:      "test-embedding",
			Dimensions: 3,
			Vectors:    [][]float32{{1, 0, 0}},
		}},
		VectorStore: vectorStore,
		DescriptorForBase: func(base core.KnowledgeBase) (ingest.VectorDescriptor, error) {
			return jsonstore.DescriptorForKnowledgeBase(vectorStore.Root, base.ID, base.EmbeddingDimensions)
		},
		MaxDocumentBytes: 4,
	})

	err := service.HandleDocumentIngest(context.Background(), ingest.DocumentIngestPayload{JobID: job.ID, KnowledgeBaseID: base.ID, DocumentID: doc.ID})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("oversize ingest error = %v, want knowledge invalid", err)
	}
	gotDoc, _ := repo.GetDocument(context.Background(), doc.ID)
	if gotDoc.Status != core.DocumentFailed || !strings.Contains(gotDoc.ErrorMessage, "max_document_bytes") {
		t.Fatalf("oversize doc status = %#v", gotDoc)
	}
	if len(vectorStore.Upserts) != 0 {
		t.Fatalf("oversize document should not upsert vectors: %#v", vectorStore.Upserts)
	}
}

func TestHandlerAllowsArtifactAtConfiguredMaxDocumentBytes(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore(t.TempDir())
	clock := testkit.NewFakeClock(time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC))
	base := core.KnowledgeBase{
		ID:                  "kb-1",
		Name:                "Docs",
		OwnerType:           core.OwnerOrganization,
		OwnerID:             "org-1",
		Visibility:          core.VisibilityScoped,
		VectorBackend:       core.VectorBackendLanceDB,
		VectorStorePath:     t.TempDir(),
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 3,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1, PreserveHeads: true},
		Status:              core.KnowledgeActive,
	}
	if err := repo.SaveKnowledgeBase(context.Background(), base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	doc := core.KnowledgeDocument{
		ID:              "doc-at-limit",
		KnowledgeBaseID: "kb-1",
		ArtifactID:      "artifact-at-limit",
		Filename:        "at-limit.md",
		ContentType:     "text/markdown",
		SizeBytes:       4,
		Status:          core.DocumentUploaded,
	}
	if err := repo.SaveDocument(context.Background(), doc); err != nil {
		t.Fatalf("save doc: %v", err)
	}
	job := core.KnowledgeIngestJob{ID: "job-at-limit", KnowledgeBaseID: "kb-1", DocumentID: doc.ID, Topic: ingest.TopicIngestDocument, Status: core.JobQueued}
	if err := repo.SaveIngestJob(context.Background(), job); err != nil {
		t.Fatalf("save job: %v", err)
	}
	service := ingest.NewService(repo, clock, ingest.Options{
		Artifacts:  fakeArtifactReader{content: "1234"},
		Extractors: extractor.NewRegistry(markdown.Extractor{}),
		Chunker:    slidingwindow.New(),
		Embedding: testkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{
			Model:      "test-embedding",
			Dimensions: 3,
			Vectors:    [][]float32{{1, 0, 0}},
		}},
		VectorStore: vectorStore,
		DescriptorForBase: func(base core.KnowledgeBase) (ingest.VectorDescriptor, error) {
			return jsonstore.DescriptorForKnowledgeBase(vectorStore.Root, base.ID, base.EmbeddingDimensions)
		},
		MaxDocumentBytes: 4,
	})

	if err := service.HandleDocumentIngest(context.Background(), ingest.DocumentIngestPayload{JobID: job.ID, KnowledgeBaseID: base.ID, DocumentID: doc.ID}); err != nil {
		t.Fatalf("handle ingest at limit: %v", err)
	}
	gotDoc, _ := repo.GetDocument(context.Background(), doc.ID)
	if gotDoc.Status != core.DocumentIndexed {
		t.Fatalf("expected indexed doc at limit, got %#v", gotDoc)
	}
}

func TestHandlerRetryReplacesDocumentChunks(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore(t.TempDir())
	clock := testkit.NewFakeClock(time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC))
	base := core.KnowledgeBase{
		ID:                  "kb-1",
		Name:                "Docs",
		OwnerType:           core.OwnerOrganization,
		OwnerID:             "org-1",
		Visibility:          core.VisibilityScoped,
		VectorBackend:       core.VectorBackendLanceDB,
		VectorStorePath:     t.TempDir(),
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 3,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1, PreserveHeads: true},
		Status:              core.KnowledgeActive,
	}
	_ = repo.SaveKnowledgeBase(context.Background(), base)
	_ = repo.SaveDocument(context.Background(), core.KnowledgeDocument{
		ID:              "doc-1",
		KnowledgeBaseID: "kb-1",
		ArtifactID:      "artifact-1",
		Filename:        "notes.md",
		ContentType:     "text/markdown",
		Status:          core.DocumentUploaded,
	})
	_ = repo.SaveIngestJob(context.Background(), core.KnowledgeIngestJob{ID: "job-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Topic: ingest.TopicIngestDocument, Status: core.JobQueued})
	service := ingest.NewService(repo, clock, ingest.Options{
		Artifacts:  fakeArtifactReader{content: "# Mission\nBootstrap the PM mission."},
		Extractors: extractor.NewRegistry(markdown.Extractor{}),
		Chunker:    slidingwindow.New(),
		Embedding: testkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{
			Model:      "test-embedding",
			Dimensions: 3,
			Vectors:    [][]float32{{1, 0, 0}},
		}},
		VectorStore: vectorStore,
		DescriptorForBase: func(base core.KnowledgeBase) (ingest.VectorDescriptor, error) {
			return jsonstore.DescriptorForKnowledgeBase(vectorStore.Root, base.ID, base.EmbeddingDimensions)
		},
	})

	payload := ingest.DocumentIngestPayload{JobID: "job-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1"}
	if err := service.HandleDocumentIngest(context.Background(), payload); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	_ = repo.SaveIngestJob(context.Background(), core.KnowledgeIngestJob{ID: "job-2", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Topic: ingest.TopicIngestDocument, Status: core.JobQueued})
	payload.JobID = "job-2"
	if err := service.HandleDocumentIngest(context.Background(), payload); err != nil {
		t.Fatalf("retry ingest: %v", err)
	}

	chunks, err := repo.ListChunks(context.Background(), core.ChunkQuery{DocumentID: "doc-1"})
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks) != 1 || chunks[0].ID != "doc-1:0" {
		t.Fatalf("expected replacement without duplicates, got %#v", chunks)
	}
	if len(vectorStore.Upserts) != 2 || vectorStore.Upserts[0].Chunks[0].ChunkID != vectorStore.Upserts[1].Chunks[0].ChunkID {
		t.Fatalf("expected deterministic vector upserts, got %#v", vectorStore.Upserts)
	}
}

func TestHandlerReindexesCurrentDocuments(t *testing.T) {
	repo := testkit.NewFakeRepository()
	vectorStore := testkit.NewFakeVectorStore(t.TempDir())
	clock := testkit.NewFakeClock(time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC))
	statusSink := &recordingDocumentStatusSink{}
	base := core.KnowledgeBase{
		ID:                  "kb-1",
		Name:                "Docs",
		OwnerType:           core.OwnerOrganization,
		OwnerID:             "org-1",
		Visibility:          core.VisibilityScoped,
		VectorBackend:       core.VectorBackendLanceDB,
		VectorStorePath:     t.TempDir(),
		EmbeddingModel:      "test-embedding",
		EmbeddingDimensions: 3,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 80, OverlapRunes: 0, MinRunes: 1, PreserveHeads: true},
		Status:              core.KnowledgeActive,
	}
	_ = repo.SaveKnowledgeBase(context.Background(), base)
	_ = repo.SaveDocument(context.Background(), core.KnowledgeDocument{ID: "doc-1", KnowledgeBaseID: "kb-1", ArtifactID: "artifact-1", Filename: "one.md", ContentType: "text/markdown", Status: core.DocumentIndexed})
	_ = repo.SaveDocument(context.Background(), core.KnowledgeDocument{ID: "doc-2", KnowledgeBaseID: "kb-1", ArtifactID: "artifact-2", Filename: "two.md", ContentType: "text/markdown", Status: core.DocumentUploaded})
	_ = repo.SaveIngestJob(context.Background(), core.KnowledgeIngestJob{ID: "job-reindex", KnowledgeBaseID: "kb-1", Topic: ingest.TopicReindexBase, Status: core.JobQueued})
	service := ingest.NewService(repo, clock, ingest.Options{
		Artifacts: multiArtifactReader{content: map[string]string{
			"artifact-1": "# One\nFirst document.",
			"artifact-2": "# Two\nSecond document.",
		}},
		Extractors: extractor.NewRegistry(markdown.Extractor{}),
		Chunker:    slidingwindow.New(),
		Embedding: testkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{
			Model:      "test-embedding",
			Dimensions: 3,
			Vectors:    [][]float32{{1, 0, 0}},
		}},
		VectorStore: vectorStore,
		DescriptorForBase: func(base core.KnowledgeBase) (ingest.VectorDescriptor, error) {
			return jsonstore.DescriptorForKnowledgeBase(vectorStore.Root, base.ID, base.EmbeddingDimensions)
		},
		DocumentStatusSink: statusSink,
	})

	if err := service.HandleReindexBase(context.Background(), ingest.ReindexBasePayload{JobID: "job-reindex", KnowledgeBaseID: "kb-1"}); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	job, _ := repo.GetIngestJob(context.Background(), "job-reindex")
	if job.Status != core.JobCompleted {
		t.Fatalf("expected completed reindex job, got %#v", job)
	}
	chunks, err := repo.ListChunks(context.Background(), core.ChunkQuery{KnowledgeBaseID: "kb-1"})
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected chunks for current documents, got %#v", chunks)
	}
	if len(vectorStore.DeletedDocuments) != 2 || len(vectorStore.Upserts) != 2 {
		t.Fatalf("expected vector rebuild per document, deletes=%#v upserts=%#v", vectorStore.DeletedDocuments, vectorStore.Upserts)
	}
	wantStatuses := []core.DocumentStatus{
		core.DocumentParsing,
		core.DocumentChunking,
		core.DocumentEmbedding,
		core.DocumentIndexed,
	}
	assertDocumentStatusSequence(t, statusSink.events, "doc-1", wantStatuses)
	assertDocumentStatusSequence(t, statusSink.events, "doc-2", wantStatuses)
}

type fakeArtifactReader struct {
	content string
}

func (r fakeArtifactReader) GetReader(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(r.content)), nil
}

type multiArtifactReader struct {
	content map[string]string
}

func (r multiArtifactReader) GetReader(_ context.Context, id string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(r.content[id])), nil
}

type fakeIngestMetrics struct {
	vectorUpsertCount int
	vectorChunkCount  int
}

func (m *fakeIngestMetrics) RecordVectorUpsert(_ time.Duration, chunks int) {
	m.vectorUpsertCount++
	m.vectorChunkCount += chunks
}

type recordingDocumentStatusSink struct {
	events []graphprotocol.DomainEvent
}

func (s *recordingDocumentStatusSink) PublishKnowledgeDocumentMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.events = append(s.events, event)
	return nil
}

func (s *recordingDocumentStatusSink) PublishKnowledgeDocumentStatus(ctx context.Context, event graphprotocol.DomainEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.events = append(s.events, event)
	return nil
}

func (s *recordingDocumentStatusSink) PublishKnowledgeChunkMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.events = append(s.events, event)
	return nil
}

func assertDocumentStatusSequence(t *testing.T, events []graphprotocol.DomainEvent, documentID string, want []core.DocumentStatus) {
	t.Helper()
	var got []core.DocumentStatus
	seenIDs := map[string]bool{}
	for _, event := range events {
		if event.Type != "knowledge.document.status.updated" || event.Payload["id"] != documentID {
			continue
		}
		if event.ID == "" {
			t.Fatalf("status event id is empty: %#v", event)
		}
		if seenIDs[event.ID] {
			t.Fatalf("duplicate status event id %q in %+v", event.ID, events)
		}
		seenIDs[event.ID] = true
		got = append(got, core.DocumentStatus(event.Payload["status"].(string)))
		if event.TenantID != "org-1" {
			t.Fatalf("event tenant = %q, want org-1: %#v", event.TenantID, event)
		}
		if event.StreamID != documentID {
			t.Fatalf("event stream id = %q, want %q: %#v", event.StreamID, documentID, event)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("status sequence for %s = %+v, want %+v; events=%+v", documentID, got, want, events)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("status sequence for %s = %+v, want %+v", documentID, got, want)
		}
	}
}

func knowledgeDocumentStatusEventID(t *testing.T, events []graphprotocol.DomainEvent, documentID string, wantStatus string) string {
	t.Helper()
	for _, event := range events {
		if event.Type == "knowledge.document.status.updated" &&
			event.Payload["id"] == documentID &&
			event.Payload["status"] == wantStatus {
			if event.ID == "" {
				t.Fatalf("status event id is empty: %+v", event)
			}
			return event.ID
		}
	}
	t.Fatalf("knowledge document status event %s/%s missing in %+v", documentID, wantStatus, events)
	return ""
}

func knowledgeDocumentMetadataEventIndex(t *testing.T, events []graphprotocol.DomainEvent, documentID string) int {
	t.Helper()
	for i, event := range events {
		if event.Type == "knowledge.document.metadata.updated" && event.Payload["id"] == documentID {
			if event.Payload["knowledge_base_id"] == "" || event.Payload["title"] == "" {
				t.Fatalf("document metadata payload incomplete: %+v", event)
			}
			return i
		}
	}
	t.Fatalf("knowledge document metadata event %s missing in %+v", documentID, events)
	return -1
}

func knowledgeChunkMetadataEventIndex(t *testing.T, events []graphprotocol.DomainEvent, chunkID string) int {
	t.Helper()
	for i, event := range events {
		if event.Type == "knowledge.chunk.metadata.updated" && event.Payload["id"] == chunkID {
			if event.Payload["document_id"] == "" || event.Payload["content_hash"] == "" {
				t.Fatalf("chunk metadata payload incomplete: %+v", event)
			}
			return i
		}
	}
	t.Fatalf("knowledge chunk metadata event %s missing in %+v", chunkID, events)
	return -1
}

func knowledgeDocumentStatusEventIndex(t *testing.T, events []graphprotocol.DomainEvent, documentID string, wantStatus string) int {
	t.Helper()
	for i, event := range events {
		if event.Type == "knowledge.document.status.updated" &&
			event.Payload["id"] == documentID &&
			event.Payload["status"] == wantStatus {
			return i
		}
	}
	t.Fatalf("knowledge document status event %s/%s missing in %+v", documentID, wantStatus, events)
	return -1
}
