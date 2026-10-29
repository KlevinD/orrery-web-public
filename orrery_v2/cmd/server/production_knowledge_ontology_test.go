package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/ingest"
	"orrery_v2/internal/knowledge/provider"
	knowledgestore "orrery_v2/internal/knowledge/store"
	knowledgetestkit "orrery_v2/internal/knowledge/testkit"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/document/chunker/slidingwindow"
	doccore "orrery_v2/internal/platform/document/core"
	"orrery_v2/internal/platform/document/extractor"
	"orrery_v2/internal/platform/document/extractor/markdown"
	"orrery_v2/internal/platform/migration"
)

func TestProductionKnowledgeDocumentMetadataWritesOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-knowledge-ontology"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	coreDB, ok := databases.Get(db.RoleCore)
	if !ok || coreDB == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := knowledgestore.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register knowledge migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(coreDB)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}

	now := time.Date(2026, 6, 19, 10, 30, 0, 0, time.UTC)
	repo := knowledgestore.NewPlatformStore(coreDB)
	providerStub := productionOntologyKnowledgeProvider{
		ensureResult: provider.EnsureKnowledgeBaseResult{ProviderResourceID: "folder-123"},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          2048,
			Checksum:           "sha256:abc",
			Status:             core.DocumentIndexed,
		},
	}
	metadataSink, err := newProductionOntologyDocumentMetadataSink(ctx, coreDB)
	if err != nil {
		t.Fatalf("new production ontology document sink: %v", err)
	}
	command := knowledgeadmin.NewCommandService(repo, knowledgetestkit.NewFakeVectorStore(t.TempDir()), knowledgetestkit.NewFakeIDGenerator(), knowledgetestkit.NewFakeClock(now)).
		WithProviderRegistry(provider.NewStaticRegistry(core.ProviderPageIndex, providerStub)).
		WithDocumentMetadataSink(metadataSink)

	base, err := command.CreateKnowledgeBase(ctx, knowledgeadmin.ViewerRef{
		ViewerID:        "operator-1",
		OrganizationIDs: []string{"org-1"},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage},
	}, knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("CreateKnowledgeBase: %v", err)
	}
	doc, err := command.UploadDocument(ctx, knowledgeadmin.ViewerRef{
		ViewerID:        "operator-1",
		OrganizationIDs: []string{"org-1"},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeWrite},
	}, knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("release playbook"),
	})
	if err != nil {
		t.Fatalf("UploadDocument: %v", err)
	}

	objects, _, facts, evidence, err := contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "knowledge_document" || objects[0].ID != doc.ID {
		t.Fatalf("objects = %+v", objects)
	}
	if len(facts) < 9 {
		t.Fatalf("facts = %+v", facts)
	}
	if len(evidence) != 1 {
		t.Fatalf("evidence = %+v", evidence)
	}
	events, err := newProductionOntologyEventLog(coreDB).List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEvent(t, events, "knowledge-document-metadata:"+doc.ID+":rev-1", "knowledge.document.metadata.updated")
}

