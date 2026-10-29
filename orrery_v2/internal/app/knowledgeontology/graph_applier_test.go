package knowledgeontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
)

type recordingGraphWriter struct {
	evidence        []contextgraph.Evidence
	links           []contextgraph.Link
	deleted         []string
	deletedEvidence []string
}

func (w *recordingGraphWriter) SaveEvidence(_ context.Context, evidence contextgraph.Evidence) error {
	w.evidence = append(w.evidence, evidence)
	return nil
}
func (w *recordingGraphWriter) SaveLink(_ context.Context, link contextgraph.Link) error {
	w.links = append(w.links, link)
	return nil
}
func (w *recordingGraphWriter) DeleteLink(_ context.Context, _, linkID, _ string) error {
	w.deleted = append(w.deleted, linkID)
	return nil
}
func (w *recordingGraphWriter) DeleteEvidence(_ context.Context, _, evidenceID string, _ contextgraph.Ref) error {
	w.deletedEvidence = append(w.deletedEvidence, evidenceID)
	return nil
}

type allowLinkTypes struct{}

func (allowLinkTypes) ValidateKnowledgeLink(context.Context, string, string, string, string) error {
	return nil
}

func TestGraphApplierWritesConfirmedAssertionWithExactEvidence(t *testing.T) {
	writer := &recordingGraphWriter{}
	applier := NewGraphApplier(writer, allowLinkTypes{})
	binding := Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1", SourceAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"}}
	projection := Projection{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Revision: 3, ContentHash: "sha256:abc",
		Assertions: []AssertionProjection{{
			LinkType: "project_uses_tool", FromObjectID: "project-1", ToObjectID: "tool-1", Status: AssertionConfirmed,
			Evidence: Evidence{KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Revision: 3, ContentHash: "sha256:abc", Anchor: "frontmatter:ontology.assertions[0]", ExtractionMethod: "explicit_frontmatter"},
		}},
	}

	if err := applier.ApplyProjection(context.Background(), binding, projection); err != nil {
		t.Fatalf("ApplyProjection: %v", err)
	}
	if len(writer.evidence) != 1 || len(writer.links) != 1 {
		t.Fatalf("writes = evidence:%d links:%d", len(writer.evidence), len(writer.links))
	}
	link := writer.links[0]
	if link.Status != "confirmed" || link.FromObjectID != "project-1" || link.ToObjectID != "tool-1" || len(link.EvidenceRefs) != 1 {
		t.Fatalf("link = %+v", link)
	}
	if link.Properties["document_id"] != "doc-1" || link.Properties["revision"] != 3 || link.Properties["content_hash"] != "sha256:abc" {
		t.Fatalf("link properties = %+v", link.Properties)
	}
	if writer.evidence[0].SourceRef.Kind != "knowledge_revision" || writer.evidence[0].SourceRef.ID != "doc-1@3" {
		t.Fatalf("evidence = %+v", writer.evidence[0])
	}
}

func TestGraphApplierSkipsUnresolvedAssertions(t *testing.T) {
	writer := &recordingGraphWriter{}
	applier := NewGraphApplier(writer, allowLinkTypes{})
	projection := Projection{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Revision: 1, Assertions: []AssertionProjection{{Status: AssertionUnresolved}}}

	if err := applier.ApplyProjection(context.Background(), Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1"}, projection); err != nil {
		t.Fatalf("ApplyProjection: %v", err)
	}
	if len(writer.evidence) != 0 || len(writer.links) != 0 {
		t.Fatalf("unresolved assertion was written")
	}
}

