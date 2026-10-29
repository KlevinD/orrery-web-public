package knowledgeadmin

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/knowledge/store"
	"orrery_v2/internal/knowledge/testkit"
	doccore "orrery_v2/internal/platform/document/core"
)

type catalogTestSource struct {
	documents []provider.FolderDocument
	err       error
}

type catalogTestRepository struct {
	store.Repository
	saveErr   error
	deleteErr error
	saves     int
	deletes   int
}

type blockingCatalogSource struct {
	documents []provider.FolderDocument
	entered   chan struct{}
	release   chan struct{}
	mu        sync.Mutex
	active    int
	maxActive int
}

func (s *blockingCatalogSource) ListFolderDocuments(context.Context, provider.FolderDocumentsRequest) ([]provider.FolderDocument, error) {
	s.mu.Lock()
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()
	s.entered <- struct{}{}
	<-s.release
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
	return append([]provider.FolderDocument(nil), s.documents...), nil
}

func (r *catalogTestRepository) SaveDocument(ctx context.Context, document core.KnowledgeDocument) error {
	r.saves++
	if r.saveErr != nil {
		return r.saveErr
	}
	return r.Repository.SaveDocument(ctx, document)
}

func (r *catalogTestRepository) DeleteDocumentMetadata(ctx context.Context, id core.KnowledgeDocumentID) error {
	r.deletes++
	if r.deleteErr != nil {
		return r.deleteErr
	}
	return r.Repository.DeleteDocumentMetadata(ctx, id)
}

func (s *catalogTestSource) ListFolderDocuments(context.Context, provider.FolderDocumentsRequest) ([]provider.FolderDocument, error) {
	return append([]provider.FolderDocument(nil), s.documents...), s.err
}

func TestCatalogReconcilerCreatesNestedPathProjection(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	repo := testkit.NewFakeRepository()
	base := catalogTestBase()
	if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	source := &catalogTestSource{documents: []provider.FolderDocument{{
		DocumentID: "pageindex-doc-1", FolderID: base.ProviderResourceID,
		Filename: "rendered-title.md", ContentType: "text/markdown", SizeBytes: 42,
		Checksum: "sha256:abc", Status: string(core.DocumentIndexed),
		KnowledgeKey: "04-Resources/Plan/Orrery/design.md",
		Source:       "digital-brain://04-Resources/Plan/Orrery/design.md",
		Title:        "Design",
	}}}
	reconciler := newCatalogReconcilerWithSource(repo, source, testkit.NewFakeClock(now))

	result, err := reconciler.Reconcile(ctx, base)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if result.Created != 1 || result.Updated != 0 || result.Unchanged != 0 || result.Removed != 0 || result.Rejected != 0 {
		t.Fatalf("result = %+v", result)
	}
	page, err := repo.ListDocuments(ctx, core.DocumentQuery{KnowledgeBaseID: base.ID})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("documents = %+v err=%v", page, err)
	}
	document := page.Items[0]
	if document.ID != remoteCatalogDocumentID(base.ID, "pageindex-doc-1") || document.ProviderDocumentID != "pageindex-doc-1" || document.Filename != "design.md" {
		t.Fatalf("document identity = %+v", document)
	}
	if document.ContentType != "text/markdown" || document.SizeBytes != 42 || document.Checksum != "sha256:abc" || document.Status != core.DocumentIndexed {
		t.Fatalf("document metadata = %+v", document)
	}
	wantMetadata := map[string]string{
		"catalog_origin": "pageindex_remote",
		"folder_path":    "04-Resources/Plan/Orrery",
		"relative_path":  "04-Resources/Plan/Orrery/design.md",
		"source_uri":     "digital-brain://04-Resources/Plan/Orrery/design.md",
		"title":          "Design",
	}
	for key, want := range wantMetadata {
		if document.ProviderMetadata[key] != want {
			t.Fatalf("provider metadata %s = %q, want %q; all=%+v", key, document.ProviderMetadata[key], want, document.ProviderMetadata)
		}
	}
	if !document.CreatedAt.Equal(now) || !document.UpdatedAt.Equal(now) || !document.IndexedAt.Equal(now) {
		t.Fatalf("document timestamps = %+v", document)
	}
}