func TestProductionKnowledgeDocumentStatusUpdatesOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-knowledge-status-ontology"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	coreDB, ok := databases.Get(db.RoleCore)
	if !ok || coreDB == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := knowledgestore.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register knowledge migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(coreDB)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}

	now := time.Date(2026, 6, 19, 11, 30, 0, 0, time.UTC)
	repo := knowledgestore.NewPlatformStore(coreDB)
	providerStub := productionOntologyKnowledgeProvider{
		ensureResult: provider.EnsureKnowledgeBaseResult{ProviderResourceID: "folder-123"},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          2048,
			Checksum:           "sha256:abc",
			Status:             core.DocumentParsing,
		},
		statusResult: provider.DocumentStatusResult{
			ProviderDocumentID: "pi-doc-456",
			Status:             core.DocumentIndexed,
		},
		reprocessResult: provider.ReprocessDocumentResult{
			Status: core.DocumentParsing,
			Job: provider.DocumentProcessingJob{
				JobID:  "pi-job-789",
				Status: "queued",
				Stage:  "queued",
			},
		},
		processingResult: provider.DocumentProcessingResult{
			Filename: "release-handbook.md",
			Status:   core.DocumentIndexed,
			Progress: 100,
			Stage:    "indexed",
			Pages:    []map[string]any{{"page": float64(1), "text": "release playbook"}},
		},
	}
	metadataSink, err := newProductionOntologyDocumentMetadataSink(ctx, coreDB)
	if err != nil {
		t.Fatalf("new production ontology document sink: %v", err)
	}
	command := knowledgeadmin.NewCommandService(repo, knowledgetestkit.NewFakeVectorStore(t.TempDir()), knowledgetestkit.NewFakeIDGenerator(), knowledgetestkit.NewFakeClock(now)).
		WithProviderRegistry(provider.NewStaticRegistry(core.ProviderPageIndex, providerStub)).
		WithDocumentMetadataSink(metadataSink)

	manager := knowledgeadmin.ViewerRef{
		ViewerID:        "operator-1",
		OrganizationIDs: []string{"org-1"},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage},
	}
	reader := knowledgeadmin.ViewerRef{
		ViewerID:        "operator-1",
		OrganizationIDs: []string{"org-1"},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	}
	writer := knowledgeadmin.ViewerRef{
		ViewerID:        "operator-1",
		OrganizationIDs: []string{"org-1"},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeWrite},
	}
	base, err := command.CreateKnowledgeBase(ctx, manager, knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("CreateKnowledgeBase: %v", err)
	}
	doc, err := command.UploadDocument(ctx, writer, knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("release playbook"),
	})
	if err != nil {
		t.Fatalf("UploadDocument: %v", err)
	}
	if _, err := command.RefreshDocumentStatus(ctx, reader, knowledgeadmin.DocumentActionCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
	}); err != nil {
		t.Fatalf("RefreshDocumentStatus: %v", err)
	}
	_, _, facts, evidence, err := contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot after refresh: %v", err)
	}
	assertContextGraphFact(t, facts, "knowledge_document", doc.ID, "status", string(core.DocumentIndexed))
	firstIndexedEvidenceIDs := contextGraphEvidenceIDs(t, evidence, "knowledge-document-status:"+doc.ID+":"+string(core.DocumentIndexed))

	if _, err := command.ReprocessDocument(ctx, manager, knowledgeadmin.DocumentActionCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
	}); err != nil {
		t.Fatalf("ReprocessDocument: %v", err)
	}
	_, _, facts, _, err = contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot after reprocess: %v", err)
	}
	assertContextGraphFact(t, facts, "knowledge_document", doc.ID, "status", string(core.DocumentParsing))

	if err := command.DeleteDocument(ctx, manager, knowledgeadmin.DeleteDocumentCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
	}); err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	_, _, facts, evidence, err = contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot after delete: %v", err)
	}
	assertContextGraphFact(t, facts, "knowledge_document", doc.ID, "status", string(core.DocumentDeleting))
	if len(evidence) < 4 {
		t.Fatalf("evidence = %+v", evidence)
	}

	secondBase, err := command.CreateKnowledgeBase(ctx, manager, knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs Replay",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("CreateKnowledgeBase second: %v", err)
	}
	secondDoc, err := command.UploadDocument(ctx, writer, knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: secondBase.ID,
		Filename:        "release-handbook-replay.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("release playbook replay"),
	})
	if err != nil {
		t.Fatalf("UploadDocument second: %v", err)
	}
	if _, err := command.RefreshDocumentStatus(ctx, reader, knowledgeadmin.DocumentActionCommand{
		KnowledgeBaseID: secondBase.ID,
		DocumentID:      secondDoc.ID,
	}); err != nil {
		t.Fatalf("RefreshDocumentStatus second first indexed: %v", err)
	}
	_, _, _, evidence, err = contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot after second refresh: %v", err)
	}
	firstRepeatedIndexedEvidenceIDs := contextGraphEvidenceIDs(t, evidence, "knowledge-document-status:"+secondDoc.ID+":"+string(core.DocumentIndexed))
	if _, err := command.GetDocumentProcessingResult(ctx, reader, knowledgeadmin.DocumentProcessingResultCommand{
		KnowledgeBaseID: secondBase.ID,
		DocumentID:      secondDoc.ID,
		Pages:           "1",
	}); err != nil {
		t.Fatalf("GetDocumentProcessingResult second repeated indexed: %v", err)
	}
	_, _, facts, evidence, err = contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot after repeated indexed: %v", err)
	}
	assertContextGraphFact(t, facts, "knowledge_document", secondDoc.ID, "status", string(core.DocumentIndexed))
	secondRepeatedIndexedEvidenceIDs := contextGraphEvidenceIDs(t, evidence, "knowledge-document-status:"+secondDoc.ID+":"+string(core.DocumentIndexed))
	if len(firstRepeatedIndexedEvidenceIDs) != 1 || len(secondRepeatedIndexedEvidenceIDs) != 2 {
		t.Fatalf("repeated indexed evidence ids = before %+v after %+v", firstRepeatedIndexedEvidenceIDs, secondRepeatedIndexedEvidenceIDs)
	}
	if len(firstIndexedEvidenceIDs) != 1 {
		t.Fatalf("first indexed evidence id should not be empty")
	}
	events, err := newProductionOntologyEventLog(coreDB).List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEventType(t, events, "knowledge.document.status.updated")
}