func TestGraphApplierConnectsKnowledgeDocumentToMentionedEntityAndRemovesStaleLinks(t *testing.T) {
	writer := &recordingGraphWriter{}
	applier := NewGraphApplier(writer, allowLinkTypes{})
	projection := Projection{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "provider-doc-1",
		KnowledgeDocumentObjectID: "knowledge-doc-1", Revision: 2, ContentHash: "sha256:current",
		Subjects: []SubjectProjection{{
			Reference: ObjectRef{Kind: "project", ID: "project-1"}, ObjectID: "project-1",
			Resolution: ResolutionResolved, Status: AssertionConfirmed, Surface: "Orrery", Confidence: 0.85,
			Evidence: Evidence{Anchor: "markdown:line:2", ExtractionMethod: "deterministic_exact_entity_match"},
		}},
		ExistingLinks: []contextgraph.Link{{
			ID: "stale-link", TenantID: "tenant-1", LinkType: "knowledge_document_mentions_project", SourceStreamID: "knowledge_revision:provider-doc-1",
		}},
	}
	binding := Binding{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1",
		SourceAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
	}

	if err := applier.ApplyProjection(context.Background(), binding, projection); err != nil {
		t.Fatalf("ApplyProjection: %v", err)
	}
	if len(writer.links) != 1 || len(writer.evidence) != 1 {
		t.Fatalf("writes = links:%d evidence:%d", len(writer.links), len(writer.evidence))
	}
	link := writer.links[0]
	if link.LinkType != "knowledge_document_mentions_project" ||
		link.FromObjectID != "knowledge-doc-1" || link.ToObjectID != "project-1" ||
		link.Status != "confirmed" || link.Confidence != 0.85 {
		t.Fatalf("mention link = %+v", link)
	}
	if link.Properties["provider_document_id"] != "provider-doc-1" || link.SourceStreamID != "knowledge_revision:provider-doc-1" {
		t.Fatalf("mention provenance = %+v", link)
	}
	if len(writer.deleted) != 1 || writer.deleted[0] != "stale-link" {
		t.Fatalf("deleted links = %+v", writer.deleted)
	}
}

func TestGraphApplierReplacesStaleExplicitAssertionAndEvidence(t *testing.T) {
	writer := &recordingGraphWriter{}
	applier := NewGraphApplier(writer, allowLinkTypes{})
	oldEvidence := contextgraph.Evidence{
		ID: "old-evidence", TenantID: "tenant-1",
		SourceRef: contextgraph.Ref{Kind: "knowledge_revision", ID: "doc-1@1"},
	}
	projection := Projection{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Revision: 2,
		Assertions: []AssertionProjection{{
			LinkType: "project_uses_tool", FromObjectID: "project-1", ToObjectID: "tool-1", Status: AssertionConfirmed,
			Evidence: Evidence{Anchor: "frontmatter:ontology.assertions[0]"},
		}},
		ExistingLinks: []contextgraph.Link{{
			ID: "old-assertion", TenantID: "tenant-1", LinkType: "project_uses_tool", SourceStreamID: "knowledge_revision:doc-1",
		}},
		ExistingEvidence: []contextgraph.Evidence{oldEvidence},
	}

	if err := applier.ApplyProjection(context.Background(), Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1"}, projection); err != nil {
		t.Fatalf("ApplyProjection: %v", err)
	}
	if len(writer.deleted) != 1 || writer.deleted[0] != "old-assertion" {
		t.Fatalf("deleted links = %+v", writer.deleted)
	}
	if len(writer.deletedEvidence) != 1 || writer.deletedEvidence[0] != "old-evidence" {
		t.Fatalf("deleted evidence = %+v", writer.deletedEvidence)
	}
}

func TestGraphApplierTombstoneDeletesOnlyDocumentOwnedArtifacts(t *testing.T) {
	writer := &recordingGraphWriter{}
	applier := NewGraphApplier(writer, allowLinkTypes{})
	links := []contextgraph.Link{
		{ID: "target-link", TenantID: "tenant-1", SourceStreamID: "knowledge_revision:doc-1"},
		{ID: "other-link", TenantID: "tenant-1", SourceStreamID: "knowledge_revision:doc-10"},
	}
	evidence := []contextgraph.Evidence{
		{ID: "target-evidence", TenantID: "tenant-1", SourceRef: contextgraph.Ref{Kind: "knowledge_revision", ID: "doc-1@2"}},
		{ID: "other-evidence", TenantID: "tenant-1", SourceRef: contextgraph.Ref{Kind: "knowledge_revision", ID: "doc-10@2"}},
	}

	if err := applier.DeleteDocumentProjection(context.Background(), Binding{TenantID: "tenant-1"}, "doc-1", links, evidence); err != nil {
		t.Fatalf("DeleteDocumentProjection: %v", err)
	}
	if len(writer.deleted) != 1 || writer.deleted[0] != "target-link" {
		t.Fatalf("deleted links = %+v", writer.deleted)
	}
	if len(writer.deletedEvidence) != 1 || writer.deletedEvidence[0] != "target-evidence" {
		t.Fatalf("deleted evidence = %+v", writer.deletedEvidence)
	}
}

