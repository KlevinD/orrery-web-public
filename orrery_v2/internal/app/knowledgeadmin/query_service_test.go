package knowledgeadmin_test

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/knowledge/core"
	doccore "orrery_v2/internal/platform/document/core"
)

func TestQueryServiceListsBasesAndDocuments(t *testing.T) {
	fixture := newFixture()
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock)
	query := knowledgeadmin.NewQueryService(fixture.repo)
	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Queryable",
		OwnerType:   string(core.OwnerOrganization),
		OwnerID:     "org-1",
		Visibility:  string(core.VisibilityScoped),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	if _, err := command.CreateDocumentMetadata(context.Background(), writeViewer(), knowledgeadmin.CreateDocumentCommand{
		KnowledgeBaseID: base.ID,
		ArtifactID:      "artifact-1",
		Filename:        "notes.md",
		ContentType:     "text/markdown",
		SizeBytes:       42,
		Checksum:        "sum",
	}); err != nil {
		t.Fatalf("create doc: %v", err)
	}

	orgReader := knowledgeadmin.ViewerRef{ViewerID: "reader", OrganizationIDs: []string{"org-1"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead}}
	bases, err := query.ListKnowledgeBases(context.Background(), orgReader, knowledgeadmin.ListKnowledgeBasesQuery{OwnerType: string(core.OwnerOrganization), OwnerID: "org-1"})
	if err != nil {
		t.Fatalf("list bases: %v", err)
	}
	if bases.Total != 1 || bases.Items[0].ID != base.ID {
		t.Fatalf("bases mismatch: %#v", bases)
	}
	docs, err := query.ListDocuments(context.Background(), orgReader, base.ID)
	if err != nil {
		t.Fatalf("list docs: %v", err)
	}
	if docs.Total != 1 || docs.Items[0].Filename != "notes.md" {
		t.Fatalf("docs mismatch: %#v", docs)
	}
}

func TestQueryServiceGetsKnowledgeJobStatus(t *testing.T) {
	fixture := newFixture()
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock)
	query := knowledgeadmin.NewQueryService(fixture.repo)
	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Jobs",
		OwnerType:   string(core.OwnerOrganization),
		OwnerID:     "org-1",
		Visibility:  string(core.VisibilityScoped),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	job, err := command.CreateReindexJob(context.Background(), manageViewer(), knowledgeadmin.ReindexKnowledgeBaseCommand{KnowledgeBaseID: base.ID})
	if err != nil {
		t.Fatalf("create reindex job: %v", err)
	}

	got, err := query.GetKnowledgeJob(context.Background(), knowledgeadmin.ViewerRef{ViewerID: "reader", OrganizationIDs: []string{"org-1"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead}}, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if got.ID != job.ID || got.Topic != "knowledge.reindex_base" || got.Status != string(core.JobQueued) {
		t.Fatalf("job mismatch: %#v", got)
	}
}

func TestQueryServiceFiltersInvisibleKnowledgeBases(t *testing.T) {
	fixture := newFixture()
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock)
	query := knowledgeadmin.NewQueryService(fixture.repo)
	publicBase, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Public",
		OwnerType:   string(core.OwnerOrganization),
		OwnerID:     "org-1",
		Visibility:  string(core.VisibilityPublic),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create public base: %v", err)
	}
	privateBase, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Private",
		OwnerType:   string(core.OwnerHuman),
		OwnerID:     "human-owner",
		Visibility:  string(core.VisibilityPrivate),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create private base: %v", err)
	}
	viewer := knowledgeadmin.ViewerRef{ViewerID: "reader", OrganizationIDs: []string{"org-1"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead}}

	page, err := query.ListKnowledgeBases(context.Background(), viewer, knowledgeadmin.ListKnowledgeBasesQuery{})
	if err != nil {
		t.Fatalf("list bases: %v", err)
	}
	if page.Total != 1 || page.Items[0].ID != publicBase.ID {
		t.Fatalf("visible bases = %#v", page)
	}
	if _, err := query.GetKnowledgeBase(context.Background(), viewer, privateBase.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("invisible get error = %v, want not found", err)
	}
}

func TestQueryServiceListsBindingsDocumentsAndJobs(t *testing.T) {
	fixture := newFixture()
	command := knowledgeadmin.NewCommandService(fixture.repo, fixture.vector, fixture.ids, fixture.clock)
	query := knowledgeadmin.NewQueryService(fixture.repo)
	base, err := command.CreateKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        "Lifecycle",
		OwnerType:   string(core.OwnerOrganization),
		OwnerID:     "org-1",
		Visibility:  string(core.VisibilityScoped),
		VectorRoot:  fixture.vector.Root,
		ChunkPolicy: doccore.ChunkPolicy{MaxRunes: 100, OverlapRunes: 10},
	})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	binding, err := command.BindKnowledgeBase(context.Background(), manageViewer(), knowledgeadmin.BindKnowledgeBaseCommand{
		KnowledgeBaseID: base.ID,
		BoundToType:     string(core.BindingSubjectAgent),
		BoundToID:       "agent-pm",
		Access:          string(core.AccessRead),
	})
	if err != nil {
		t.Fatalf("bind base: %v", err)
	}
	doc, err := command.CreateDocumentMetadata(context.Background(), writeViewer(), knowledgeadmin.CreateDocumentCommand{
		KnowledgeBaseID: base.ID,
		ArtifactID:      "artifact-1",
		Filename:        "runbook.md",
		ContentType:     "text/markdown",
		SizeBytes:       32,
		Checksum:        "sum",
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	reindex, err := command.CreateReindexJob(context.Background(), manageViewer(), knowledgeadmin.ReindexKnowledgeBaseCommand{KnowledgeBaseID: base.ID})
	if err != nil {
		t.Fatalf("create reindex job: %v", err)
	}

	viewer := knowledgeadmin.ViewerRef{ViewerID: "reader", OrganizationIDs: []string{"org-1"}, PermissionScope: []string{knowledgeadmin.PermissionKnowledgeRead}}
	bindings, err := query.ListBindings(context.Background(), viewer, base.ID)
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	if bindings.Total != 1 || bindings.Items[0].ID != binding.ID || bindings.Items[0].BoundToID != "agent-pm" {
		t.Fatalf("bindings mismatch: %#v", bindings)
	}
	gotDoc, err := query.GetDocument(context.Background(), viewer, base.ID, doc.ID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if gotDoc.ID != doc.ID || gotDoc.Filename != "runbook.md" {
		t.Fatalf("document mismatch: %#v", gotDoc)
	}
	if _, err := query.GetDocument(context.Background(), viewer, "other-kb", doc.ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("mismatched document base error = %v, want not found", err)
	}
	jobs, err := query.ListJobs(context.Background(), viewer, base.ID)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if jobs.Total != 2 {
		t.Fatalf("jobs total = %d, want upload and reindex jobs: %#v", jobs.Total, jobs)
	}
	if !hasKnowledgeJob(jobs.Items, doc.JobID, "knowledge.ingest_document") || !hasKnowledgeJob(jobs.Items, reindex.ID, "knowledge.reindex_base") {
		t.Fatalf("jobs missing expected entries: %#v", jobs.Items)
	}
}

func hasKnowledgeJob(items []knowledgeadmin.KnowledgeJobDTO, id string, topic string) bool {
	for _, item := range items {
		if item.ID == id && item.Topic == topic {
			return true
		}
	}
	return false
}
