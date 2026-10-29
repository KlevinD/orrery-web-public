package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/app/operations"
	knowledgecore "orrery_v2/internal/knowledge/core"
	knowledgetestkit "orrery_v2/internal/knowledge/testkit"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestProductionKnowledgeOntologyEventTenantResolverCanonicalizesPositionOwner(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "knowledge-ontology-position-event-tenant")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	repo := knowledgetestkit.NewFakeRepository()
	base, document := savePositionKnowledgeOntologyCatalog(t, ctx, repo)
	catalog := positionKnowledgeOntologyCatalog()
	sink, err := newProductionOntologyDocumentMetadataSinkWithRunner(core, runner)
	if err != nil {
		t.Fatal(err)
	}
	sink = sink.WithTenantResolver(newProductionKnowledgeOntologyEventTenantResolver(repo, catalog))
	event := positionKnowledgeDocumentMetadataEvent(base, document)
	if err := sink.PublishKnowledgeDocumentMetadata(ctx, event); err != nil {
		t.Fatalf("PublishKnowledgeDocumentMetadata: %v", err)
	}

	objects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "minvia")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "knowledge_document", string(document.ID))
	positionObjects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, base.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	assertNoContextGraphObject(t, positionObjects, "knowledge_document", string(document.ID))
	recorded, err := sink.events.Get(ctx, event.ID)
	if err != nil {
		t.Fatalf("read canonical ontology event: %v", err)
	}
	if recorded.TenantID != "minvia" || recorded.SourceAccessRef.Kind != "knowledge_document" ||
		recorded.SourceAccessRef.ID != string(document.ID) || recorded.SourceAccessRef.TenantID != "minvia" {
		t.Fatalf("canonical ontology event = %+v", recorded)
	}
}

func TestProductionKnowledgeOntologyEventTenantResolverRejectsUnresolvedPositionOwner(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "knowledge-ontology-unresolved-position-event-tenant")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	repo := knowledgetestkit.NewFakeRepository()
	base, document := savePositionKnowledgeOntologyCatalog(t, ctx, repo)
	sink, err := newProductionOntologyDocumentMetadataSinkWithRunner(core, runner)
	if err != nil {
		t.Fatal(err)
	}
	sink = sink.WithTenantResolver(newProductionKnowledgeOntologyEventTenantResolver(
		repo, &fakeKnowledgeOntologyOrganizationCatalog{},
	))
	if err := sink.PublishKnowledgeDocumentMetadata(ctx, positionKnowledgeDocumentMetadataEvent(base, document)); err == nil {
		t.Fatal("PublishKnowledgeDocumentMetadata error = nil")
	}
	objects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, base.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	assertNoContextGraphObject(t, objects, "knowledge_document", string(document.ID))
}

func TestProductionKnowledgeOntologyEventTenantResolverReevaluatesPositionOrganization(t *testing.T) {
	ctx := context.Background()
	repo := knowledgetestkit.NewFakeRepository()
	base, document := savePositionKnowledgeOntologyCatalog(t, ctx, repo)
	catalog := positionKnowledgeOntologyCatalog()
	resolver := newProductionKnowledgeOntologyEventTenantResolver(repo, catalog)
	event := positionKnowledgeDocumentMetadataEvent(base, document)

	tenantID, supported, err := resolver.ResolveEventTenant(ctx, event)
	if err != nil {
		t.Fatalf("ResolveEventTenant: %v", err)
	}
	if tenantID != "minvia" || !supported {
		t.Fatalf("initial tenant = %q, supported = %v", tenantID, supported)
	}
	catalog.graph.Organizations = []operations.OrganizationDTO{{OrganizationID: "minvia-next"}}
	catalog.graph.Departments = []operations.DepartmentDTO{{DepartmentID: "department-growth", OrganizationID: "minvia-next"}}

	tenantID, supported, err = resolver.ResolveEventTenant(ctx, event)
	if err != nil {
		t.Fatalf("ResolveEventTenant after organization change: %v", err)
	}
	if tenantID != "minvia-next" || !supported {
		t.Fatalf("updated tenant = %q, supported = %v", tenantID, supported)
	}
	if catalog.calls != 2 {
		t.Fatalf("organization catalog calls = %d, want 2", catalog.calls)
	}
}

func TestProductionKnowledgeOntologyCatalogProjectorRepairsMissingPositionDocument(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "knowledge-ontology-position-catalog-projector")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	repo := knowledgetestkit.NewFakeRepository()
	base, document := savePositionKnowledgeOntologyCatalog(t, ctx, repo)
	sink, err := newProductionOntologyDocumentMetadataSinkWithRunner(core, runner)
	if err != nil {
		t.Fatal(err)
	}
	projector := productionKnowledgeOntologyCatalogProjector{
		repo: repo, graph: contextgraph.NewPlatformStore(core), sink: sink,
	}
	binding := knowledgeontology.Binding{
		TenantID: "minvia", KnowledgeBaseID: string(base.ID), FolderID: base.ProviderResourceID, Enabled: true,
	}
	if err := projector.EnsureKnowledgeBaseCatalog(ctx, binding); err != nil {
		t.Fatalf("EnsureKnowledgeBaseCatalog: %v", err)
	}
	if err := projector.EnsureKnowledgeBaseCatalog(ctx, binding); err != nil {
		t.Fatalf("idempotent EnsureKnowledgeBaseCatalog: %v", err)
	}

	objects, _, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "minvia")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "knowledge_base", string(base.ID))
	assertContextGraphObject(t, objects, "knowledge_document", string(document.ID))
	projection := knowledgeontology.ProjectPublishedDocument(knowledgeontology.ProjectRequest{
		TenantID: "minvia", KnowledgeBaseID: string(base.ID), DocumentID: document.ProviderDocumentID,
		Revision: 1, ContentHash: "sha256:pageindex", Objects: objects, Facts: facts,
	})
	if projection.KnowledgeDocumentObjectID != string(document.ID) {
		t.Fatalf("knowledge document object id = %q, want %q", projection.KnowledgeDocumentObjectID, document.ID)
	}
}

