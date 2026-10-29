package knowledgeadmin_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/ingest"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/knowledge/testkit"
	doccore "orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestCommandServiceCreatesBaseAndEnsuresVectorDirectory(t *testing.T) {
	fixture := newFixture()
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock)

	dto, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:           "Product Knowledge",
		OwnerType:      string(core.OwnerOrganization),
		OwnerID:        "org-1",
		Visibility:     string(core.VisibilityScoped),
		VectorRoot:     fixture.vector.Root,
		EmbeddingModel: "text-embedding-3-small",
		ChunkPolicy:    doccore.ChunkPolicy{MaxRunes: 2400, OverlapRunes: 240, MinRunes: 400},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	if dto.ID == "" || dto.Name != "Product Knowledge" {
		t.Fatalf("dto mismatch: %#v", dto)
	}
	if len(fixture.vector.Ensured) != 1 {
		t.Fatalf("expected vector ensure, got %#v", fixture.vector.Ensured)
	}
}

func TestCommandServiceInfersOwnerIDFromViewerWhenCreateOmitsBackendID(t *testing.T) {
	fixture := newFixture()
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock)
	tests := []struct {
		name      string
		ownerType core.OwnerType
		viewer    knowledgeadmin.ViewerRef
		wantOwner string
	}{
		{
			name:      "organization",
			ownerType: core.OwnerOrganization,
			viewer: knowledgeadmin.ViewerRef{
				ViewerID:        "admin",
				OrganizationIDs: []string{"org-current"},
				PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage},
			},
			wantOwner: "org-current",
		},
		{
			name:      "position",
			ownerType: core.OwnerPosition,
			viewer: knowledgeadmin.ViewerRef{
				ViewerID:        "admin",
				PositionIDs:     []string{"position-current"},
				PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage},
			},
			wantOwner: "position-current",
		},
		{
			name:      "agent",
			ownerType: core.OwnerAgent,
			viewer: knowledgeadmin.ViewerRef{
				ViewerID:        "admin",
				AgentIDs:        []string{"agent-current"},
				PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage},
			},
			wantOwner: "agent-current",
		},
		{
			name:      "human",
			ownerType: core.OwnerHuman,
			viewer: knowledgeadmin.ViewerRef{
				ViewerID:        "human-current",
				PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage},
			},
			wantOwner: "human-current",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dto, err := service.CreateKnowledgeBase(context.Background(), tt.viewer, knowledgeadmin.CreateKnowledgeBaseCommand{
				Name:        "Inferred " + tt.name,
				OwnerType:   string(tt.ownerType),
				Visibility:  string(core.VisibilityScoped),
				VectorRoot:  fixture.vector.Root,
				Provider:    string(core.ProviderLocal),
				ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
			})
			if err != nil {
				t.Fatalf("create inferred owner base: %v", err)
			}
			if dto.OwnerID != tt.wantOwner {
				t.Fatalf("owner id = %q, want %q", dto.OwnerID, tt.wantOwner)
			}
		})
	}
}

func TestCommandServiceUsesConfiguredDefaultChunkPolicy(t *testing.T) {
	fixture := newFixture()
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithDefaultChunkPolicy(doccore.ChunkPolicy{MaxRunes: 900, OverlapRunes: 90, MinRunes: 30, PreserveHeads: true})

	dto, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Configured Policy",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		VectorRoot: fixture.vector.Root,
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	if dto.ChunkPolicy.MaxRunes != 900 || dto.ChunkPolicy.OverlapRunes != 90 || dto.ChunkPolicy.MinRunes != 30 || !dto.ChunkPolicy.PreserveHeads {
		t.Fatalf("chunk policy = %#v", dto.ChunkPolicy)
	}
}

func TestCommandServiceUsesConfiguredDefaultVectorRoot(t *testing.T) {
	fixture := newFixture()
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithDefaultVectorRoot(fixture.vector.Root)

	dto, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:                "Configured Root",
		OwnerType:           string(core.OwnerOrganization),
		OwnerID:             "org-1",
		Visibility:          string(core.VisibilityScoped),
		EmbeddingDimensions: 2,
		ChunkPolicy:         doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	wantRoot, err := filepath.Abs(fixture.vector.Root)
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	if len(fixture.vector.Ensured) != 1 || fixture.vector.Ensured[0].RootPath != wantRoot {
		t.Fatalf("ensured descriptor = %#v", fixture.vector.Ensured)
	}
	if dto.VectorStorePath == "" || !strings.Contains(dto.VectorStorePath, "kb_"+dto.ID) {
		t.Fatalf("vector store path = %q", dto.VectorStorePath)
	}
}

func TestCommandServiceDeletesBaseAndDropsVectorDirectory(t *testing.T) {
	fixture := newFixture()
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock)
	dto, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Delete Me",
		OwnerType:   string(core.OwnerOrganization),
		OwnerID:     "org-1",
		Visibility:  string(core.VisibilityScoped),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}

	if err := service.DeleteKnowledgeBase(context.Background(), manageViewer(), dto.ID); err != nil {
		t.Fatalf("delete base: %v", err)
	}
	if len(fixture.vector.Dropped) != 1 {
		t.Fatalf("expected vector drop, got %#v", fixture.vector.Dropped)
	}
}

func TestCommandServiceUpdatesAndDisablesBase(t *testing.T) {
	fixture := newFixture()
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock)
	base, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Original",
		OwnerType:   string(core.OwnerOrganization),
		OwnerID:     "org-1",
		Visibility:  string(core.VisibilityScoped),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}

	updated, err := service.UpdateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.UpdateKnowledgeBaseCommand{
		ID:          base.ID,
		Name:        "Updated",
		Description: "new description",
		Visibility:  string(core.VisibilityPrivate),
		Status:      string(core.KnowledgeDisabled),
	})
	if err != nil {
		t.Fatalf("update base: %v", err)
	}
	if updated.Name != "Updated" || updated.Description != "new description" || updated.Visibility != string(core.VisibilityPrivate) || updated.Status != string(core.KnowledgeDisabled) {
		t.Fatalf("updated dto mismatch: %#v", updated)
	}

	partial, err := service.UpdateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.UpdateKnowledgeBaseCommand{
		ID:         base.ID,
		Status:     string(core.KnowledgeActive),
		UpdateMask: []string{"status"},
	})
	if err != nil {
		t.Fatalf("partial update base: %v", err)
	}
	if partial.Description != "new description" || partial.Visibility != string(core.VisibilityPrivate) || partial.Status != string(core.KnowledgeActive) {
		t.Fatalf("partial update should preserve omitted fields: %#v", partial)
	}
}

func TestCommandServiceBindUnbindAndEnsureDefault(t *testing.T) {
	fixture := newFixture()
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock)
	owner := agentOwnerManageViewer("agent-1")
	base, err := service.EnsureDefaultPersonalKnowledgeBase(context.Background(), owner, knowledgeadmin.EnsureDefaultCommand{
		OwnerType:  string(core.OwnerAgent),
		OwnerID:    "agent-1",
		VectorRoot: fixture.vector.Root,
	})
	if err != nil {
		t.Fatalf("ensure default: %v", err)
	}
	again, err := service.EnsureDefaultPersonalKnowledgeBase(context.Background(), owner, knowledgeadmin.EnsureDefaultCommand{
		OwnerType:  string(core.OwnerAgent),
		OwnerID:    "agent-1",
		VectorRoot: fixture.vector.Root,
	})
	if err != nil {
		t.Fatalf("ensure default again: %v", err)
	}
	if base.ID != again.ID {
		t.Fatalf("default ensure not idempotent: %s %s", base.ID, again.ID)
	}

	binding, err := service.BindKnowledgeBase(context.Background(), owner, knowledgeadmin.BindKnowledgeBaseCommand{
		KnowledgeBaseID: base.ID,
		BoundToType:     string(core.BindingSubjectHuman),
		BoundToID:       "human-1",
		Access:          string(core.AccessRead),
	})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if binding.ID == "" || binding.BoundToID != "human-1" {
		t.Fatalf("binding mismatch: %#v", binding)
	}
	if err := service.UnbindKnowledgeBase(context.Background(), owner, binding.ID); err != nil {
		t.Fatalf("unbind: %v", err)
	}
}

