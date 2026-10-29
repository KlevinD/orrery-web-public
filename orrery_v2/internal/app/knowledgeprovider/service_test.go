package knowledgeprovider

import (
	"context"
	"errors"
	"testing"
	"time"

	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/testkit"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

type fakeExternalCatalog struct {
	folders   []ExternalFolder
	documents map[string][]ExternalDocument
	err       error
}

func (f *fakeExternalCatalog) ListFolders(context.Context, Binding, string) ([]ExternalFolder, error) {
	return f.folders, f.err
}

func (f *fakeExternalCatalog) ListDocuments(_ context.Context, _ Binding, _ string, folderID string) ([]ExternalDocument, error) {
	return f.documents[folderID], f.err
}

func TestServiceSyncAdoptsIncludedFoldersIdempotently(t *testing.T) {
	ctx := context.Background()
	store := newTestProviderStore(t, "knowledge-provider-sync")
	binding := Binding{
		ID: "binding-1", Name: "Minvia PageIndex", MCPServerID: "mcp.pageindex", Mode: ModeReadOnly, Status: StatusActive,
		FolderInclude: []string{"minvia-core", "minvia-operations"}, FolderExclude: []string{"MinviaBrain"},
	}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	catalog := &fakeExternalCatalog{
		folders: []ExternalFolder{
			{ID: "legacy", Name: "MinviaBrain"},
			{ID: "core", Name: "minvia-core"},
			{ID: "ops", Name: "minvia-operations"},
		},
		documents: map[string][]ExternalDocument{
			"legacy": {{ID: "legacy-1", FolderID: "legacy", Name: "legacy.md"}},
			"core":   {{ID: "core-1", FolderID: "core", Name: "one.md"}, {ID: "core-2", FolderID: "core", Name: "two.md"}},
			"ops":    {{ID: "ops-1", FolderID: "ops", Name: "three.md"}, {ID: "ops-2", FolderID: "ops", Name: "four.md"}},
		},
	}
	repository := testkit.NewFakeRepository()
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	service := NewService(store, catalog, repository, func() time.Time { return now })

	request := SyncRequest{PrincipalID: "superadmin", OwnerID: "minvia"}
	result, err := service.Sync(ctx, binding.ID, request)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if result.FoldersSynced != 2 || result.DocumentsSynced != 4 {
		t.Fatalf("first sync result: %+v", result)
	}
	second, err := service.Sync(ctx, binding.ID, request)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if second.FoldersSynced != 2 || second.DocumentsSynced != 4 {
		t.Fatalf("second sync result: %+v", second)
	}

	bases, err := repository.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{})
	if err != nil {
		t.Fatalf("list bases: %v", err)
	}
	if bases.Total != 2 {
		t.Fatalf("expected two adopted bases, got %+v", bases.Items)
	}
	totalDocuments := 0
	for _, base := range bases.Items {
		if base.Name == "MinviaBrain" || base.Provider != knowledgecore.ProviderMCP || base.ProviderResourceID == "" {
			t.Fatalf("unexpected adopted base: %+v", base)
		}
		documents, err := repository.ListDocuments(ctx, knowledgecore.DocumentQuery{KnowledgeBaseID: base.ID})
		if err != nil {
			t.Fatalf("list documents: %v", err)
		}
		for _, document := range documents.Items {
			if document.ProviderDocumentID == "" || document.Status != knowledgecore.DocumentIndexed {
				t.Fatalf("unexpected adopted document: %+v", document)
			}
		}
		totalDocuments += documents.Total
	}
	if totalDocuments != 4 {
		t.Fatalf("expected four adopted documents, got %d", totalDocuments)
	}
}

func TestServiceSyncPreservesManagedKnowledgeBaseConfiguration(t *testing.T) {
	ctx := context.Background()
	store := newTestProviderStore(t, "knowledge-provider-preserve-managed-config")
	binding := Binding{
		ID: "binding-1", Name: "Minvia PageIndex", MCPServerID: "mcp.pageindex", Mode: ModeReadOnly, Status: StatusActive,
	}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	catalog := &fakeExternalCatalog{
		folders:   []ExternalFolder{{ID: "projects", Name: "minvia-projects"}},
		documents: map[string][]ExternalDocument{"projects": {{ID: "doc-1", FolderID: "projects", Name: "one.md"}}},
	}
	repository := testkit.NewFakeRepository()
	now := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	service := NewService(store, catalog, repository, func() time.Time { return now })
	request := SyncRequest{PrincipalID: "superadmin", OwnerID: "minvia"}
	if _, err := service.Sync(ctx, binding.ID, request); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	bases, err := repository.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{})
	if err != nil || bases.Total != 1 {
		t.Fatalf("list initial bases: page=%+v err=%v", bases, err)
	}
	managed := bases.Items[0]
	managed.Visibility = knowledgecore.VisibilityPublic
	managed.Provider = knowledgecore.ProviderPageIndex
	managed.ProviderMetadata = map[string]string{"managed": "true"}
	managed.EmbeddingProfileRef = "embedding-managed"
	managed.EmbeddingModel = "managed-model"
	managed.EmbeddingDimensions = 1536
	if err := repository.SaveKnowledgeBase(ctx, managed); err != nil {
		t.Fatalf("save managed base: %v", err)
	}

	catalog.folders[0].Name = "minvia-projects-renamed"
	now = now.Add(time.Hour)
	if _, err := service.Sync(ctx, binding.ID, request); err != nil {
		t.Fatalf("resync: %v", err)
	}
	got, err := repository.GetKnowledgeBase(ctx, managed.ID)
	if err != nil {
		t.Fatalf("get resynced base: %v", err)
	}
	if got.Name != "minvia-projects-renamed" {
		t.Fatalf("external name was not refreshed: %+v", got)
	}
	if got.Visibility != knowledgecore.VisibilityPublic || got.Provider != knowledgecore.ProviderPageIndex || got.ProviderResourceID != "projects" {
		t.Fatalf("managed provider configuration was overwritten: %+v", got)
	}
	if got.ProviderMetadata["managed"] != "true" || got.EmbeddingProfileRef != "embedding-managed" || got.EmbeddingModel != "managed-model" || got.EmbeddingDimensions != 1536 {
		t.Fatalf("managed knowledge settings were overwritten: %+v", got)
	}
}