func TestOntologyInitializationProjectsPositionKnowledgeIntoOrganizationTenant(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "knowledge-ontology-position-initialization")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	base := knowledgeOntologyTestBase("kb-position", knowledgecore.OwnerPosition, "position-marketing")
	base.UpdatedAt = now
	document := knowledgecore.KnowledgeDocument{
		ID: "kdoc-position", KnowledgeBaseID: base.ID, ProviderDocumentID: "pi-position",
		Filename: "marketing.md", ContentType: "text/markdown", SizeBytes: 4096, Checksum: "sha256:marketing", Status: knowledgecore.DocumentIndexed,
		CreatedAt: now, UpdatedAt: now, IndexedAt: now,
	}
	source := fakeOntologyInitializationCatalogSource{
		bases: []knowledgecore.KnowledgeBase{base},
		documents: map[knowledgecore.KnowledgeBaseID][]knowledgecore.KnowledgeDocument{
			base.ID: {document},
		},
	}
	report, err := newOntologyInitializationKnowledgeAdapter(source, runner, positionKnowledgeOntologyCatalog()).Reconcile(
		ctx, ontologyInitializationRequest{Mode: ontologyInitializationApply, Operator: "operator", PageSize: 100},
	)
	if err != nil {
		t.Fatalf("reconcile position knowledge ontology: %v", err)
	}
	if report.Source != 2 || report.Projected != 2 || report.Failed != 0 {
		t.Fatalf("initialization report = %+v", report)
	}
	objects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "minvia")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "knowledge_base", string(base.ID))
	assertContextGraphObject(t, objects, "knowledge_document", string(document.ID))
	positionObjects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, base.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	assertNoContextGraphObject(t, positionObjects, "knowledge_document", string(document.ID))
}

func savePositionKnowledgeOntologyCatalog(t *testing.T, ctx context.Context, repo *knowledgetestkit.FakeRepository) (knowledgecore.KnowledgeBase, knowledgecore.KnowledgeDocument) {
	t.Helper()
	now := time.Date(2026, 8, 28, 8, 30, 0, 0, time.UTC)
	base := knowledgeOntologyTestBase("kb-position", knowledgecore.OwnerPosition, "position-marketing")
	base.UpdatedAt = now
	if err := repo.SaveKnowledgeBase(ctx, base); err != nil {
		t.Fatalf("SaveKnowledgeBase: %v", err)
	}
	document := knowledgecore.KnowledgeDocument{
		ID: "kdoc-position", KnowledgeBaseID: base.ID, ProviderDocumentID: "pi-position",
		Filename: "marketing.md", ContentType: "text/markdown", SizeBytes: 4096, Checksum: "sha256:marketing", Status: knowledgecore.DocumentIndexed,
		CreatedAt: now, UpdatedAt: now, IndexedAt: now,
	}
	if err := repo.SaveDocument(ctx, document); err != nil {
		t.Fatalf("SaveDocument: %v", err)
	}
	return base, document
}

func positionKnowledgeOntologyCatalog() *fakeKnowledgeOntologyOrganizationCatalog {
	return &fakeKnowledgeOntologyOrganizationCatalog{graph: operations.OrgGraphDTO{
		Organizations: []operations.OrganizationDTO{{OrganizationID: "minvia"}},
		Departments:   []operations.DepartmentDTO{{DepartmentID: "department-growth", OrganizationID: "minvia"}},
		Positions:     []operations.PositionDTO{{PositionID: "position-marketing", DepartmentID: "department-growth", KnowledgeBaseID: "kb-position"}},
	}}
}

func positionKnowledgeDocumentMetadataEvent(base knowledgecore.KnowledgeBase, document knowledgecore.KnowledgeDocument) graphprotocol.DomainEvent {
	return graphprotocol.DomainEvent{
		ID: "knowledge-document-metadata:" + string(document.ID) + ":rev-1", TenantID: base.OwnerID,
		Type: "knowledge.document.metadata.updated", StreamID: string(document.ID), Version: 1, OccurredAt: document.UpdatedAt,
		Payload: map[string]any{
			"id": string(document.ID), "knowledge_base_id": string(base.ID), "title": document.Filename,
			"source_uri": "pageindex://" + base.ProviderResourceID + "/" + document.ProviderDocumentID,
			"provider":   string(base.Provider), "provider_document_id": document.ProviderDocumentID,
			"content_type": document.ContentType, "size_bytes": document.SizeBytes, "checksum": document.Checksum,
			"status": string(document.Status),
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "knowledge"},
	}
}