func TestCommandServiceCreateDocumentMetadataAndJob(t *testing.T) {
	fixture := newFixture()
	execution := &fakeExecutionSubmitter{}
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).WithExecutionQueue(execution)
	base, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Docs",
		OwnerType:   string(core.OwnerOrganization),
		OwnerID:     "org-1",
		Visibility:  string(core.VisibilityScoped),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}

	doc, err := service.CreateDocumentMetadata(context.Background(), writeViewer(), knowledgeadmin.CreateDocumentCommand{
		KnowledgeBaseID: base.ID,
		ArtifactID:      "artifact-1",
		Filename:        "notes.md",
		ContentType:     "text/markdown",
		SizeBytes:       42,
		Checksum:        "sum",
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	if doc.Status != string(core.DocumentUploaded) || doc.JobID == "" {
		t.Fatalf("document dto mismatch: %#v", doc)
	}
	if len(execution.submitted) != 1 {
		t.Fatalf("expected one execution job, got %#v", execution.submitted)
	}
	submitted := execution.submitted[0]
	if submitted.topic != ingest.TopicIngestDocument || submitted.maxAttempts != 3 {
		t.Fatalf("execution submission mismatch: %#v", submitted)
	}
	var payload ingest.DocumentIngestPayload
	if err := json.Unmarshal(submitted.payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.JobID != core.KnowledgeJobID(doc.JobID) || payload.KnowledgeBaseID != core.KnowledgeBaseID(base.ID) || payload.DocumentID != core.KnowledgeDocumentID(doc.ID) {
		t.Fatalf("payload mismatch: %#v", payload)
	}
}

func TestCommandServiceDeletesDocumentAndVectorChunks(t *testing.T) {
	fixture := newFixture()
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock)
	base, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Docs",
		OwnerType:   string(core.OwnerOrganization),
		OwnerID:     "org-1",
		Visibility:  string(core.VisibilityScoped),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	doc, err := service.CreateDocumentMetadata(context.Background(), writeViewer(), knowledgeadmin.CreateDocumentCommand{
		KnowledgeBaseID: base.ID,
		ArtifactID:      "artifact-1",
		Filename:        "notes.md",
		ContentType:     "text/markdown",
		SizeBytes:       42,
		Checksum:        "sum",
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	if err := service.DeleteDocument(context.Background(), manageViewer(), knowledgeadmin.DeleteDocumentCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
	}); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	if _, err := fixture.repo.GetDocument(context.Background(), core.KnowledgeDocumentID(doc.ID)); err == nil {
		t.Fatal("expected document metadata deleted")
	}
	if len(fixture.vector.DeletedDocuments) != 1 || fixture.vector.DeletedDocuments[0].DocumentID != doc.ID {
		t.Fatalf("deleted vector docs = %#v", fixture.vector.DeletedDocuments)
	}
}

func TestCommandServiceCreatesReindexJob(t *testing.T) {
	fixture := newFixture()
	execution := &fakeExecutionSubmitter{}
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).WithExecutionQueue(execution)
	base, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Docs",
		OwnerType:   string(core.OwnerOrganization),
		OwnerID:     "org-1",
		Visibility:  string(core.VisibilityScoped),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}

	job, err := service.CreateReindexJob(context.Background(), manageViewer(), knowledgeadmin.ReindexKnowledgeBaseCommand{KnowledgeBaseID: base.ID})
	if err != nil {
		t.Fatalf("create reindex job: %v", err)
	}
	if job.KnowledgeBaseID != base.ID || job.Topic != "knowledge.reindex_base" || job.Status != string(core.JobQueued) {
		t.Fatalf("job mismatch: %#v", job)
	}
	if len(execution.submitted) != 1 {
		t.Fatalf("expected one execution job, got %#v", execution.submitted)
	}
	submitted := execution.submitted[0]
	if submitted.topic != ingest.TopicReindexBase || submitted.maxAttempts != 1 {
		t.Fatalf("execution submission mismatch: %#v", submitted)
	}
	var payload ingest.ReindexBasePayload
	if err := json.Unmarshal(submitted.payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.JobID != core.KnowledgeJobID(job.ID) || payload.KnowledgeBaseID != core.KnowledgeBaseID(base.ID) {
		t.Fatalf("payload mismatch: %#v", payload)
	}
}

func TestCommandServiceReturnsRemotePageIndexReindexJobWithoutSavingLocalIngestJob(t *testing.T) {
	fixture := newFixture()
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
		reindexResult: provider.ReindexKnowledgeBaseResult{
			JobID:          "pi-reindex-123",
			Queued:         true,
			LocalIngestJob: false,
			Supported:      true,
		},
	}
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).WithProviderRegistry(
		provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex),
	)
	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}

	job, err := command.CreateReindexJob(context.Background(), manageViewer(), knowledgeadmin.ReindexKnowledgeBaseCommand{KnowledgeBaseID: base.ID})
	if err != nil {
		t.Fatalf("create pageindex reindex job: %v", err)
	}
	if job.ID != "pi-reindex-123" || job.KnowledgeBaseID != base.ID || job.Topic != ingest.TopicReindexBase || job.Status != string(core.JobQueued) {
		t.Fatalf("remote reindex dto = %#v", job)
	}
	if _, err := fixture.repo.GetIngestJob(context.Background(), core.KnowledgeJobID(job.ID)); err == nil {
		t.Fatal("remote PageIndex reindex job should not be saved as a local ingest job")
	}
}

func TestCommandServiceUploadsDocumentArtifactAndCreatesMetadata(t *testing.T) {
	fixture := newFixture()
	artifacts := &fakeArtifactWriter{}
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).WithArtifactStore(artifacts)
	base, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Docs",
		OwnerType:   string(core.OwnerOrganization),
		OwnerID:     "org-1",
		Visibility:  string(core.VisibilityScoped),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}

	doc, err := service.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "notes.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("# hello"),
	})
	if err != nil {
		t.Fatalf("upload document: %v", err)
	}
	if doc.ArtifactID != "artifact-1" || doc.Filename != "notes.md" || doc.SizeBytes != 7 || doc.Checksum == "" || doc.JobID == "" {
		t.Fatalf("uploaded doc mismatch: %#v", doc)
	}
	if artifacts.name != "notes.md" || artifacts.contentType != "text/markdown" || artifacts.content != "# hello" {
		t.Fatalf("artifact write mismatch: %#v", artifacts)
	}
}

func TestCommandServiceAllowsWriteScopedViewerToUploadGlobalKnowledgeButRestrictsManage(t *testing.T) {
	fixture := newFixture()
	artifacts := &fakeArtifactWriter{}
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).WithArtifactStore(artifacts)
	base, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Global Docs",
		OwnerType:   string(core.OwnerGlobal),
		Visibility:  string(core.VisibilityPublic),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create global base: %v", err)
	}
	writer := knowledgeadmin.ViewerRef{ViewerID: "writer", PermissionScope: []string{knowledgeadmin.PermissionKnowledgeWrite}}

	if _, err := service.UploadDocument(context.Background(), writer, knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "global.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("# global"),
	}); err != nil {
		t.Fatalf("write-scoped upload to global knowledge: %v", err)
	}
	if _, err := service.BindKnowledgeBase(context.Background(), writer, knowledgeadmin.BindKnowledgeBaseCommand{
		KnowledgeBaseID: base.ID,
		BoundToType:     string(core.BindingSubjectHuman),
		BoundToID:       "human-manager",
		Access:          string(core.AccessManage),
	}); !errors.Is(err, knowledgeadmin.ErrPermissionDenied) {
		t.Fatalf("write-scoped global bind error = %v, want permission denied", err)
	}
}

