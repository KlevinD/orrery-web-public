package knowledgeontology

import (
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
)

func TestProjectPublishedDocumentResolvesOnlyExistingObjects(t *testing.T) {
	objects := []contextgraph.Object{
		{ID: "project-1", TenantID: "tenant-1", ObjectType: "project", CanonicalKey: "project:orrery", Properties: map[string]any{"external_id": "orrery"}},
		{ID: "tool-1", TenantID: "tenant-1", ObjectType: "tool", CanonicalKey: "tool:pageindex", Properties: map[string]any{"aliases": []any{"pageindex", "PageIndex"}}},
	}
	extracted := Extraction{
		Subjects: []ObjectRef{{Kind: "project", ID: "orrery"}},
		Assertions: []Assertion{{
			LinkType: "project_uses_tool",
			From:     ObjectRef{Kind: "project", ID: "orrery"},
			To:       ObjectRef{Kind: "tool", ID: "pageindex"},
			Anchor:   "frontmatter:ontology.assertions[0]",
		}},
	}
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)

	projection := ProjectPublishedDocument(ProjectRequest{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1",
		Revision: 3, ContentHash: "sha256:abc", PublishedAt: now,
		Extraction: extracted, Objects: objects,
	})

	if len(projection.Subjects) != 1 || projection.Subjects[0].ObjectID != "project-1" || projection.Subjects[0].Resolution != ResolutionResolved {
		t.Fatalf("subjects = %+v", projection.Subjects)
	}
	if len(projection.Assertions) != 1 {
		t.Fatalf("assertions = %+v", projection.Assertions)
	}
	assertion := projection.Assertions[0]
	if assertion.FromObjectID != "project-1" || assertion.ToObjectID != "tool-1" || assertion.Status != AssertionConfirmed {
		t.Fatalf("assertion = %+v", assertion)
	}
	if assertion.Evidence.DocumentID != "doc-1" || assertion.Evidence.Revision != 3 || assertion.Evidence.ContentHash != "sha256:abc" || assertion.Evidence.Anchor == "" {
		t.Fatalf("evidence = %+v", assertion.Evidence)
	}
}

func TestProjectPublishedDocumentLeavesAmbiguousAliasUnresolved(t *testing.T) {
	objects := []contextgraph.Object{
		{ID: "tool-1", TenantID: "tenant-1", ObjectType: "tool", CanonicalKey: "tool:one", Properties: map[string]any{"aliases": []any{"search"}}},
		{ID: "tool-2", TenantID: "tenant-1", ObjectType: "tool", CanonicalKey: "tool:two", Properties: map[string]any{"aliases": []any{"search"}}},
	}
	projection := ProjectPublishedDocument(ProjectRequest{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Revision: 1,
		Extraction: Extraction{Subjects: []ObjectRef{{Kind: "tool", ID: "search"}}},
		Objects:    objects,
	})

	if len(projection.Subjects) != 1 || projection.Subjects[0].Resolution != ResolutionAmbiguous || projection.Subjects[0].ObjectID != "" {
		t.Fatalf("subjects = %+v", projection.Subjects)
	}
}

func TestProjectPublishedDocumentDoesNotCreateUnknownObjectsOrAssertions(t *testing.T) {
	projection := ProjectPublishedDocument(ProjectRequest{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Revision: 1,
		Extraction: Extraction{Assertions: []Assertion{{
			LinkType: "project_uses_tool",
			From:     ObjectRef{Kind: "project", ID: "missing"},
			To:       ObjectRef{Kind: "tool", ID: "missing"},
		}}},
	})

	if len(projection.Assertions) != 1 || projection.Assertions[0].Status != AssertionUnresolved {
		t.Fatalf("assertions = %+v", projection.Assertions)
	}
	if projection.Assertions[0].FromObjectID != "" || projection.Assertions[0].ToObjectID != "" {
		t.Fatalf("unexpected object creation in projection: %+v", projection.Assertions[0])
	}
}

func TestProjectPublishedDocumentExtractsConfirmedRetrievalMentionsFromMarkdown(t *testing.T) {
	projection := ProjectPublishedDocument(ProjectRequest{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Revision: 1,
		Source: "# Architecture\nOrrery uses PageIndex for knowledge navigation.\n",
		Objects: []contextgraph.Object{
			{ID: "knowledge-doc-1", TenantID: "tenant-1", ObjectType: "knowledge_document", Label: "Architecture"},
			{ID: "project-1", TenantID: "tenant-1", ObjectType: "project", Label: "Orrery"},
			{ID: "tool-1", TenantID: "tenant-1", ObjectType: "tool", Label: "PageIndex"},
			{ID: "other-tenant", TenantID: "tenant-2", ObjectType: "project", Label: "Orrery"},
		},
		Facts: []contextgraph.Fact{
			{TenantID: "tenant-1", Subject: contextgraph.Ref{Kind: "knowledge_document", ID: "knowledge-doc-1"}, Predicate: "provider_document_id", Value: "doc-1"},
			{TenantID: "tenant-1", Subject: contextgraph.Ref{Kind: "knowledge_document", ID: "knowledge-doc-1"}, Predicate: "knowledge_base_id", Value: "kb-1"},
		},
	})

	if len(projection.Subjects) != 2 {
		t.Fatalf("subjects = %+v", projection.Subjects)
	}
	for _, subject := range projection.Subjects {
		if subject.Status != AssertionConfirmed || subject.Resolution != ResolutionResolved || subject.Confidence <= 0 {
			t.Fatalf("confirmed retrieval subject = %+v", subject)
		}
		if subject.Evidence.ExtractionMethod != "deterministic_exact_entity_match" || subject.Evidence.Anchor != "markdown:line:2" {
			t.Fatalf("mention evidence = %+v", subject.Evidence)
		}
	}
	if projection.KnowledgeDocumentObjectID != "knowledge-doc-1" {
		t.Fatalf("knowledge document object id = %q", projection.KnowledgeDocumentObjectID)
	}
}