func TestGraphApplierRequiresProjectedKnowledgeDocumentForResolvedMention(t *testing.T) {
	writer := &recordingGraphWriter{}
	applier := NewGraphApplier(writer, allowLinkTypes{})
	err := applier.ApplyProjection(context.Background(), Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1"}, Projection{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "provider-doc-1", Revision: 1,
		Subjects: []SubjectProjection{{
			Reference: ObjectRef{Kind: "project", ID: "project-1"}, ObjectID: "project-1",
			Resolution: ResolutionResolved, Status: AssertionConfirmed,
		}},
	})
	if err == nil {
		t.Fatal("expected missing knowledge document projection to fail")
	}
}

func TestOntologyEntityNeighborhoodUnifiesKnowledgeAndMemory(t *testing.T) {
	ctx := context.Background()
	registry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	graph := contextgraph.NewMemoryStore()
	now := projectionObservedAt(time.Time{})
	for _, object := range []contextgraph.Object{
		{ID: "project-1", TenantID: "tenant-1", ObjectType: "project", Label: "Orrery", UpdatedAt: now},
		{ID: "knowledge-doc-1", TenantID: "tenant-1", ObjectType: "knowledge_document", Label: "Orrery architecture", UpdatedAt: now},
		{ID: "memory-1", TenantID: "tenant-1", ObjectType: "memory_record", Label: "Orrery decision", UpdatedAt: now},
	} {
		if err := graph.SaveObject(ctx, object); err != nil {
			t.Fatalf("SaveObject(%s): %v", object.ID, err)
		}
	}
	if err := graph.SaveLink(ctx, contextgraph.Link{
		ID: "memory-link-1", TenantID: "tenant-1", LinkType: "memory_mentions_project",
		FromObjectID: "memory-1", ToObjectID: "project-1", Status: "confirmed", UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveLink(memory): %v", err)
	}
	applier := NewGraphApplier(graph, NewOntologyLinkValidator(registry, graph))
	if err := applier.ApplyProjection(ctx, Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1"}, Projection{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "provider-doc-1",
		KnowledgeDocumentObjectID: "knowledge-doc-1", Revision: 1, ContentHash: "sha256:abc",
		Subjects: []SubjectProjection{{
			Reference: ObjectRef{Kind: "project", ID: "project-1"}, ObjectID: "project-1",
			Resolution: ResolutionResolved, Status: AssertionConfirmed, Surface: "Orrery", Confidence: 0.85,
		}},
	}); err != nil {
		t.Fatalf("ApplyProjection: %v", err)
	}
	bundle, err := contextgraph.NewService(graph).BuildContextBundle(ctx, contextgraph.BuildContextBundleRequest{
		TenantID: "tenant-1", Consumer: "agent-runtime", Purpose: "unified-retrieval",
		ActorRef:    contextgraph.Ref{Kind: "agent", ID: "agent-1"},
		SubjectRefs: []contextgraph.Ref{{Kind: "project", ID: "project-1"}}, MaxDepth: 1,
	})
	if err != nil {
		t.Fatalf("BuildContextBundle: %v", err)
	}
	found := map[string]bool{}
	for _, object := range bundle.Objects {
		found[object.ObjectType+":"+object.ID] = true
	}
	for _, key := range []string{"project:project-1", "knowledge_document:knowledge-doc-1", "memory_record:memory-1"} {
		if !found[key] {
			t.Fatalf("unified object %s missing from %+v", key, bundle.Objects)
		}
	}
	if len(bundle.Links) != 2 {
		t.Fatalf("unified links = %+v", bundle.Links)
	}
}