func TestCommandAndSearchServicesUploadAndQueryThroughPageIndexProvider(t *testing.T) {
	fixture := newFixture()
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          int64(len("发布流程必须先完成灰度验收")),
			Status:             core.DocumentIndexed,
		},
		searchResult: provider.SearchResultPage{Results: []provider.SearchResult{{
			KnowledgeBaseID: "kb-1",
			DocumentID:      "pi-doc-456",
			ChunkID:         "chunk-1",
			Score:           0.91,
			Snippet:         "发布流程必须先完成灰度验收，然后通知运营。",
			DocumentName:    "release-handbook.md",
		}}},
	}
	registry := provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).WithProviderRegistry(registry)

	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}
	if base.Provider != string(core.ProviderPageIndex) || base.ProviderResourceID != "folder-123" {
		t.Fatalf("base provider fields = %#v", base)
	}

	doc, err := command.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("发布流程必须先完成灰度验收"),
	})
	if err != nil {
		t.Fatalf("upload pageindex document: %v", err)
	}
	if doc.ProviderDocumentID != "pi-doc-456" || doc.Status != string(core.DocumentIndexed) {
		t.Fatalf("uploaded document provider fields = %#v", doc)
	}
	// Provider results must identify the requested base, not an unrelated fixture ID.
	pageIndex.searchResult.Results[0].KnowledgeBaseID = base.ID

	search := knowledgeadmin.NewSearchService(fixture.repo, fixture.vector, nil).WithProviderRegistry(registry)
	page, err := search.SearchKnowledge(context.Background(), knowledgeadmin.ViewerRef{
		ViewerID:        "reader",
		OrganizationIDs: []string{"org-1"},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	}, knowledgeadmin.SearchKnowledgeCommand{Query: "灰度验收", KnowledgeBaseIDs: []string{base.ID}, Limit: 3})
	if err != nil {
		t.Fatalf("search pageindex document: %v", err)
	}
	if len(page.Results) != 1 ||
		page.Results[0].DocumentID != "pi-doc-456" ||
		page.Results[0].DocumentName != "release-handbook.md" ||
		!strings.Contains(page.Results[0].Snippet, "灰度验收") {
		t.Fatalf("search results = %#v", page.Results)
	}
	if pageIndex.uploadBody != "发布流程必须先完成灰度验收" || pageIndex.searchQuery != "灰度验收" {
		t.Fatalf("provider observed upload=%q search=%q", pageIndex.uploadBody, pageIndex.searchQuery)
	}
}

func TestCommandServicePublishesKnowledgeDocumentMetadataEventAfterPageIndexUpload(t *testing.T) {
	fixture := newFixture()
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          2048,
			Checksum:           "sha256:abc",
			Status:             core.DocumentIndexed,
		},
	}
	metadata := &recordingDocumentMetadataSink{}
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithProviderRegistry(provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)).
		WithDocumentMetadataSink(metadata)

	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}
	doc, err := command.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("release playbook"),
	})
	if err != nil {
		t.Fatalf("upload pageindex document: %v", err)
	}
	if len(metadata.events) != 1 {
		t.Fatalf("metadata events = %#v", metadata.events)
	}
	event := metadata.events[0]
	if event.Type != "knowledge.document.metadata.updated" || event.TenantID == "" || event.Payload["id"] != doc.ID {
		t.Fatalf("event = %#v", event)
	}
	if event.Payload["knowledge_base_id"] != base.ID ||
		event.Payload["provider"] != string(core.ProviderPageIndex) ||
		event.Payload["provider_document_id"] != "pi-doc-456" ||
		event.Payload["title"] != "release-handbook.md" ||
		event.Payload["source_uri"] != "pageindex://folder-123/pi-doc-456" ||
		event.Payload["status"] != string(core.DocumentIndexed) {
		t.Fatalf("payload = %#v", event.Payload)
	}
}

func TestCommandServiceRefreshesAndReprocessesPageIndexDocument(t *testing.T) {
	fixture := newFixture()
	sink := &recordingDocumentMetadataSink{}
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          int64(len("发布流程必须先完成灰度验收")),
			Status:             core.DocumentParsing,
			ProviderMetadata: map[string]string{
				"pageindex_job_id":        "pi-job-upload",
				"pageindex_job_stage":     "queued",
				"pageindex_job_progress":  "0",
				"pageindex_upload_status": "queued",
			},
		},
		statusResult: provider.DocumentStatusResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          int64(len("发布流程必须先完成灰度验收")),
			Status:             core.DocumentIndexed,
			Job: &provider.DocumentProcessingJob{
				JobID:    "pi-job-456",
				Status:   "completed",
				Progress: 100,
				Stage:    "indexed",
			},
		},
		reprocessResult: provider.ReprocessDocumentResult{
			Status: core.DocumentParsing,
			Job: provider.DocumentProcessingJob{
				JobID:    "pi-job-789",
				Status:   "queued",
				Progress: 0,
				Stage:    "queued",
			},
		},
		processingResult: provider.DocumentProcessingResult{
			Filename: "release-handbook.md",
			Status:   core.DocumentIndexed,
			Progress: 100,
			Stage:    "indexed",
			Pages:    []map[string]any{{"page": float64(1), "text": "发布流程必须先完成灰度验收"}},
		},
	}
	registry := provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithProviderRegistry(registry).
		WithDocumentMetadataSink(sink)

	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}
	doc, err := command.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("发布流程必须先完成灰度验收"),
	})
	if err != nil {
		t.Fatalf("upload pageindex document: %v", err)
	}
	if doc.JobID != "pi-job-upload" ||
		doc.ProcessingProgress != 0 ||
		doc.ProcessingStage != "queued" ||
		doc.ProcessingJobStatus != "" {
		t.Fatalf("uploaded pageindex dto = %#v", doc)
	}

	refreshed, err := command.RefreshDocumentStatus(context.Background(), readViewer(), knowledgeadmin.DocumentActionCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
	})
	if err != nil {
		t.Fatalf("refresh status: %v", err)
	}
	if refreshed.Status != string(core.DocumentIndexed) ||
		refreshed.JobID != "pi-job-456" ||
		refreshed.ProcessingProgress != 100 ||
		refreshed.ProcessingStage != "indexed" ||
		refreshed.ProcessingJobStatus != "completed" {
		t.Fatalf("refreshed status = %#v", refreshed)
	}
	stored, err := fixture.repo.GetDocument(context.Background(), core.KnowledgeDocumentID(doc.ID))
	if err != nil {
		t.Fatalf("get stored doc: %v", err)
	}
	if stored.Status != core.DocumentIndexed || pageIndex.statusDoc.ProviderDocumentID != "pi-doc-456" {
		t.Fatalf("stored/status request mismatch: stored=%#v statusDoc=%#v", stored, pageIndex.statusDoc)
	}
	assertKnowledgeDocumentStatusEvent(t, sink.events, doc.ID, string(core.DocumentIndexed))
	firstIndexedEventID := knowledgeDocumentStatusEventID(t, sink.events, doc.ID, string(core.DocumentIndexed))

	reprocessed, err := command.ReprocessDocument(context.Background(), manageViewer(), knowledgeadmin.DocumentActionCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
	})
	if err != nil {
		t.Fatalf("reprocess document: %v", err)
	}
	if reprocessed.Status != string(core.DocumentParsing) ||
		reprocessed.JobID != "pi-job-789" ||
		reprocessed.ProcessingProgress != 0 ||
		reprocessed.ProcessingStage != "queued" ||
		reprocessed.ProcessingJobStatus != "queued" {
		t.Fatalf("reprocessed dto = %#v", reprocessed)
	}
	stored, _ = fixture.repo.GetDocument(context.Background(), core.KnowledgeDocumentID(doc.ID))
	if stored.Status != core.DocumentParsing || pageIndex.reprocessDoc.ProviderDocumentID != "pi-doc-456" {
		t.Fatalf("stored/reprocess request mismatch: stored=%#v reprocessDoc=%#v", stored, pageIndex.reprocessDoc)
	}
	assertKnowledgeDocumentStatusEvent(t, sink.events, doc.ID, string(core.DocumentParsing))

	eventCount := len(sink.events)
	result, err := command.GetDocumentProcessingResult(context.Background(), readViewer(), knowledgeadmin.DocumentProcessingResultCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
		Pages:           "1",
	})
	if err != nil {
		t.Fatalf("processing result: %v", err)
	}
	if result.Status != string(core.DocumentIndexed) ||
		result.Progress != 100 ||
		result.Document.ProcessingProgress != 100 ||
		result.Document.ProcessingStage != "indexed" ||
		len(result.Pages) != 1 {
		t.Fatalf("processing result dto = %#v", result)
	}
	assertKnowledgeDocumentStatusEvent(t, sink.events[eventCount:], doc.ID, string(core.DocumentIndexed))
	secondIndexedEventID := knowledgeDocumentStatusEventID(t, sink.events[eventCount:], doc.ID, string(core.DocumentIndexed))
	if firstIndexedEventID == secondIndexedEventID {
		t.Fatalf("repeated indexed status events should keep distinct evidence ids, both were %q", firstIndexedEventID)
	}
}