func TestProductionKnowledgeProcessingResultWritesPageMetadataToOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-knowledge-page-ontology"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	coreDB, ok := databases.Get(db.RoleCore)
	if !ok || coreDB == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := knowledgestore.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register knowledge migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(coreDB)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}

	now := time.Date(2026, 6, 20, 16, 0, 0, 0, time.UTC)
	repo := knowledgestore.NewPlatformStore(coreDB)
	providerStub := productionOntologyKnowledgeProvider{
		ensureResult: provider.EnsureKnowledgeBaseResult{ProviderResourceID: "folder-123"},
		uploadResult: provider.UploadDocumentResult{
			ProviderDocumentID: "pi-doc-456",
			Filename:           "release-handbook.md",
			ContentType:        "text/markdown",
			SizeBytes:          2048,
			Checksum:           "sha256:abc",
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
				"text":           "release playbook page text",
			}},
		},
	}
	metadataSink, err := newProductionOntologyDocumentMetadataSink(ctx, coreDB)
	if err != nil {
		t.Fatalf("new production ontology document sink: %v", err)
	}
	command := knowledgeadmin.NewCommandService(repo, knowledgetestkit.NewFakeVectorStore(t.TempDir()), knowledgetestkit.NewFakeIDGenerator(), knowledgetestkit.NewFakeClock(now)).
		WithProviderRegistry(provider.NewStaticRegistry(core.ProviderPageIndex, providerStub)).
		WithDocumentMetadataSink(metadataSink)
	base, err := command.CreateKnowledgeBase(ctx, knowledgeadmin.ViewerRef{
		ViewerID:        "operator-1",
		OrganizationIDs: []string{"org-1"},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage},
	}, knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("CreateKnowledgeBase: %v", err)
	}
	doc, err := command.UploadDocument(ctx, knowledgeadmin.ViewerRef{
		ViewerID:        "operator-1",
		OrganizationIDs: []string{"org-1"},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeWrite},
	}, knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("release playbook"),
	})
	if err != nil {
		t.Fatalf("UploadDocument: %v", err)
	}
	if _, err := command.GetDocumentProcessingResult(ctx, knowledgeadmin.ViewerRef{
		ViewerID:        "operator-1",
		OrganizationIDs: []string{"org-1"},
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead},
	}, knowledgeadmin.DocumentProcessingResultCommand{
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
		Pages:           "1",
	}); err != nil {
		t.Fatalf("GetDocumentProcessingResult: %v", err)
	}

	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	pageID := doc.ID + ":page:1"
	assertContextGraphObject(t, objects, "knowledge_page", pageID)
	assertContextGraphLink(t, links, "knowledge_page_belongs_to_document", pageID, doc.ID)
	assertContextGraphFact(t, facts, "knowledge_page", pageID, "page_number", 1)
	assertContextGraphFact(t, facts, "knowledge_page", pageID, "provider_document_id", "pi-doc-456")
	contextGraphEvidenceIDs(t, evidence, "knowledge-page-metadata:"+pageID+":rev-")
	anchorID := pageID + ":anchor:0007"
	assertContextGraphObject(t, objects, "knowledge_anchor", anchorID)
	assertContextGraphLink(t, links, "knowledge_anchor_belongs_to_page", anchorID, pageID)
	assertContextGraphFact(t, facts, "knowledge_anchor", anchorID, "provider_node_id", "0007")
	assertContextGraphFact(t, facts, "knowledge_anchor", anchorID, "snippet_hash", "sha256:18a62be0d2e560cc1a35166c719bdc8d1729444e4af9ed8c4ade4d63fe0d04ce")
	contextGraphEvidenceIDs(t, evidence, "knowledge-anchor-metadata:"+anchorID+":rev-")
	events, err := newProductionOntologyEventLog(coreDB).List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEventType(t, events, "knowledge.page.metadata.updated")
	assertOntologyEventType(t, events, "knowledge.anchor.metadata.updated")
}