func TestCatalogReconcilerIsIdempotentAndUpdatesChangedMetadata(t *testing.T) {
	ctx := context.Background()
	firstTime := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	repo := testkit.NewFakeRepository()
	base := catalogTestBase()
	if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	source := &catalogTestSource{documents: []provider.FolderDocument{catalogRemoteDocument(base)}}
	first := newCatalogReconcilerWithSource(repo, source, testkit.NewFakeClock(firstTime))
	if result, err := first.Reconcile(ctx, base); err != nil || result.Created != 1 {
		t.Fatalf("first reconcile = %+v err=%v", result, err)
	}

	secondRepo := &catalogTestRepository{Repository: repo}
	second := newCatalogReconcilerWithSource(secondRepo, source, testkit.NewFakeClock(firstTime.Add(time.Hour)))
	result, err := second.Reconcile(ctx, base)
	if err != nil || result.Unchanged != 1 || result.Created != 0 || result.Updated != 0 || secondRepo.saves != 0 || secondRepo.deletes != 0 {
		t.Fatalf("second reconcile = %+v err=%v saves=%d deletes=%d", result, err, secondRepo.saves, secondRepo.deletes)
	}

	source.documents[0].Checksum = "sha256:changed"
	source.documents[0].SizeBytes = 84
	result, err = second.Reconcile(ctx, base)
	if err != nil || result.Updated != 1 || result.Created != 0 || secondRepo.saves != 1 {
		t.Fatalf("changed reconcile = %+v err=%v saves=%d", result, err, secondRepo.saves)
	}
	page, _ := repo.ListDocuments(ctx, core.DocumentQuery{KnowledgeBaseID: base.ID})
	if len(page.Items) != 1 || page.Items[0].Checksum != "sha256:changed" || page.Items[0].SizeBytes != 84 || !page.Items[0].CreatedAt.Equal(firstTime) || !page.Items[0].UpdatedAt.Equal(firstTime.Add(time.Hour)) {
		t.Fatalf("updated document = %+v", page.Items)
	}
}

func TestCatalogReconcilerPreservesCatalogOnRemoteOrLocalFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	base := catalogTestBase()

	t.Run("remote listing", func(t *testing.T) {
		repo := testkit.NewFakeRepository()
		seedCatalogDocument(t, repo, base, "stale-remote", "Old/kept.md", true, now)
		reconciler := newCatalogReconcilerWithSource(repo, &catalogTestSource{err: errors.New("pageindex unavailable")}, testkit.NewFakeClock(now))
		if _, err := reconciler.Reconcile(ctx, base); err == nil {
			t.Fatal("Reconcile succeeded")
		}
		assertCatalogDocumentCount(t, repo, base.ID, 1)
	})

	t.Run("local upsert", func(t *testing.T) {
		baseRepo := testkit.NewFakeRepository()
		seedCatalogDocument(t, baseRepo, base, "stale-remote", "Old/kept.md", true, now)
		repo := &catalogTestRepository{Repository: baseRepo, saveErr: errors.New("write failed")}
		reconciler := newCatalogReconcilerWithSource(repo, &catalogTestSource{documents: []provider.FolderDocument{catalogRemoteDocument(base)}}, testkit.NewFakeClock(now))
		if _, err := reconciler.Reconcile(ctx, base); err == nil {
			t.Fatal("Reconcile succeeded")
		}
		if repo.deletes != 0 {
			t.Fatalf("deletes = %d", repo.deletes)
		}
		assertCatalogDocumentCount(t, baseRepo, base.ID, 1)
	})
}

func TestCatalogReconcilerRemovesOnlyMissingManagedMetadata(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	repo := testkit.NewFakeRepository()
	base := catalogTestBase()
	seedCatalogDocument(t, repo, base, "managed-remote", "Old/managed.md", true, now)
	seedCatalogDocument(t, repo, base, "unmanaged-remote", "Old/unmanaged.md", false, now)
	reconciler := newCatalogReconcilerWithSource(repo, &catalogTestSource{}, testkit.NewFakeClock(now))

	result, err := reconciler.Reconcile(ctx, base)
	if err != nil || result.Removed != 1 || result.Created != 0 || result.Updated != 0 || result.Unchanged != 0 {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	page, _ := repo.ListDocuments(ctx, core.DocumentQuery{KnowledgeBaseID: base.ID})
	if len(page.Items) != 1 || page.Items[0].ProviderDocumentID != "unmanaged-remote" {
		t.Fatalf("remaining documents = %+v", page.Items)
	}
}

func TestCatalogReconcilerRejectsDuplicateRemoteIdentityBeforeWriting(t *testing.T) {
	for _, mutate := range []func(*provider.FolderDocument){
		func(document *provider.FolderDocument) { document.DocumentID = "pageindex-doc-1" },
		func(document *provider.FolderDocument) { document.KnowledgeKey = "04-Resources/Plan/Orrery/design.md" },
	} {
		ctx := context.Background()
		repo := &catalogTestRepository{Repository: testkit.NewFakeRepository()}
		base := catalogTestBase()
		first := catalogRemoteDocument(base)
		second := first
		second.DocumentID = "pageindex-doc-2"
		second.KnowledgeKey = "04-Resources/Plan/Orrery/other.md"
		mutate(&second)
		reconciler := newCatalogReconcilerWithSource(repo, &catalogTestSource{documents: []provider.FolderDocument{first, second}}, testkit.NewFakeClock(time.Now()))
		if _, err := reconciler.Reconcile(ctx, base); err == nil {
			t.Fatal("Reconcile succeeded")
		}
		if repo.saves != 0 || repo.deletes != 0 {
			t.Fatalf("writes occurred: saves=%d deletes=%d", repo.saves, repo.deletes)
		}
	}
}

func TestCatalogReconcilerSerializesConcurrentRuns(t *testing.T) {
	ctx := context.Background()
	repo := testkit.NewFakeRepository()
	base := catalogTestBase()
	source := &blockingCatalogSource{
		documents: []provider.FolderDocument{catalogRemoteDocument(base)},
		entered:   make(chan struct{}, 2),
		release:   make(chan struct{}),
	}
	reconciler := newCatalogReconcilerWithSource(repo, source, testkit.NewFakeClock(time.Now()))
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			_, _ = reconciler.Reconcile(ctx, base)
		}()
	}
	<-source.entered
	earlySecond := false
	select {
	case <-source.entered:
		earlySecond = true
	case <-time.After(50 * time.Millisecond):
	}
	close(source.release)
	wait.Wait()
	if earlySecond || source.maxActive != 1 {
		t.Fatalf("concurrent catalog runs overlapped: early=%v max_active=%d", earlySecond, source.maxActive)
	}
}