func TestCommandServicePublishesPageMetadataAfterProcessingResult(t *testing.T) {
	fixture := newFixture()
	sink := &recordingDocumentMetadataSink{}
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          int64(len("发布流程必须先完成灰度验收")),
			Status:             core.DocumentIndexed,
		},
		processingResult: provider.DocumentProcessingResult{
			Filename: "release-handbook.md",
			Status:   core.DocumentIndexed,
			Progress: 100,
			Stage:    "indexed",
			Pages: []map[string]any{{
				"page":  float64(1),
				"title": "Release",
				"text":  "发布流程必须先完成灰度验收",
			}},
		},
	}
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithProviderRegistry(provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)).
		WithDocumentMetadataSink(sink)
	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}
	doc, err := command.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("发布流程必须先完成灰度验收"),
	})
	if err != nil {
		t.Fatalf("upload pageindex document: %v", err)
	}
	eventCount := len(sink.events)

	if _, err := command.GetDocumentProcessingResult(context.Background(), readViewer(), knowledgeadmin.DocumentProcessingResultCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
		Pages:           "1",
	}); err != nil {
		t.Fatalf("processing result: %v", err)
	}

	var pageEvent graphprotocol.DomainEvent
	for _, event := range sink.events[eventCount:] {
		if event.Type == "knowledge.page.metadata.updated" {
			pageEvent = event
			break
		}
	}
	if pageEvent.ID == "" {
		t.Fatalf("page metadata event missing in %+v", sink.events[eventCount:])
	}
	if pageEvent.TenantID != "org-1" ||
		pageEvent.StreamID != doc.ID ||
		pageEvent.Payload["id"] != doc.ID+":page:1" ||
		pageEvent.Payload["document_id"] != doc.ID ||
		pageEvent.Payload["knowledge_base_id"] != base.ID ||
		pageEvent.Payload["page_number"] != 1 ||
		pageEvent.Payload["provider"] != string(core.ProviderPageIndex) ||
		pageEvent.Payload["provider_document_id"] != "pi-doc-456" ||
		pageEvent.Payload["source_uri"] != "pageindex://folder-123/pi-doc-456#page=1" {
		t.Fatalf("page metadata event = %+v", pageEvent)
	}
	if _, ok := pageEvent.Payload["text"]; ok {
		t.Fatalf("page metadata should not include page text: %+v", pageEvent.Payload)
	}
	if _, ok := pageEvent.Payload["content"]; ok {
		t.Fatalf("page metadata should not include page content: %+v", pageEvent.Payload)
	}
}

func TestCommandServiceReturnsNormalizedProcessingResult(t *testing.T) {
	fixture := newFixture()
	sink := &recordingDocumentMetadataSink{}
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          int64(len("发布流程必须先完成灰度验收")),
			Status:             core.DocumentIndexed,
		},
		processingResult: provider.DocumentProcessingResult{
			Filename: "release-handbook.md",
			Status:   core.DocumentIndexed,
			Progress: 100,
			Stage:    "indexed",
			StructureNodes: []provider.DocumentStructureNode{{
				ProviderNodeID: "root-1",
				Text:           "Release Handbook",
				PageNumber:     1,
				StructurePath:  []string{"Release Handbook"},
				Children: []provider.DocumentStructureNode{{
					ProviderNodeID: "node-7",
					Text:           "Gray release checklist",
					PageNumber:     2,
					StructurePath:  []string{"Release Handbook", "Gray release checklist"},
				}},
			}},
			PagePreviews: []provider.DocumentPagePreview{{
				PageNumber:     2,
				ProviderNodeID: "node-7",
				HeadingPath:    []string{"Release"},
				StructurePath:  []string{"Release Handbook", "Gray release checklist"},
				BBox:           map[string]any{"x": 0.1, "y": 0.2, "width": 0.3, "height": 0.4},
				ImageRef:       "pageindex://folder-123/pi-doc-456#page=2&image=preview",
				Text:           "发布流程必须先完成灰度验收",
				SnippetHash:    "sha256:snippet",
			}},
			Pages: []map[string]any{{"page_index": float64(2), "text": "发布流程必须先完成灰度验收"}},
		},
	}
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithProviderRegistry(provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)).
		WithDocumentMetadataSink(sink)
	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}
	doc, err := command.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("发布流程必须先完成灰度验收"),
	})
	if err != nil {
		t.Fatalf("upload pageindex document: %v", err)
	}

	result, err := command.GetDocumentProcessingResult(context.Background(), readViewer(), knowledgeadmin.DocumentProcessingResultCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
		Pages:           "2",
	})
	if err != nil {
		t.Fatalf("processing result: %v", err)
	}
	if len(result.StructureNodes) != 1 || result.StructureNodes[0].Children[0].ProviderNodeID != "node-7" {
		t.Fatalf("structure nodes dto = %#v", result.StructureNodes)
	}
	if len(result.PagePreviews) != 1 {
		t.Fatalf("page previews dto = %#v", result.PagePreviews)
	}
	page := result.PagePreviews[0]
	if page.PageNumber != 2 || page.ProviderNodeID != "node-7" || page.SnippetHash != "sha256:snippet" || page.Text == "" {
		t.Fatalf("page preview dto = %#v", page)
	}
	if len(result.Pages) != 1 {
		t.Fatalf("raw pages should remain for compatibility: %#v", result.Pages)
	}
}