func TestProductionKnowledgeIngestStatusUpdatesOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-knowledge-ingest-status-ontology"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	coreDB, ok := databases.Get(db.RoleCore)
	if !ok || coreDB == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := knowledgestore.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register knowledge migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(coreDB)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}

	now := time.Date(2026, 6, 19, 12, 30, 0, 0, time.UTC)
	repo := knowledgestore.NewPlatformStore(coreDB)
	base := core.KnowledgeBase{
		ID:                  "kb-1",
		Name:                "Product Docs",
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
	if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatalf("SaveKnowledgeBase: %v", err)
	}
	doc := core.KnowledgeDocument{
		ID:              "kdoc-1",
		KnowledgeBaseID: base.ID,
		ArtifactID:      "artifact-1",
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Status:          core.DocumentUploaded,
	}
	if err := repo.SaveDocument(ctx, doc); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}
	if err := repo.SaveIngestJob(ctx, core.KnowledgeIngestJob{
		ID:              "kjob-1",
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
		Topic:           ingest.TopicIngestDocument,
		Status:          core.JobQueued,
	}); err != nil {
		t.Fatalf("SaveIngestJob: %v", err)
	}
	metadataSink, err := newProductionOntologyDocumentMetadataSink(ctx, coreDB)
	if err != nil {
		t.Fatalf("new production ontology document sink: %v", err)
	}
	vectorStore := knowledgetestkit.NewFakeVectorStore(t.TempDir())
	ingestService := ingest.NewService(repo, knowledgetestkit.NewFakeClock(now), ingest.Options{
		Artifacts: productionOntologyArtifactReader{content: "# Release\nShip through staged rollout."},
		Extractors: extractor.NewRegistry(
			markdown.Extractor{},
		),
		Chunker: slidingwindow.New(),
		Embedding: knowledgetestkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{
			Model:      "test-embedding",
			Dimensions: 3,
			Vectors:    [][]float32{{1, 0, 0}},
		}},
		VectorStore: vectorStore,
		DescriptorForBase: func(base core.KnowledgeBase) (ingest.VectorDescriptor, error) {
			return ingest.VectorDescriptor{
				KnowledgeBaseID: string(base.ID),
				StorePath:       base.VectorStorePath,
				Dimensions:      base.EmbeddingDimensions,
			}, nil
		},
		DocumentStatusSink: metadataSink,
	})
	if err := ingestService.HandleDocumentIngest(ctx, ingest.DocumentIngestPayload{
		JobID:           "kjob-1",
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
	}); err != nil {
		t.Fatalf("HandleDocumentIngest: %v", err)
	}

	_, _, facts, evidence, err := contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot after ingest: %v", err)
	}
	assertContextGraphFact(t, facts, "knowledge_document", string(doc.ID), "status", string(core.DocumentIndexed))
	contextGraphEvidenceIDs(t, evidence, "knowledge-document-metadata:kdoc-1:rev-")
	contextGraphEvidenceIDs(t, evidence, "knowledge-document-status:kdoc-1:indexed:rev-")
	contextGraphEvidenceIDs(t, evidence, "knowledge-chunk-metadata:kdoc-1:0:rev-")
}