func TestNormalizeCatalogKnowledgeKey(t *testing.T) {
	relative, folder, filename, err := normalizeCatalogKnowledgeKey("04-Resources/Plan/Orrery/design.md")
	if err != nil || relative != "04-Resources/Plan/Orrery/design.md" || folder != "04-Resources/Plan/Orrery" || filename != "design.md" {
		t.Fatalf("normalized = %q %q %q err=%v", relative, folder, filename, err)
	}
	relative, folder, filename, err = normalizeCatalogKnowledgeKey("README.md")
	if err != nil || relative != "README.md" || folder != "" || filename != "README.md" {
		t.Fatalf("root normalized = %q %q %q err=%v", relative, folder, filename, err)
	}
}

func TestNormalizeCatalogKnowledgeKeyRejectsUnsafePaths(t *testing.T) {
	for _, value := range []string{
		"", "/absolute.md", "../escape.md", "a/../../escape.md", "a//empty.md",
		"a/./dot.md", `a\backslash.md`, "https://host/doc.md", "C:/drive.md", "folder/",
	} {
		t.Run(value, func(t *testing.T) {
			if _, _, _, err := normalizeCatalogKnowledgeKey(value); err == nil {
				t.Fatalf("normalizeCatalogKnowledgeKey(%q) succeeded", value)
			}
		})
	}
}

func catalogTestBase() core.KnowledgeBase {
	return core.KnowledgeBase{
		ID: "kb-personal", Name: "Personal", OwnerType: core.OwnerHuman, OwnerID: "human-1",
		Visibility: core.VisibilityPrivate, Provider: core.ProviderPageIndex,
		ProviderResourceID: "folder-personal", ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
		Status: core.KnowledgeActive, CreatedAt: time.Date(2026, 8, 25, 7, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 8, 25, 7, 0, 0, 0, time.UTC),
	}
}

func catalogRemoteDocument(base core.KnowledgeBase) provider.FolderDocument {
	return provider.FolderDocument{
		DocumentID: "pageindex-doc-1", FolderID: base.ProviderResourceID,
		Filename: "rendered-title.md", ContentType: "text/markdown", SizeBytes: 42,
		Checksum: "sha256:abc", Status: string(core.DocumentIndexed),
		KnowledgeKey: "04-Resources/Plan/Orrery/design.md",
		Source:       "digital-brain://04-Resources/Plan/Orrery/design.md",
		Title:        "Design",
	}
}

func seedCatalogDocument(t *testing.T, repo store.Repository, base core.KnowledgeBase, providerID, relativePath string, managed bool, now time.Time) {
	t.Helper()
	_, folder, filename, err := normalizeCatalogKnowledgeKey(relativePath)
	if err != nil {
		t.Fatalf("normalize seed path: %v", err)
	}
	metadata := map[string]string{"folder_path": folder, "relative_path": relativePath}
	if managed {
		metadata["catalog_origin"] = remoteCatalogOrigin
	}
	if err := repo.SaveDocument(context.Background(), core.KnowledgeDocument{
		ID: remoteCatalogDocumentID(base.ID, providerID), KnowledgeBaseID: base.ID,
		ProviderDocumentID: providerID, ProviderMetadata: metadata, Filename: filename,
		ContentType: "text/markdown", Status: core.DocumentIndexed,
		CreatedAt: now, UpdatedAt: now, IndexedAt: now,
	}); err != nil {
		t.Fatalf("save seed document: %v", err)
	}
}

func assertCatalogDocumentCount(t *testing.T, repo store.Repository, baseID core.KnowledgeBaseID, want int) {
	t.Helper()
	page, err := repo.ListDocuments(context.Background(), core.DocumentQuery{KnowledgeBaseID: baseID})
	if err != nil || len(page.Items) != want {
		t.Fatalf("document count = %d want %d err=%v", len(page.Items), want, err)
	}
}