func TestCommandServicePublishesStructureNodeAnchorsWithoutPagePreviews(t *testing.T) {
	fixture := newFixture()
	sink := &recordingDocumentMetadataSink{}
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          int64(len("发布流程必须先完成灰度验收")),
			Status:             core.DocumentIndexed,
		},
		processingResult: provider.DocumentProcessingResult{
			Filename: "release-handbook.md",
			Status:   core.DocumentIndexed,
			Progress: 100,
			Stage:    "indexed",
			StructureNodes: []provider.DocumentStructureNode{{
				ProviderNodeID: "root-1",
				Text:           "Release Handbook",
				PageNumber:     1,
				HeadingPath:    []string{"Release Handbook"},
				StructurePath:  []string{"Release Handbook"},
				BBox:           map[string]any{"x": 0.1, "y": 0.2, "width": 0.3, "height": 0.4},
				Children: []provider.DocumentStructureNode{{
					ProviderNodeID: "node-7",
					Text:           "Gray release checklist",
					PageNumber:     1,
					HeadingPath:    []string{"Release Handbook", "Gray release checklist"},
					StructurePath:  []string{"Release Handbook", "Gray release checklist"},
					BBox:           map[string]any{"x": 0.2, "y": 0.3, "width": 0.4, "height": 0.5},
				}},
			}},
		},
	}
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithProviderRegistry(provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)).
		WithDocumentMetadataSink(sink)
	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}
	doc, err := command.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("发布流程必须先完成灰度验收"),
	})
	if err != nil {
		t.Fatalf("upload pageindex document: %v", err)
	}
	eventCount := len(sink.events)

	if _, err := command.GetDocumentProcessingResult(context.Background(), readViewer(), knowledgeadmin.DocumentProcessingResultCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
		Pages:           "1",
	}); err != nil {
		t.Fatalf("processing result: %v", err)
	}

	var pageEvents []graphprotocol.DomainEvent
	var anchorEvents []graphprotocol.DomainEvent
	for _, event := range sink.events[eventCount:] {
		switch event.Type {
		case "knowledge.page.metadata.updated":
			pageEvents = append(pageEvents, event)
		case "knowledge.anchor.metadata.updated":
			anchorEvents = append(anchorEvents, event)
		}
	}
	if len(pageEvents) != 1 {
		t.Fatalf("page metadata events = %+v", pageEvents)
	}
	if len(anchorEvents) != 2 {
		t.Fatalf("anchor metadata events = %+v", anchorEvents)
	}
	child := anchorEvents[1]
	if child.Payload["provider_node_id"] != "node-7" ||
		child.Payload["id"] != doc.ID+":page:1:anchor:node-7" ||
		child.Payload["snippet_hash"] == "" ||
		child.Payload["source_uri"] != "pageindex://folder-123/pi-doc-456#page=1&anchor=node-7" {
		t.Fatalf("child structure anchor event = %+v", child)
	}
	if _, ok := child.Payload["text"]; ok {
		t.Fatalf("structure anchor metadata should not include raw text: %+v", child.Payload)
	}
	if _, ok := child.Payload["content"]; ok {
		t.Fatalf("structure anchor metadata should not include content: %+v", child.Payload)
	}
}

func TestCommandServicePublishesDistinctStructureNodeAnchorsFromBBoxFallback(t *testing.T) {
	fixture := newFixture()
	sink := &recordingDocumentMetadataSink{}
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          int64(len("发布流程必须先完成灰度验收")),
			Status:             core.DocumentIndexed,
		},
		processingResult: provider.DocumentProcessingResult{
			Filename: "release-handbook.md",
			Status:   core.DocumentIndexed,
			Progress: 100,
			Stage:    "indexed",
			StructureNodes: []provider.DocumentStructureNode{{
				PageNumber: 1,
				BBox:       map[string]any{"x": 0.1, "y": 0.2, "width": 0.3, "height": 0.4},
			}, {
				PageNumber: 1,
				BBox:       map[string]any{"x": 0.5, "y": 0.6, "width": 0.7, "height": 0.8},
			}},
		},
	}
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithProviderRegistry(provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)).
		WithDocumentMetadataSink(sink)
	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}
	doc, err := command.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("发布流程必须先完成灰度验收"),
	})
	if err != nil {
		t.Fatalf("upload pageindex document: %v", err)
	}
	eventCount := len(sink.events)

	if _, err := command.GetDocumentProcessingResult(context.Background(), readViewer(), knowledgeadmin.DocumentProcessingResultCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
		Pages:           "1",
	}); err != nil {
		t.Fatalf("processing result: %v", err)
	}

	var anchorEvents []graphprotocol.DomainEvent
	for _, event := range sink.events[eventCount:] {
		if event.Type == "knowledge.anchor.metadata.updated" {
			anchorEvents = append(anchorEvents, event)
		}
	}
	if len(anchorEvents) != 2 {
		t.Fatalf("anchor metadata events = %+v", anchorEvents)
	}
	firstID, _ := anchorEvents[0].Payload["id"].(string)
	secondID, _ := anchorEvents[1].Payload["id"].(string)
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatalf("anchor ids should be distinct, got %q and %q", firstID, secondID)
	}
	if !strings.Contains(firstID, ":anchor:bbox-") || !strings.Contains(secondID, ":anchor:bbox-") {
		t.Fatalf("anchor ids should use bbox fallback, got %q and %q", firstID, secondID)
	}
	for _, event := range anchorEvents {
		if event.Payload["provider_node_id"] != "" {
			t.Fatalf("bbox fallback should not invent provider_node_id: %+v", event.Payload)
		}
		if _, ok := event.Payload["text"]; ok {
			t.Fatalf("bbox fallback should not include raw text: %+v", event.Payload)
		}
	}
}

func TestCommandServicePublishesAnchorMetadataAfterProcessingResult(t *testing.T) {
	fixture := newFixture()
	sink := &recordingDocumentMetadataSink{}
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          int64(len("发布流程必须先完成灰度验收")),
			Status:             core.DocumentIndexed,
		},
		processingResult: provider.DocumentProcessingResult{
			Filename: "release-handbook.md",
			Status:   core.DocumentIndexed,
			Progress: 100,
			Stage:    "indexed",
			Pages: []map[string]any{{
				"page":           float64(1),
				"title":          "Release",
				"node_id":        "0007",
				"structure_path": []any{"Handbook", "Release"},
				"bbox":           map[string]any{"x": 0.1, "y": 0.2, "width": 0.3, "height": 0.4},
				"page_image_uri": "pageindex://folder-123/pi-doc-456#page=1&image=preview",
				"text":           "发布流程必须先完成灰度验收",
				"nodes":          []any{map[string]any{"id": "0007", "text": "发布流程必须先完成灰度验收"}},
			}},
		},
	}
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithProviderRegistry(provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)).
		WithDocumentMetadataSink(sink)
	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}
	doc, err := command.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("发布流程必须先完成灰度验收"),
	})
	if err != nil {
		t.Fatalf("upload pageindex document: %v", err)
	}
	eventCount := len(sink.events)

	if _, err := command.GetDocumentProcessingResult(context.Background(), readViewer(), knowledgeadmin.DocumentProcessingResultCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
		Pages:           "1",
	}); err != nil {
		t.Fatalf("processing result: %v", err)
	}

	var anchorEvent graphprotocol.DomainEvent
	for _, event := range sink.events[eventCount:] {
		if event.Type == "knowledge.anchor.metadata.updated" {
			anchorEvent = event
			break
		}
	}
	if anchorEvent.ID == "" {
		t.Fatalf("anchor metadata event missing in %+v", sink.events[eventCount:])
	}
	wantHash := "sha256:" + sha256Hex("发布流程必须先完成灰度验收")
	if anchorEvent.TenantID != "org-1" ||
		anchorEvent.StreamID != doc.ID ||
		anchorEvent.Payload["id"] != doc.ID+":page:1:anchor:0007" ||
		anchorEvent.Payload["page_id"] != doc.ID+":page:1" ||
		anchorEvent.Payload["document_id"] != doc.ID ||
		anchorEvent.Payload["knowledge_base_id"] != base.ID ||
		anchorEvent.Payload["page_number"] != 1 ||
		anchorEvent.Payload["provider"] != string(core.ProviderPageIndex) ||
		anchorEvent.Payload["provider_document_id"] != "pi-doc-456" ||
		anchorEvent.Payload["provider_node_id"] != "0007" ||
		anchorEvent.Payload["snippet_hash"] != wantHash ||
		anchorEvent.Payload["image_ref"] != "pageindex://folder-123/pi-doc-456#page=1&image=preview" ||
		anchorEvent.Payload["source_uri"] != "pageindex://folder-123/pi-doc-456#page=1&anchor=0007" {
		t.Fatalf("anchor metadata event = %+v", anchorEvent)
	}
	for _, key := range []string{"text", "content", "nodes", "tree", "page_image_uri", "image"} {
		if _, ok := anchorEvent.Payload[key]; ok {
			t.Fatalf("anchor metadata should not include raw %s: %+v", key, anchorEvent.Payload)
		}
	}
}