func TestProductionKnowledgeIngestWritesChunkMetadataToOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-knowledge-chunk-ontology"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	coreDB, ok := databases.Get(db.RoleCore)
	if !ok || coreDB == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := knowledgestore.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register knowledge migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(coreDB)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}

	now := time.Date(2026, 6, 20, 13, 0, 0, 0, time.UTC)
	repo := knowledgestore.NewPlatformStore(coreDB)
	base := core.KnowledgeBase{
		ID:                  "kb-1",
		Name:                "Product Docs",
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
	if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatalf("SaveKnowledgeBase: %v", err)
	}
	doc := core.KnowledgeDocument{
		ID:              "kdoc-1",
		KnowledgeBaseID: base.ID,
		ArtifactID:      "artifact-1",
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Status:          core.DocumentUploaded,
	}
	if err := repo.SaveDocument(ctx, doc); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}
	if err := repo.SaveIngestJob(ctx, core.KnowledgeIngestJob{
		ID:              "kjob-1",
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
		Topic:           ingest.TopicIngestDocument,
		Status:          core.JobQueued,
	}); err != nil {
		t.Fatalf("SaveIngestJob: %v", err)
	}
	metadataSink, err := newProductionOntologyDocumentMetadataSink(ctx, coreDB)
	if err != nil {
		t.Fatalf("new production ontology document sink: %v", err)
	}
	vectorStore := knowledgetestkit.NewFakeVectorStore(t.TempDir())
	ingestService := ingest.NewService(repo, knowledgetestkit.NewFakeClock(now), ingest.Options{
		Artifacts: productionOntologyArtifactReader{content: "# Release\nShip through staged rollout."},
		Extractors: extractor.NewRegistry(
			markdown.Extractor{},
		),
		Chunker: slidingwindow.New(),
		Embedding: knowledgetestkit.FakeEmbeddingProvider{Response: ingest.EmbedResponse{
			Model:      "test-embedding",
			Dimensions: 3,
			Vectors:    [][]float32{{1, 0, 0}},
		}},
		VectorStore: vectorStore,
		DescriptorForBase: func(base core.KnowledgeBase) (ingest.VectorDescriptor, error) {
			return ingest.VectorDescriptor{
				KnowledgeBaseID: string(base.ID),
				StorePath:       base.VectorStorePath,
				Dimensions:      base.EmbeddingDimensions,
			}, nil
		},
		DocumentStatusSink: metadataSink,
	})
	if err := ingestService.HandleDocumentIngest(ctx, ingest.DocumentIngestPayload{
		JobID:           "kjob-1",
		KnowledgeBaseID: base.ID,
		DocumentID:      doc.ID,
	}); err != nil {
		t.Fatalf("HandleDocumentIngest: %v", err)
	}

	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(coreDB).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot after ingest: %v", err)
	}
	assertContextGraphObject(t, objects, "knowledge_chunk", string(doc.ID)+":0")
	assertContextGraphLink(t, links, "knowledge_chunk_belongs_to_document", string(doc.ID)+":0", string(doc.ID))
	assertContextGraphFact(t, facts, "knowledge_chunk", string(doc.ID)+":0", "knowledge_base_id", string(base.ID))
	assertContextGraphFact(t, facts, "knowledge_chunk", string(doc.ID)+":0", "ordinal", 0)
	contextGraphEvidenceIDs(t, evidence, "knowledge-chunk-metadata:kdoc-1:0:rev-")
	events, err := newProductionOntologyEventLog(coreDB).List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEventType(t, events, "knowledge.chunk.metadata.updated")
}
