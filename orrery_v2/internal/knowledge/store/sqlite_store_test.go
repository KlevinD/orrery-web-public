package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/platform/db"
	doccore "orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/migration"
)

func TestPlatformStoreKnowledgeBaseCRUDAndDefaultUniqueness(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	base := testBase("kb-1")
	base.OwnerType = core.OwnerAgent
	base.OwnerID = "agent-1"
	base.IsDefault = true

	if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	got, err := repo.GetKnowledgeBase(ctx, base.ID)
	if err != nil {
		t.Fatalf("get base: %v", err)
	}
	if got.Name != base.Name || !got.IsDefault {
		t.Fatalf("base mismatch: %#v", got)
	}
	page, err := repo.ListKnowledgeBases(ctx, core.KnowledgeBaseQuery{OwnerType: core.OwnerAgent, OwnerID: "agent-1"})
	if err != nil {
		t.Fatalf("list bases: %v", err)
	}
	if page.Total != 1 || page.Items[0].ID != base.ID {
		t.Fatalf("page mismatch: %#v", page)
	}

	duplicate := testBase("kb-2")
	duplicate.OwnerType = core.OwnerAgent
	duplicate.OwnerID = "agent-1"
	duplicate.IsDefault = true
	if err := repo.SaveKnowledgeBase(ctx, duplicate); !errors.Is(err, core.ErrConflict) {
		t.Fatalf("expected default uniqueness conflict, got %v", err)
	}

	if err := repo.MarkKnowledgeBaseDeleting(ctx, base.ID, now()); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	got, err = repo.GetKnowledgeBase(ctx, base.ID)
	if err != nil {
		t.Fatalf("get deleting base: %v", err)
	}
	if got.Status != core.KnowledgeDeleting {
		t.Fatalf("expected deleting, got %s", got.Status)
	}
	if err := repo.DeleteKnowledgeBaseMetadata(ctx, base.ID); err != nil {
		t.Fatalf("delete base: %v", err)
	}
	if _, err := repo.GetKnowledgeBase(ctx, base.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestPlatformStoreBindings(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	base := testBase("kb-bind")
	if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	binding := core.KnowledgeBinding{
		ID:              "bind-1",
		KnowledgeBaseID: base.ID,
		BoundToType:     core.BindingSubjectAgent,
		BoundToID:       "agent-pm",
		Access:          core.AccessRead,
		CreatedAt:       now(),
	}
	if err := repo.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	bindings, err := repo.ListBindings(ctx, core.BindingQuery{KnowledgeBaseID: base.ID})
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	if len(bindings) != 1 || bindings[0].BoundToID != "agent-pm" {
		t.Fatalf("bindings mismatch: %#v", bindings)
	}
	gotBinding, err := repo.GetBinding(ctx, binding.ID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if gotBinding.ID != binding.ID || gotBinding.KnowledgeBaseID != base.ID {
		t.Fatalf("binding mismatch: %#v", gotBinding)
	}
	if err := repo.DeleteBinding(ctx, binding.ID); err != nil {
		t.Fatalf("delete binding: %v", err)
	}
	bindings, err = repo.ListBindings(ctx, core.BindingQuery{KnowledgeBaseID: base.ID})
	if err != nil {
		t.Fatalf("list bindings after delete: %v", err)
	}
	if len(bindings) != 0 {
		t.Fatalf("expected no bindings, got %#v", bindings)
	}
}

func TestPlatformStoreListsKnowledgeWithoutReadingUnrelatedRecords(t *testing.T) {
	ctx := context.Background()
	dbstore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	repo := store.NewPlatformStore(dbstore)
	base := testBase("kb-bounded-read")
	if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	if _, err := dbstore.Exec(ctx, `
INSERT INTO platform_records (table_name, record_key, payload_json)
VALUES (?, ?, ?)`, "unrelated_records", "broken", "{not-json"); err != nil {
		t.Fatalf("insert unrelated record: %v", err)
	}

	page, err := repo.ListKnowledgeBases(ctx, core.KnowledgeBaseQuery{})
	if err != nil {
		t.Fatalf("list bases: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != base.ID {
		t.Fatalf("page = %#v, want only %s", page, base.ID)
	}
}

func TestPlatformStoreDocumentsChunksAndJobs(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	base := testBase("kb-doc")
	if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	doc := core.KnowledgeDocument{
		ID:              "doc-1",
		KnowledgeBaseID: base.ID,
		ArtifactID:      "artifact-1",
		Filename:        "notes.md",
		ContentType:     "text/markdown",
		SizeBytes:       42,
		Checksum:        "sum",
		Status:          core.DocumentUploaded,
		CreatedAt:       now(),
		UpdatedAt:       now(),
	}
	if err := repo.SaveDocument(ctx, doc); err != nil {
		t.Fatalf("save doc: %v", err)
	}
	if err := repo.UpdateDocumentStatus(ctx, doc.ID, core.DocumentIndexed, "", now()); err != nil {
		t.Fatalf("update doc status: %v", err)
	}
	gotDoc, err := repo.GetDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	if gotDoc.Status != core.DocumentIndexed {
		t.Fatalf("expected indexed doc, got %s", gotDoc.Status)
	}
	docs, err := repo.ListDocuments(ctx, core.DocumentQuery{KnowledgeBaseID: base.ID})
	if err != nil {
		t.Fatalf("list docs: %v", err)
	}
	if docs.Total != 1 || docs.Items[0].ID != doc.ID {
		t.Fatalf("docs mismatch: %#v", docs)
	}

	chunks := []core.KnowledgeChunk{
		{ID: "chunk-1", KnowledgeBaseID: base.ID, DocumentID: doc.ID, Ordinal: 0, ContentHash: "hash-1", VectorID: "vec-1", CreatedAt: now()},
		{ID: "chunk-2", KnowledgeBaseID: base.ID, DocumentID: doc.ID, Ordinal: 1, ContentHash: "hash-2", VectorID: "vec-2", CreatedAt: now()},
	}
	if err := repo.ReplaceChunks(ctx, doc.ID, chunks); err != nil {
		t.Fatalf("replace chunks: %v", err)
	}
	gotChunks, err := repo.ListChunks(ctx, core.ChunkQuery{DocumentID: doc.ID})
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(gotChunks) != 2 || gotChunks[1].Ordinal != 1 {
		t.Fatalf("chunks mismatch: %#v", gotChunks)
	}
	gotChunk, err := repo.GetChunk(ctx, chunks[1].ID)
	if err != nil || gotChunk.ID != chunks[1].ID || gotChunk.KnowledgeBaseID != base.ID {
		t.Fatalf("get chunk = %#v, err=%v", gotChunk, err)
	}
	if _, err := repo.GetChunk(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing chunk error = %v", err)
	}
	batchBases, err := repo.GetKnowledgeBases(ctx, []core.KnowledgeBaseID{base.ID, "missing"})
	if err != nil || len(batchBases) != 1 || batchBases[base.ID].ID != base.ID {
		t.Fatalf("batch bases = %#v, err=%v", batchBases, err)
	}
	batchDocs, err := repo.GetDocuments(ctx, []core.KnowledgeDocumentID{doc.ID, "missing"})
	if err != nil || len(batchDocs) != 1 || batchDocs[doc.ID].ID != doc.ID {
		t.Fatalf("batch documents = %#v, err=%v", batchDocs, err)
	}
	batchChunks, err := repo.GetChunks(ctx, []core.KnowledgeChunkID{chunks[0].ID, chunks[1].ID, "missing"})
	if err != nil || len(batchChunks) != 2 || batchChunks[chunks[1].ID].ID != chunks[1].ID {
		t.Fatalf("batch chunks = %#v, err=%v", batchChunks, err)
	}

	job := core.KnowledgeIngestJob{
		ID:              "job-1",
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
		Topic:           "knowledge.ingest_document",
		Status:          core.JobQueued,
		CreatedAt:       now(),
		UpdatedAt:       now(),
	}
	if err := repo.SaveIngestJob(ctx, job); err != nil {
		t.Fatalf("save job: %v", err)
	}
	if err := repo.UpdateIngestJobStatus(ctx, job.ID, core.JobCompleted, "", now()); err != nil {
		t.Fatalf("update job: %v", err)
	}
	gotJob, err := repo.GetIngestJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if gotJob.Status != core.JobCompleted {
		t.Fatalf("expected completed job, got %s", gotJob.Status)
	}
	reindex := core.KnowledgeIngestJob{
		ID:              "job-reindex",
		KnowledgeBaseID: base.ID,
		Topic:           "knowledge.reindex_base",
		Status:          core.JobQueued,
		CreatedAt:       now().Add(time.Second),
		UpdatedAt:       now().Add(time.Second),
	}
	if err := repo.SaveIngestJob(ctx, reindex); err != nil {
		t.Fatalf("save reindex job: %v", err)
	}
	jobs, err := repo.ListIngestJobs(ctx, core.JobQuery{KnowledgeBaseID: base.ID})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 2 || jobs[0].ID != job.ID || jobs[1].ID != reindex.ID {
		t.Fatalf("jobs mismatch: %#v", jobs)
	}
	docJobs, err := repo.ListIngestJobs(ctx, core.JobQuery{KnowledgeBaseID: base.ID, DocumentID: doc.ID})
	if err != nil {
		t.Fatalf("list doc jobs: %v", err)
	}
	if len(docJobs) != 1 || docJobs[0].ID != job.ID {
		t.Fatalf("document jobs mismatch: %#v", docJobs)
	}
}

func TestPlatformStoreCountsKnowledgeStatuses(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	for _, base := range []core.KnowledgeBase{
		testBase("kb-active"),
		testBaseWithStatus("kb-disabled", core.KnowledgeDisabled),
		testBaseWithStatus("kb-deleting", core.KnowledgeDeleting),
		testBaseWithStatus("kb-deleting-2", core.KnowledgeDeleting),
	} {
		if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
			t.Fatalf("save base %s: %v", base.ID, err)
		}
	}
	baseCounts, err := repo.CountKnowledgeBasesByStatus(ctx)
	if err != nil {
		t.Fatalf("count bases: %v", err)
	}
	if baseCounts[core.KnowledgeActive] != 1 || baseCounts[core.KnowledgeDisabled] != 1 || baseCounts[core.KnowledgeDeleting] != 2 {
		t.Fatalf("base status counts mismatch: %#v", baseCounts)
	}

	for _, doc := range []core.KnowledgeDocument{
		testDocument("doc-uploaded", "kb-active", core.DocumentUploaded),
		testDocument("doc-indexed", "kb-active", core.DocumentIndexed),
		testDocument("doc-failed", "kb-disabled", core.DocumentFailed),
		testDocument("doc-failed-2", "kb-deleting", core.DocumentFailed),
	} {
		if err := repo.SaveDocument(ctx, doc); err != nil {
			t.Fatalf("save doc %s: %v", doc.ID, err)
		}
	}
	docCounts, err := repo.CountDocumentsByStatus(ctx)
	if err != nil {
		t.Fatalf("count documents: %v", err)
	}
	if docCounts[core.DocumentUploaded] != 1 || docCounts[core.DocumentIndexed] != 1 || docCounts[core.DocumentFailed] != 2 {
		t.Fatalf("document status counts mismatch: %#v", docCounts)
	}

	for _, job := range []core.KnowledgeIngestJob{
		testJob("job-queued", "kb-active", core.JobQueued),
		testJob("job-running", "kb-active", core.JobRunning),
		testJob("job-failed", "kb-disabled", core.JobFailed),
		testJob("job-failed-2", "kb-deleting", core.JobFailed),
	} {
		if err := repo.SaveIngestJob(ctx, job); err != nil {
			t.Fatalf("save job %s: %v", job.ID, err)
		}
	}
	jobCounts, err := repo.CountIngestJobsByStatus(ctx)
	if err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if jobCounts[core.JobQueued] != 1 || jobCounts[core.JobRunning] != 1 || jobCounts[core.JobFailed] != 2 {
		t.Fatalf("job status counts mismatch: %#v", jobCounts)
	}
}

func TestPlatformStoreAllocatesPersistentEventRevisions(t *testing.T) {
	ctx := context.Background()
	repo := newMigratedRepo(t)

	first, err := repo.NextEventRevision(ctx, "knowledge_document:doc-1")
	if err != nil {
		t.Fatalf("first revision: %v", err)
	}
	second, err := repo.NextEventRevision(ctx, "knowledge_document:doc-1")
	if err != nil {
		t.Fatalf("second revision: %v", err)
	}
	other, err := repo.NextEventRevision(ctx, "knowledge_document:doc-2")
	if err != nil {
		t.Fatalf("other revision: %v", err)
	}

	if first != 1 || second != 2 || other != 1 {
		t.Fatalf("revisions = %d, %d, %d; want 1, 2, 1", first, second, other)
	}
}

func newMigratedRepo(t *testing.T) store.Repository {
	t.Helper()
	ctx := context.Background()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: t.Name() + "-migrated"},
	}})
	if err != nil {
		t.Fatalf("open db registry: %v", err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	coordinator := migration.NewCoordinator(registry)
	if err := store.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	coreDB, ok := registry.Get(db.RoleCore)
	if !ok || coreDB == nil {
		t.Fatalf("core database missing")
	}
	return store.NewPlatformStore(coreDB)
}

func newRepo(t *testing.T) *store.PlatformStore {
	t.Helper()
	dbstore, err := db.Open(context.Background(), db.Config{Driver: db.DriverMemory, Name: t.Name()})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	return store.NewPlatformStore(dbstore)
}

func testBase(id core.KnowledgeBaseID) core.KnowledgeBase {
	return core.KnowledgeBase{
		ID:            id,
		Name:          "Product Knowledge",
		OwnerType:     core.OwnerOrganization,
		OwnerID:       "org-1",
		Visibility:    core.VisibilityScoped,
		VectorBackend: core.VectorBackendLanceDB,
		ChunkPolicy:   doccore.ChunkPolicy{MaxRunes: 2400, OverlapRunes: 240, MinRunes: 400},
		Status:        core.KnowledgeActive,
		CreatedAt:     now(),
		UpdatedAt:     now(),
	}
}

func testBaseWithStatus(id core.KnowledgeBaseID, status core.KnowledgeStatus) core.KnowledgeBase {
	base := testBase(id)
	base.Status = status
	return base
}

func testDocument(id core.KnowledgeDocumentID, baseID core.KnowledgeBaseID, status core.DocumentStatus) core.KnowledgeDocument {
	return core.KnowledgeDocument{
		ID:              id,
		KnowledgeBaseID: baseID,
		ArtifactID:      "artifact-" + string(id),
		Filename:        string(id) + ".md",
		ContentType:     "text/markdown",
		SizeBytes:       42,
		Checksum:        "sum-" + string(id),
		Status:          status,
		CreatedAt:       now(),
		UpdatedAt:       now(),
	}
}

func testJob(id core.KnowledgeJobID, baseID core.KnowledgeBaseID, status core.JobStatus) core.KnowledgeIngestJob {
	return core.KnowledgeIngestJob{
		ID:              id,
		KnowledgeBaseID: baseID,
		Topic:           "knowledge.ingest_document",
		Status:          status,
		CreatedAt:       now(),
		UpdatedAt:       now(),
	}
}

func now() time.Time {
	return time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
}