func TestCommandServiceKeepsDocumentEventRevisionsAcrossInstances(t *testing.T) {
	fixture := newFixture()
	sink := &recordingDocumentMetadataSink{}
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          int64(len("发布流程必须先完成灰度验收")),
			Status:             core.DocumentIndexed,
		},
		statusResult: provider.DocumentStatusResult{
			ProviderDocumentID: "pi-doc-456",
			Status:             core.DocumentIndexed,
		},
	}
	registry := provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)
	firstCommand := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithProviderRegistry(registry).
		WithDocumentMetadataSink(sink)

	base, err := firstCommand.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}
	doc, err := firstCommand.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("发布流程必须先完成灰度验收"),
	})
	if err != nil {
		t.Fatalf("upload pageindex document: %v", err)
	}
	uploadedEventID := knowledgeDocumentMetadataEventID(t, sink.events, doc.ID)

	secondCommand := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithProviderRegistry(registry).
		WithDocumentMetadataSink(sink)
	if _, err := secondCommand.RefreshDocumentStatus(context.Background(), readViewer(), knowledgeadmin.DocumentActionCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
	}); err != nil {
		t.Fatalf("refresh status after service restart: %v", err)
	}
	indexedEventID := knowledgeDocumentStatusEventID(t, sink.events, doc.ID, string(core.DocumentIndexed))

	if uploadedEventID == indexedEventID {
		t.Fatalf("events from separate service instances reused the same id %q", uploadedEventID)
	}
	if !strings.HasSuffix(uploadedEventID, ":rev-1") || !strings.HasSuffix(indexedEventID, ":rev-2") {
		t.Fatalf("event ids = %q and %q, want rev-1 then rev-2", uploadedEventID, indexedEventID)
	}
}

func TestCommandServiceDeletesPageIndexDocumentThroughProvider(t *testing.T) {
	fixture := newFixture()
	sink := &recordingDocumentMetadataSink{}
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          int64(len("发布流程必须先完成灰度验收")),
			Status:             core.DocumentIndexed,
		},
	}
	registry := provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithProviderRegistry(registry).
		WithDocumentMetadataSink(sink)

	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}
	doc, err := command.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("发布流程必须先完成灰度验收"),
	})
	if err != nil {
		t.Fatalf("upload pageindex document: %v", err)
	}

	if err := command.DeleteDocument(context.Background(), manageViewer(), knowledgeadmin.DeleteDocumentCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
	}); err != nil {
		t.Fatalf("delete pageindex document: %v", err)
	}
	if pageIndex.deletedDocument.ProviderDocumentID != "pi-doc-456" {
		t.Fatalf("deleted document = %#v", pageIndex.deletedDocument)
	}
	if _, err := fixture.repo.GetDocument(context.Background(), core.KnowledgeDocumentID(doc.ID)); err == nil {
		t.Fatal("expected document metadata deleted")
	}
	assertKnowledgeDocumentStatusEvent(t, sink.events, doc.ID, string(core.DocumentDeleting))
}

func TestCommandServiceKeepsPageIndexDocumentWhenProviderDeleteFails(t *testing.T) {
	fixture := newFixture()
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          int64(len("发布流程必须先完成灰度验收")),
			Status:             core.DocumentIndexed,
		},
		deleteDocumentErr: errors.New("pageindex delete failed"),
	}
	registry := provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).WithProviderRegistry(registry)

	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}
	doc, err := command.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("发布流程必须先完成灰度验收"),
	})
	if err != nil {
		t.Fatalf("upload pageindex document: %v", err)
	}

	err = command.DeleteDocument(context.Background(), manageViewer(), knowledgeadmin.DeleteDocumentCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
	})
	if !errors.Is(err, pageIndex.deleteDocumentErr) {
		t.Fatalf("delete pageindex document error = %v, want provider error", err)
	}
	stored, err := fixture.repo.GetDocument(context.Background(), core.KnowledgeDocumentID(doc.ID))
	if err != nil {
		t.Fatalf("document metadata should remain after provider delete failure: %v", err)
	}
	if stored.Status != core.DocumentIndexed || stored.ProviderDocumentID != "pi-doc-456" {
		t.Fatalf("stored document changed after failed provider delete: %#v", stored)
	}
}

func TestCommandServiceDeletesPageIndexBaseThroughProvider(t *testing.T) {
	fixture := newFixture()
	pageIndex := &fakeKnowledgeProvider{
		name: core.ProviderPageIndex,
		ensureResult: provider.EnsureKnowledgeBaseResult{
			ProviderResourceID: "folder-123",
		},
	}
	registry := provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).WithProviderRegistry(registry)

	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}

	if err := command.DeleteKnowledgeBase(context.Background(), manageViewer(), base.ID); err != nil {
		t.Fatalf("delete pageindex base: %v", err)
	}
	if pageIndex.deletedBase.ID != core.KnowledgeBaseID(base.ID) {
		t.Fatalf("deleted base id = %q, want %q", pageIndex.deletedBase.ID, base.ID)
	}
	if pageIndex.deletedBase.Provider != core.ProviderPageIndex || pageIndex.deletedBase.ProviderResourceID != "folder-123" {
		t.Fatalf("deleted provider fields = %#v", pageIndex.deletedBase)
	}
}

func TestCommandServiceRejectsUploadOverConfiguredMaxDocumentBytes(t *testing.T) {
	fixture := newFixture()
	artifacts := &fakeArtifactWriter{}
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithArtifactStore(artifacts).
		WithIngestPolicy(knowledgeadmin.IngestPolicy{MaxDocumentBytes: 4})
	base, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Docs",
		OwnerType:   string(core.OwnerOrganization),
		OwnerID:     "org-1",
		Visibility:  string(core.VisibilityScoped),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}

	_, err = service.UploadDocument(context.Background(), writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "oversize.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("12345"),
	})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("upload oversize error = %v, want knowledge invalid", err)
	}
	if artifacts.name != "" || artifacts.content != "" {
		t.Fatalf("oversize upload should not write artifact: %#v", artifacts)
	}
}

func TestCommandServiceRejectsDocumentMetadataOverConfiguredMaxDocumentBytes(t *testing.T) {
	fixture := newFixture()
	execution := &fakeExecutionSubmitter{}
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithExecutionQueue(execution).
		WithIngestPolicy(knowledgeadmin.IngestPolicy{MaxDocumentBytes: 4})
	base, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Docs",
		OwnerType:   string(core.OwnerOrganization),
		OwnerID:     "org-1",
		Visibility:  string(core.VisibilityScoped),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}

	_, err = service.CreateDocumentMetadata(context.Background(), writeViewer(), knowledgeadmin.CreateDocumentCommand{
		KnowledgeBaseID: base.ID,
		ArtifactID:      "artifact-oversize",
		Filename:        "oversize.md",
		ContentType:     "text/markdown",
		SizeBytes:       5,
		Checksum:        "sum",
	})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("metadata oversize error = %v, want knowledge invalid", err)
	}
	docs, listErr := fixture.repo.ListDocuments(context.Background(), core.DocumentQuery{KnowledgeBaseID: core.KnowledgeBaseID(base.ID)})
	if listErr != nil {
		t.Fatalf("list documents: %v", listErr)
	}
	if len(docs.Items) != 0 || len(execution.submitted) != 0 {
		t.Fatalf("oversize metadata should not persist doc or submit job, docs=%#v submitted=%#v", docs.Items, execution.submitted)
	}
}

