package knowledgeontology

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
)

type reconcileSource struct {
	documents []provider.FolderDocument
	indexed   provider.IndexedDocumentSource
	revisions []provider.DocumentRevision
	sources   map[int]provider.SemanticSource
	feeds     []provider.KnowledgeChangePage
	feedCall  int
}

func (s *reconcileSource) ListFolderDocuments(context.Context, provider.FolderDocumentsRequest) ([]provider.FolderDocument, error) {
	return append([]provider.FolderDocument(nil), s.documents...), nil
}

func (s *reconcileSource) GetIndexedDocumentSource(context.Context, provider.IndexedDocumentSourceRequest) (provider.IndexedDocumentSource, error) {
	return s.indexed, nil
}

func (s *reconcileSource) UpdateDocumentContent(context.Context, provider.UpdateDocumentContentRequest) (provider.DocumentRevision, error) {
	return provider.DocumentRevision{}, nil
}
func (s *reconcileSource) ListDocumentRevisions(context.Context, provider.DocumentRevisionsRequest) ([]provider.DocumentRevision, error) {
	return s.revisions, nil
}
func (s *reconcileSource) GetDocumentRevision(_ context.Context, req provider.DocumentRevisionRequest) (provider.DocumentRevision, error) {
	for _, revision := range s.revisions {
		if revision.Revision == req.Revision {
			return revision, nil
		}
	}
	return provider.DocumentRevision{}, nil
}
func (s *reconcileSource) GetSemanticSource(_ context.Context, req provider.SemanticSourceRequest) (provider.SemanticSource, error) {
	return s.sources[req.Revision], nil
}
func (s *reconcileSource) PublishDocumentRevision(context.Context, provider.PublishDocumentRevisionRequest) (provider.DocumentRevision, error) {
	return provider.DocumentRevision{}, nil
}
func (s *reconcileSource) ListKnowledgeChanges(context.Context, provider.KnowledgeChangesRequest) (provider.KnowledgeChangePage, error) {
	if s.feedCall >= len(s.feeds) {
		return provider.KnowledgeChangePage{}, nil
	}
	page := s.feeds[s.feedCall]
	s.feedCall++
	return page, nil
}

type reconcileDocuments struct{ page knowledgecore.DocumentPage }

func (d reconcileDocuments) ListDocuments(context.Context, knowledgecore.DocumentQuery) (knowledgecore.DocumentPage, error) {
	return d.page, nil
}

func TestFullReconcilerProjectsPublishedRevisionsAndReturnsLatestCursor(t *testing.T) {
	now := time.Date(2026, 7, 24, 4, 0, 0, 0, time.UTC)
	hash := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	source := &reconcileSource{
		documents: []provider.FolderDocument{{DocumentID: "doc-1", FolderID: "folder-1", Status: "indexed"}},
		revisions: []provider.DocumentRevision{
			{DocumentID: "doc-1", FolderID: "folder-1", Revision: 1, ContentHash: "draft", PublicationStatus: "draft"},
			{DocumentID: "doc-1", FolderID: "folder-1", Revision: 2, ContentHash: hash, ContentType: "text/markdown", PublicationStatus: "published", PublishedAt: &now},
		},
		sources: map[int]provider.SemanticSource{2: {
			DocumentID: "doc-1", Revision: 2, ContentHash: hash, ContentType: "text/markdown",
			Content: "---\nontology:\n  assertions:\n    - link_type: project_uses_tool\n      from: {kind: project, id: orrery}\n      to: {kind: tool, id: pageindex}\n---\n",
		}},
		feeds: []provider.KnowledgeChangePage{{NextCursor: "12", Items: []provider.KnowledgeChange{{Sequence: 12}}}},
	}
	store := &memoryBridgeStore{}
	graph := fakeSnapshot{objects: []contextgraph.Object{
		{ID: "project-1", TenantID: "tenant-1", ObjectType: "project", CanonicalKey: "project:orrery"},
		{ID: "tool-1", TenantID: "tenant-1", ObjectType: "tool", CanonicalKey: "tool:pageindex"},
	}}
	runner := NewRunner(source, graph, store, nil)
	reconciler := NewFullReconciler(source, reconcileDocuments{}, runner, store, nil)

	cursor, err := reconciler.Reconcile(context.Background(), Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1"})

	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if cursor != "12" || len(store.projections) != 1 || store.projections[0].Revision != 2 {
		t.Fatalf("cursor/projections = %q %+v", cursor, store.projections)
	}
}

func TestFullReconcilerSkipsNonIndexedFolderDocuments(t *testing.T) {
	source := &reconcileSource{documents: []provider.FolderDocument{
		{DocumentID: "indexed-doc", FolderID: "folder-1", Status: "indexed"},
		{DocumentID: "failed-doc", FolderID: "folder-1", Status: "failed"},
		{DocumentID: "parsing-doc", FolderID: "folder-1", Status: "parsing"},
	}}
	reconciler := &FullReconciler{source: source}

	documentIDs, err := reconciler.documentIDs(context.Background(), Binding{FolderID: "folder-1"})
	if err != nil {
		t.Fatalf("documentIDs: %v", err)
	}
	if len(documentIDs) != 1 || documentIDs[0] != "indexed-doc" {
		t.Fatalf("document IDs = %+v", documentIDs)
	}
}

func TestFullReconcilerProjectsLegacyIndexedDocumentWithoutRevisionRows(t *testing.T) {
	now := time.Date(2026, 8, 21, 8, 0, 0, 0, time.UTC)
	hash := "sha256:legacy"
	source := &reconcileSource{
		documents: []provider.FolderDocument{{DocumentID: "legacy-doc", FolderID: "folder-1", Status: "indexed"}},
		indexed: provider.IndexedDocumentSource{
			Revision: provider.DocumentRevision{
				DocumentID: "legacy-doc", FolderID: "folder-1", Revision: 1,
				ContentHash: hash, ContentType: "text/markdown", PublicationStatus: "published", PublishedAt: &now,
			},
			Source: provider.SemanticSource{
				DocumentID: "legacy-doc", Revision: 1, ContentHash: hash,
				ContentType: "text/markdown", Content: "Orrery uses PageIndex.",
			},
		},
	}
	store := &memoryBridgeStore{}
	runner := NewRunner(source, fakeSnapshot{objects: []contextgraph.Object{{
		ID: "project-1", TenantID: "tenant-1", ObjectType: "project", Label: "Orrery",
	}}}, store, nil)
	reconciler := NewFullReconciler(source, reconcileDocuments{}, runner, store, nil)

	if _, err := reconciler.Reconcile(context.Background(), Binding{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1",
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(store.projections) != 1 || store.projections[0].DocumentID != "legacy-doc" || len(store.projections[0].Subjects) != 1 {
		t.Fatalf("legacy projections = %+v", store.projections)
	}
}

func TestFullReconcilerRemovesProjectionMissingFromFolder(t *testing.T) {
	source := &reconcileSource{}
	store := &memoryBridgeStore{projections: []Projection{{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "deleted-doc", Revision: 1,
	}}}
	runner := NewRunner(source, fakeSnapshot{}, store, nil)
	reconciler := NewFullReconciler(source, reconcileDocuments{}, runner, store, nil)

	if _, err := reconciler.Reconcile(context.Background(), Binding{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1",
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(store.projections) != 0 || len(store.deleted) != 1 || store.deleted[0] != "deleted-doc" {
		t.Fatalf("stale projections = %+v, deleted=%+v", store.projections, store.deleted)
	}
}