func TestServiceSyncReusesNativeDocumentByProviderID(t *testing.T) {
	ctx := context.Background()
	store := newTestProviderStore(t, "knowledge-provider-reuse-native-document")
	binding := Binding{
		ID: "binding-1", Name: "Minvia PageIndex", MCPServerID: "mcp.pageindex", Mode: ModeReadOnly, Status: StatusActive,
	}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	catalog := &fakeExternalCatalog{
		folders:   []ExternalFolder{{ID: "projects", Name: "minvia-projects"}},
		documents: map[string][]ExternalDocument{"projects": nil},
	}
	repository := testkit.NewFakeRepository()
	now := time.Date(2026, 8, 31, 3, 0, 0, 0, time.UTC)
	service := NewService(store, catalog, repository, func() time.Time { return now })
	request := SyncRequest{PrincipalID: "superadmin", OwnerID: "minvia"}
	if _, err := service.Sync(ctx, binding.ID, request); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	bases, err := repository.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{})
	if err != nil || bases.Total != 1 {
		t.Fatalf("list initial bases: page=%+v err=%v", bases, err)
	}
	native := knowledgecore.KnowledgeDocument{
		ID:                 "kdoc-orrery-native",
		KnowledgeBaseID:    bases.Items[0].ID,
		ArtifactID:         "artifact-native",
		ProviderDocumentID: "doc-1",
		ProviderMetadata:   map[string]string{"ingest_job_id": "job-native", "ontology_revision": "1"},
		Filename:           "AI-Daily-2026-08-31.md",
		ContentType:        "text/markdown",
		SizeBytes:          1234,
		Checksum:           "sha256:native",
		Status:             knowledgecore.DocumentIndexed,
		CreatedBy:          "A000009",
		CreatedAt:          now,
		UpdatedAt:          now,
		IndexedAt:          now,
	}
	if err := repository.SaveDocument(ctx, native); err != nil {
		t.Fatalf("save native document: %v", err)
	}

	catalog.documents["projects"] = []ExternalDocument{{ID: "doc-1", FolderID: "projects", Name: "external-name.md"}}
	now = now.Add(time.Hour)
	result, err := service.Sync(ctx, binding.ID, request)
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if result.DocumentsSynced != 1 {
		t.Fatalf("resync result: %+v", result)
	}
	documents, err := repository.ListDocuments(ctx, knowledgecore.DocumentQuery{KnowledgeBaseID: native.KnowledgeBaseID})
	if err != nil || documents.Total != 1 {
		t.Fatalf("list documents: page=%+v err=%v", documents, err)
	}
	got := documents.Items[0]
	if got.ID != native.ID || got.ArtifactID != native.ArtifactID || got.Filename != native.Filename || got.Checksum != native.Checksum || got.CreatedBy != native.CreatedBy {
		t.Fatalf("native document was duplicated or overwritten: %+v", got)
	}
	if got.ProviderMetadata["ingest_job_id"] != "job-native" || got.ProviderMetadata["ontology_revision"] != "1" {
		t.Fatalf("native document metadata was overwritten: %+v", got.ProviderMetadata)
	}
	mapping, err := store.FindMapping(ctx, binding.ID, ResourceDocument, "doc-1")
	if err != nil {
		t.Fatalf("find reused document mapping: %v", err)
	}
	if mapping.LocalID != string(native.ID) {
		t.Fatalf("mapping local id = %q, want %q", mapping.LocalID, native.ID)
	}
}

func TestServiceSyncKeepsCatalogAndMarksBindingErrorWhenMCPFails(t *testing.T) {
	ctx := context.Background()
	store := newTestProviderStore(t, "knowledge-provider-sync-failure")
	binding := Binding{ID: "binding-1", Name: "Minvia PageIndex", MCPServerID: "mcp.pageindex", Mode: ModeReadOnly, Status: StatusActive}
	if err := store.SaveBinding(ctx, binding); err != nil {
		t.Fatalf("save binding: %v", err)
	}
	repository := testkit.NewFakeRepository()
	catalog := &fakeExternalCatalog{folders: []ExternalFolder{{ID: "core", Name: "minvia-core"}}, documents: map[string][]ExternalDocument{"core": {{ID: "doc-1", Name: "one.md"}}}}
	service := NewService(store, catalog, repository, time.Now)
	request := SyncRequest{PrincipalID: "superadmin", OwnerID: "minvia"}
	if _, err := service.Sync(ctx, binding.ID, request); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	catalog.err = errors.New("pageindex unavailable")
	if _, err := service.Sync(ctx, binding.ID, request); !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("expected dependency error, got %v", err)
	}
	bases, _ := repository.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{})
	if bases.Total != 1 {
		t.Fatalf("existing catalog was removed: %+v", bases.Items)
	}
	failed, err := store.GetBinding(ctx, binding.ID)
	if err != nil {
		t.Fatalf("get failed binding: %v", err)
	}
	if failed.Status != StatusError || failed.LastError == "" {
		t.Fatalf("binding failure was not recorded: %+v", failed)
	}
}

func newTestProviderStore(t *testing.T, name string) *Store {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: name})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := migration.NewRunner(Migrations()).Up(ctx, database); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return NewStore(database)
}