func TestCommandServiceRequiresBaseWriteBindingForNonOwnerUpload(t *testing.T) {
	fixture := newFixture()
	artifacts := &fakeArtifactWriter{}
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).WithArtifactStore(artifacts)
	base, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Private Docs",
		OwnerType:   string(core.OwnerAgent),
		OwnerID:     "agent-owner",
		Visibility:  string(core.VisibilityPrivate),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	writer := knowledgeadmin.ViewerRef{ViewerID: "agent-writer", AgentIDs: []string{"agent-writer"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeWrite}}

	if _, err := service.UploadDocument(context.Background(), writer, knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "notes.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("# denied"),
	}); !errors.Is(err, knowledgeadmin.ErrPermissionDenied) {
		t.Fatalf("upload without binding error = %v, want permission denied", err)
	}
	if _, err := service.BindKnowledgeBase(context.Background(), agentOwnerManageViewer("agent-owner"), knowledgeadmin.BindKnowledgeBaseCommand{
		KnowledgeBaseID: base.ID,
		BoundToType:     string(core.BindingSubjectAgent),
		BoundToID:       "agent-writer",
		Access:          string(core.AccessWrite),
	}); err != nil {
		t.Fatalf("bind writer: %v", err)
	}
	if _, err := service.UploadDocument(context.Background(), writer, knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "notes.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("# allowed"),
	}); err != nil {
		t.Fatalf("upload with write binding: %v", err)
	}
}

func TestCommandServiceRequiresBaseWriteBindingForDocumentMetadata(t *testing.T) {
	fixture := newFixture()
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock)
	base, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Metadata Docs",
		OwnerType:   string(core.OwnerAgent),
		OwnerID:     "agent-owner",
		Visibility:  string(core.VisibilityPrivate),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	writer := knowledgeadmin.ViewerRef{ViewerID: "agent-writer", AgentIDs: []string{"agent-writer"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeWrite}}

	if _, err := service.CreateDocumentMetadata(context.Background(), writer, knowledgeadmin.CreateDocumentCommand{
		KnowledgeBaseID: base.ID,
		ArtifactID:      "artifact-1",
		Filename:        "notes.md",
		ContentType:     "text/markdown",
		SizeBytes:       7,
		Checksum:        "sum",
	}); !errors.Is(err, knowledgeadmin.ErrPermissionDenied) {
		t.Fatalf("metadata without binding error = %v, want permission denied", err)
	}
}

func TestCommandServiceRequiresBaseManageBindingForNonOwnerBind(t *testing.T) {
	fixture := newFixture()
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock)
	base, err := service.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Managed Docs",
		OwnerType:   string(core.OwnerAgent),
		OwnerID:     "agent-owner",
		Visibility:  string(core.VisibilityPrivate),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	manager := knowledgeadmin.ViewerRef{ViewerID: "agent-manager", AgentIDs: []string{"agent-manager"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage}}

	if _, err := service.BindKnowledgeBase(context.Background(), manager, knowledgeadmin.BindKnowledgeBaseCommand{
		KnowledgeBaseID: base.ID,
		BoundToType:     string(core.BindingSubjectHuman),
		BoundToID:       "human-reader",
		Access:          string(core.AccessRead),
	}); !errors.Is(err, knowledgeadmin.ErrPermissionDenied) {
		t.Fatalf("bind without manage binding error = %v, want permission denied", err)
	}
	if _, err := service.BindKnowledgeBase(context.Background(), agentOwnerManageViewer("agent-owner"), knowledgeadmin.BindKnowledgeBaseCommand{
		KnowledgeBaseID: base.ID,
		BoundToType:     string(core.BindingSubjectAgent),
		BoundToID:       "agent-manager",
		Access:          string(core.AccessManage),
	}); err != nil {
		t.Fatalf("bind manager: %v", err)
	}
	if _, err := service.BindKnowledgeBase(context.Background(), manager, knowledgeadmin.BindKnowledgeBaseCommand{
		KnowledgeBaseID: base.ID,
		BoundToType:     string(core.BindingSubjectHuman),
		BoundToID:       "human-reader",
		Access:          string(core.AccessRead),
	}); err != nil {
		t.Fatalf("bind with manage binding: %v", err)
	}
}

func TestCommandServiceSyncKnowledgeDocumentsRequiresOwnerManageAccess(t *testing.T) {
	ctx := context.Background()
	fixture := newFixture()
	base := core.KnowledgeBase{
		ID: "kb-personal", Name: "Personal", OwnerType: core.OwnerAgent, OwnerID: "agent-owner",
		Visibility: core.VisibilityPrivate, Provider: core.ProviderPageIndex, ProviderResourceID: "folder-personal",
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10}, Status: core.KnowledgeActive,
		CreatedAt: fixture.clock.Now(), UpdatedAt: fixture.clock.Now(),
	}
	if err := fixture.repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatalf("save base: %v", err)
	}
	reconciler := &recordingCatalogReconciler{result: knowledgeadmin.CatalogReconcileResult{Created: 12, Unchanged: 40}}
	service := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).WithCatalogReconciler(reconciler)

	result, err := service.SyncKnowledgeDocuments(ctx, agentOwnerManageViewer("agent-owner"), string(base.ID))
	if err != nil || result.Created != 12 || result.Unchanged != 40 || len(reconciler.bases) != 1 || reconciler.bases[0].ID != base.ID {
		t.Fatalf("owner sync result=%+v err=%v bases=%+v", result, err, reconciler.bases)
	}
	readOnly := knowledgeadmin.ViewerRef{ViewerID: "agent-owner", AgentIDs: []string{"agent-owner"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead}}
	if _, err := service.SyncKnowledgeDocuments(ctx, readOnly, string(base.ID)); !errors.Is(err, knowledgeadmin.ErrPermissionDenied) {
		t.Fatalf("read-only sync error = %v", err)
	}
	unrelatedManager := knowledgeadmin.ViewerRef{ViewerID: "agent-other", AgentIDs: []string{"agent-other"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage}}
	if _, err := service.SyncKnowledgeDocuments(ctx, unrelatedManager, string(base.ID)); !errors.Is(err, knowledgeadmin.ErrPermissionDenied) {
		t.Fatalf("unrelated manager sync error = %v", err)
	}
	if len(reconciler.bases) != 1 {
		t.Fatalf("unauthorized calls reached reconciler: %+v", reconciler.bases)
	}
}

type fixture struct {
	repo   *testkit.FakeRepository
	vector *testkit.FakeVectorStore
	ids    *testkit.FakeIDGenerator
	clock  *testkit.FakeClock
}

type fakeArtifactWriter struct {
	name        string
	contentType string
	content     string
}

type fakeExecutionSubmitter struct {
	submitted []submittedExecution
}

