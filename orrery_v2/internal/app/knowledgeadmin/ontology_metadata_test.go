package knowledgeadmin_test

import (
	"context"
	"strings"
	"testing"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
	"orrery_v2/internal/platform/graphprotocol"
)

func TestCommandServicePublishesKnowledgeDocumentMetadataToOntologyRunner(t *testing.T) {
	ctx := context.Background()
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
	ontologyRegistry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := ontology.RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := ontology.NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock).
		WithProviderRegistry(provider.NewStaticRegistry(core.ProviderPageIndex, pageIndex)).
		WithDocumentMetadataSink(knowledgeadmin.OntologyDocumentMetadataSink{Runner: runner})

	base, err := command.CreateKnowledgeBase(ctx, manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:       "Product Docs",
		OwnerType:  string(core.OwnerOrganization),
		OwnerID:    "org-1",
		Visibility: string(core.VisibilityScoped),
		Provider:   string(core.ProviderPageIndex),
	})
	if err != nil {
		t.Fatalf("create pageindex base: %v", err)
	}
	doc, err := command.UploadDocument(ctx, writeViewer(), knowledgeadmin.UploadDocumentCommand{
		KnowledgeBaseID: base.ID,
		Filename:        "release-handbook.md",
		ContentType:     "text/markdown",
		Reader:          strings.NewReader("release playbook"),
	})
	if err != nil {
		t.Fatalf("upload pageindex document: %v", err)
	}

	objects, _, facts, evidence, err := graphStore.Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "knowledge_document" || objects[0].ID != doc.ID {
		t.Fatalf("objects = %+v", objects)
	}
	if len(facts) < 7 {
		t.Fatalf("facts = %+v", facts)
	}
	if len(evidence) != 1 {
		t.Fatalf("evidence = %+v", evidence)
	}
}