type fakeKnowledgeProvider struct {
	name              core.KnowledgeProvider
	ensureResult      provider.EnsureKnowledgeBaseResult
	uploadResult      provider.UploadDocumentResult
	searchResult      provider.SearchResultPage
	statusResult      provider.DocumentStatusResult
	reprocessResult   provider.ReprocessDocumentResult
	processingResult  provider.DocumentProcessingResult
	reindexResult     provider.ReindexKnowledgeBaseResult
	deleteDocumentErr error
	deletedBase       core.KnowledgeBase
	deletedDocument   core.KnowledgeDocument
	statusDoc         core.KnowledgeDocument
	reprocessDoc      core.KnowledgeDocument
	resultDoc         core.KnowledgeDocument
	uploadBody        string
	searchQuery       string
}

type recordingDocumentMetadataSink struct {
	events []graphprotocol.DomainEvent
}

type recordingCatalogReconciler struct {
	result knowledgeadmin.CatalogReconcileResult
	err    error
	bases  []core.KnowledgeBase
}

func (r *recordingCatalogReconciler) Reconcile(_ context.Context, base core.KnowledgeBase) (knowledgeadmin.CatalogReconcileResult, error) {
	r.bases = append(r.bases, base)
	return r.result, r.err
}

type submittedExecution struct {
	topic       string
	payload     []byte
	maxAttempts int
}

func (f *fakeExecutionSubmitter) Submit(ctx context.Context, topic string, payload []byte, maxAttempts int) error {
	f.submitted = append(f.submitted, submittedExecution{
		topic:       topic,
		payload:     append([]byte(nil), payload...),
		maxAttempts: maxAttempts,
	})
	return nil
}

func (s *recordingDocumentMetadataSink) PublishKnowledgeDocumentMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	event.Payload = cloneEventPayload(event.Payload)
	s.events = append(s.events, event)
	return nil
}

func (s *recordingDocumentMetadataSink) PublishKnowledgePageMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	return s.PublishKnowledgeDocumentMetadata(ctx, event)
}

func (s *recordingDocumentMetadataSink) PublishKnowledgeAnchorMetadata(ctx context.Context, event graphprotocol.DomainEvent) error {
	return s.PublishKnowledgeDocumentMetadata(ctx, event)
}

func cloneEventPayload(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func assertKnowledgeDocumentStatusEvent(t *testing.T, events []graphprotocol.DomainEvent, documentID string, wantStatus string) {
	t.Helper()
	for _, event := range events {
		if event.Type != "knowledge.document.status.updated" {
			continue
		}
		if event.Payload["id"] == documentID && event.Payload["status"] == wantStatus {
			if event.StreamID != documentID {
				t.Fatalf("status event stream id = %q, want %q", event.StreamID, documentID)
			}
			return
		}
	}
	t.Fatalf("knowledge document status event %s/%s missing in %+v", documentID, wantStatus, events)
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

func knowledgeDocumentMetadataEventID(t *testing.T, events []graphprotocol.DomainEvent, documentID string) string {
	t.Helper()
	for _, event := range events {
		if event.Type == "knowledge.document.metadata.updated" && event.Payload["id"] == documentID {
			if event.ID == "" {
				t.Fatalf("metadata event id is empty: %+v", event)
			}
			return event.ID
		}
	}
	t.Fatalf("knowledge document metadata event %s missing in %+v", documentID, events)
	return ""
}

func (f *fakeArtifactWriter) PutArtifact(ctx context.Context, name, contentType string, reader io.Reader) (knowledgeadmin.ArtifactRef, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return knowledgeadmin.ArtifactRef{}, err
	}
	f.name = name
	f.contentType = contentType
	f.content = string(data)
	return knowledgeadmin.ArtifactRef{
		ID:          "artifact-1",
		Name:        name,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		Checksum:    checksum(data),
	}, nil
}

func (f *fakeKnowledgeProvider) Name() core.KnowledgeProvider {
	return f.name
}

func (f *fakeKnowledgeProvider) EnsureKnowledgeBase(ctx context.Context, req provider.EnsureKnowledgeBaseRequest) (provider.EnsureKnowledgeBaseResult, error) {
	return f.ensureResult, ctx.Err()
}

func (f *fakeKnowledgeProvider) DeleteKnowledgeBase(ctx context.Context, req provider.DeleteKnowledgeBaseRequest) error {
	f.deletedBase = req.Base
	return ctx.Err()
}

func (f *fakeKnowledgeProvider) UploadDocument(ctx context.Context, req provider.UploadDocumentRequest) (provider.UploadDocumentResult, error) {
	if err := ctx.Err(); err != nil {
		return provider.UploadDocumentResult{}, err
	}
	data, err := io.ReadAll(req.Reader)
	if err != nil {
		return provider.UploadDocumentResult{}, err
	}
	f.uploadBody = string(data)
	return f.uploadResult, nil
}

func (f *fakeKnowledgeProvider) CreateDocumentMetadata(ctx context.Context, req provider.CreateDocumentMetadataRequest) (provider.UploadDocumentResult, error) {
	return provider.UploadDocumentResult{}, ctx.Err()
}

func (f *fakeKnowledgeProvider) DeleteDocument(ctx context.Context, req provider.DeleteDocumentRequest) error {
	f.deletedDocument = req.Document
	if f.deleteDocumentErr != nil {
		return f.deleteDocumentErr
	}
	return ctx.Err()
}

func (f *fakeKnowledgeProvider) RefreshDocumentStatus(ctx context.Context, req provider.DocumentStatusRequest) (provider.DocumentStatusResult, error) {
	f.statusDoc = req.Document
	return f.statusResult, ctx.Err()
}

func (f *fakeKnowledgeProvider) ReprocessDocument(ctx context.Context, req provider.ReprocessDocumentRequest) (provider.ReprocessDocumentResult, error) {
	f.reprocessDoc = req.Document
	return f.reprocessResult, ctx.Err()
}

func (f *fakeKnowledgeProvider) GetDocumentProcessingResult(ctx context.Context, req provider.DocumentProcessingResultRequest) (provider.DocumentProcessingResult, error) {
	f.resultDoc = req.Document
	return f.processingResult, ctx.Err()
}

func (f *fakeKnowledgeProvider) ReindexKnowledgeBase(ctx context.Context, req provider.ReindexKnowledgeBaseRequest) (provider.ReindexKnowledgeBaseResult, error) {
	if f.reindexResult.Supported {
		return f.reindexResult, ctx.Err()
	}
	return provider.ReindexKnowledgeBaseResult{Supported: false}, ctx.Err()
}

func (f *fakeKnowledgeProvider) Search(ctx context.Context, req provider.SearchRequest) (provider.SearchResultPage, error) {
	if err := ctx.Err(); err != nil {
		return provider.SearchResultPage{}, err
	}
	f.searchQuery = req.Query
	return f.searchResult, nil
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sha256Hex(text string) string {
	return checksum([]byte(text))
}

func newFixture() fixture {
	return fixture{
		repo:   testkit.NewFakeRepository(),
		vector: testkit.NewFakeVectorStore("D:/tmp/knowledge-test"),
		ids:    testkit.NewFakeIDGenerator(),
		clock:  testkit.NewFakeClock(time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)),
	}
}

func manageViewer() knowledgeadmin.ViewerRef {
	return knowledgeadmin.ViewerRef{ViewerID: "admin", OrganizationIDs: []string{"org-1"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage}}
}

func writeViewer() knowledgeadmin.ViewerRef {
	return knowledgeadmin.ViewerRef{ViewerID: "writer", OrganizationIDs: []string{"org-1"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeWrite}}
}

func readViewer() knowledgeadmin.ViewerRef {
	return knowledgeadmin.ViewerRef{ViewerID: "reader", OrganizationIDs: []string{"org-1"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead}}
}

func agentOwnerManageViewer(agentID string) knowledgeadmin.ViewerRef {
	return knowledgeadmin.ViewerRef{ViewerID: agentID, AgentIDs: []string{agentID}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage}}
}
