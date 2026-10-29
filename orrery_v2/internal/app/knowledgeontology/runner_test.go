package knowledgeontology

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/knowledge/provider"
)

type fakeRevisionSource struct {
	changes  provider.KnowledgeChangePage
	revision provider.DocumentRevision
	source   provider.SemanticSource
	err      error
}

type fakeIndexedRevisionSource struct {
	*fakeRevisionSource
	indexed   provider.IndexedDocumentSource
	documents []provider.FolderDocument
}

func (f *fakeIndexedRevisionSource) ListFolderDocuments(context.Context, provider.FolderDocumentsRequest) ([]provider.FolderDocument, error) {
	return append([]provider.FolderDocument(nil), f.documents...), f.err
}
func (f *fakeIndexedRevisionSource) GetIndexedDocumentSource(context.Context, provider.IndexedDocumentSourceRequest) (provider.IndexedDocumentSource, error) {
	return f.indexed, f.err
}

func (f *fakeRevisionSource) UpdateDocumentContent(context.Context, provider.UpdateDocumentContentRequest) (provider.DocumentRevision, error) {
	return provider.DocumentRevision{}, errors.New("not implemented")
}
func (f *fakeRevisionSource) ListDocumentRevisions(context.Context, provider.DocumentRevisionsRequest) ([]provider.DocumentRevision, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeRevisionSource) GetDocumentRevision(context.Context, provider.DocumentRevisionRequest) (provider.DocumentRevision, error) {
	return f.revision, f.err
}
func (f *fakeRevisionSource) GetSemanticSource(context.Context, provider.SemanticSourceRequest) (provider.SemanticSource, error) {
	return f.source, f.err
}
func (f *fakeRevisionSource) PublishDocumentRevision(context.Context, provider.PublishDocumentRevisionRequest) (provider.DocumentRevision, error) {
	return provider.DocumentRevision{}, errors.New("not implemented")
}
func (f *fakeRevisionSource) ListKnowledgeChanges(context.Context, provider.KnowledgeChangesRequest) (provider.KnowledgeChangePage, error) {
	return f.changes, f.err
}

type fakeSnapshot struct {
	objects  []contextgraph.Object
	links    []contextgraph.Link
	facts    []contextgraph.Fact
	evidence []contextgraph.Evidence
}

func (f fakeSnapshot) Snapshot(context.Context, string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error) {
	return f.objects, f.links, f.facts, f.evidence, nil
}

type memoryBridgeStore struct {
	cursor      string
	projections []Projection
	deleted     []string
	fail        bool
}

type versionedMemoryBridgeStore struct {
	memoryBridgeStore
	dictionaryVersion string
}

func (s *versionedMemoryBridgeStore) OntologyDictionaryVersion(context.Context, Binding) (string, error) {
	return s.dictionaryVersion, nil
}

func (s *versionedMemoryBridgeStore) SaveOntologyDictionaryVersion(_ context.Context, _ Binding, version string) error {
	s.dictionaryVersion = version
	return nil
}

func (s *memoryBridgeStore) Cursor(context.Context, Binding) (string, error) { return s.cursor, nil }
func (s *memoryBridgeStore) SaveProjectionAndCursor(_ context.Context, _ Binding, projection Projection, cursor string) error {
	if s.fail {
		return errors.New("save failed")
	}
	s.projections = append(s.projections, projection)
	s.cursor = cursor
	return nil
}
func (s *memoryBridgeStore) AdvanceCursor(_ context.Context, _ Binding, cursor string) error {
	if s.fail {
		return errors.New("save failed")
	}
	s.cursor = cursor
	return nil
}
func (s *memoryBridgeStore) DeleteDocument(_ context.Context, _ Binding, documentID string) error {
	if s.fail {
		return errors.New("delete failed")
	}
	filtered := s.projections[:0]
	for _, projection := range s.projections {
		if projection.DocumentID != documentID {
			filtered = append(filtered, projection)
		}
	}
	s.projections = filtered
	s.deleted = append(s.deleted, documentID)
	return nil
}
func (s *memoryBridgeStore) ListProjectedDocumentIDs(context.Context, Binding) ([]string, error) {
	seen := map[string]struct{}{}
	documentIDs := []string{}
	for _, projection := range s.projections {
		if _, exists := seen[projection.DocumentID]; exists {
			continue
		}
		seen[projection.DocumentID] = struct{}{}
		documentIDs = append(documentIDs, projection.DocumentID)
	}
	return documentIDs, nil
}

type fakeReconciler struct {
	called bool
	cursor string
	err    error
}

func (r *fakeReconciler) Reconcile(context.Context, Binding) (string, error) {
	r.called = true
	return r.cursor, r.err
}

func TestRunnerProjectsOnlyPublishedExactRevisionAndAdvancesCursor(t *testing.T) {
	now := time.Date(2026, 7, 24, 2, 0, 0, 0, time.UTC)
	hash := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	source := &fakeRevisionSource{
		changes: provider.KnowledgeChangePage{NextCursor: "8", Items: []provider.KnowledgeChange{
			{Sequence: 7, FolderID: "folder-1", DocumentID: "doc-1", Revision: 2, EventType: "document.revision.created", ContentHash: hash, OccurredAt: now},
			{Sequence: 8, FolderID: "folder-1", DocumentID: "doc-1", Revision: 2, EventType: "document.published", ContentHash: hash, OccurredAt: now},
		}},
		revision: provider.DocumentRevision{DocumentID: "doc-1", FolderID: "folder-1", Revision: 2, ContentHash: hash, ContentType: "text/markdown", PublicationStatus: "published", PublishedAt: &now},
		source: provider.SemanticSource{DocumentID: "doc-1", Revision: 2, ContentHash: hash, ContentType: "text/markdown", Editable: true, Content: `---
ontology:
  assertions:
    - link_type: project_uses_tool
      from: {kind: project, id: orrery}
      to: {kind: tool, id: pageindex}
---
`},
	}
	store := &memoryBridgeStore{}
	runner := NewRunner(source, fakeSnapshot{objects: []contextgraph.Object{
		{ID: "project-1", TenantID: "tenant-1", ObjectType: "project", CanonicalKey: "project:orrery"},
		{ID: "tool-1", TenantID: "tenant-1", ObjectType: "tool", CanonicalKey: "tool:pageindex"},
	}}, store, nil)

	report, err := runner.Run(context.Background(), Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1"}, 20)

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Events != 2 || report.Projected != 1 || store.cursor != "8" || len(store.projections) != 1 {
		t.Fatalf("report/store = %+v cursor=%q projections=%d", report, store.cursor, len(store.projections))
	}
	if store.projections[0].Assertions[0].Status != AssertionConfirmed {
		t.Fatalf("projection = %+v", store.projections[0])
	}
}

func TestRunnerProjectsIndexedDocumentSnapshotAndAdvancesCursor(t *testing.T) {
	now := time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC)
	hash := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	source := &fakeIndexedRevisionSource{
		fakeRevisionSource: &fakeRevisionSource{
			changes: provider.KnowledgeChangePage{
				NextCursor: "13",
				Items: []provider.KnowledgeChange{{
					Sequence: 13, FolderID: "folder-1", DocumentID: "doc-1", EventType: "document.indexed", OccurredAt: now,
				}},
			},
		},
		documents: []provider.FolderDocument{{DocumentID: "doc-1", FolderID: "folder-1", Status: "indexed"}},
		indexed: provider.IndexedDocumentSource{
			Revision: provider.DocumentRevision{
				DocumentID: "doc-1", FolderID: "folder-1", Revision: 1, ContentHash: hash,
				ContentType: "text/markdown", PublicationStatus: "published", PublishedAt: &now,
			},
			Source: provider.SemanticSource{
				DocumentID: "doc-1", Revision: 1, ContentHash: hash, ContentType: "text/markdown",
				Content: "Orrery uses PageIndex.",
			},
		},
	}
	store := &memoryBridgeStore{}
	runner := NewRunner(source, fakeSnapshot{objects: []contextgraph.Object{{
		ID: "project-1", TenantID: "tenant-1", ObjectType: "project", Label: "Orrery",
	}}}, store, nil)

	report, err := runner.Run(context.Background(), Binding{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1",
	}, 20)

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Events != 1 || report.Projected != 1 || store.cursor != "13" || len(store.projections) != 1 {
		t.Fatalf("report/store = %+v cursor=%q projections=%d", report, store.cursor, len(store.projections))
	}
}

func TestRunnerReconcilesIndexedDocumentMissingFromProjectionSet(t *testing.T) {
	source := &fakeIndexedRevisionSource{
		fakeRevisionSource: &fakeRevisionSource{},
		documents: []provider.FolderDocument{{
			DocumentID: "doc-missing", FolderID: "folder-1", Status: "indexed",
		}},
	}
	store := &memoryBridgeStore{}
	reconciler := &fakeReconciler{cursor: "21"}
	runner := NewRunner(source, fakeSnapshot{}, store, reconciler)

	report, err := runner.Run(context.Background(), Binding{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1",
	}, 20)

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.Reconciled || !reconciler.called || store.cursor != "21" {
		t.Fatalf("report/reconciler/cursor = %+v %t %q", report, reconciler.called, store.cursor)
	}
}

func TestRunnerDoesNotReconcileNonIndexedDocumentMissingFromProjectionSet(t *testing.T) {
	source := &fakeIndexedRevisionSource{
		fakeRevisionSource: &fakeRevisionSource{},
		documents: []provider.FolderDocument{
			{DocumentID: "doc-processing", FolderID: "folder-1", Status: "processing"},
			{DocumentID: "doc-failed", FolderID: "folder-1", Status: "failed"},
		},
	}
	store := &memoryBridgeStore{projections: []Projection{{DocumentID: "doc-failed"}}}
	reconciler := &fakeReconciler{cursor: "21"}
	runner := NewRunner(source, fakeSnapshot{}, store, reconciler)

	report, err := runner.Run(context.Background(), Binding{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1",
	}, 20)

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Reconciled || reconciler.called || report.Tombstoned != 0 || len(store.deleted) != 0 {
		t.Fatalf("non-indexed documents changed projection state: report=%+v called=%t deleted=%v", report, reconciler.called, store.deleted)
	}
}

func TestRunnerDoesNotAdvanceCursorWhenContentHashDiffers(t *testing.T) {
	now := time.Now().UTC()
	source := &fakeRevisionSource{
		changes:  provider.KnowledgeChangePage{NextCursor: "1", Items: []provider.KnowledgeChange{{Sequence: 1, FolderID: "folder-1", DocumentID: "doc-1", Revision: 1, EventType: "document.published", ContentHash: "sha256:event", OccurredAt: now}}},
		revision: provider.DocumentRevision{DocumentID: "doc-1", FolderID: "folder-1", Revision: 1, ContentHash: "sha256:revision", PublicationStatus: "published", PublishedAt: &now},
		source:   provider.SemanticSource{DocumentID: "doc-1", Revision: 1, ContentHash: "sha256:source", ContentType: "text/markdown"},
	}
	store := &memoryBridgeStore{}
	runner := NewRunner(source, fakeSnapshot{}, store, nil)

	_, err := runner.Run(context.Background(), Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1"}, 20)

	if !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("Run error = %v", err)
	}
	if store.cursor != "" || len(store.projections) != 0 {
		t.Fatalf("cursor/projections advanced: %q/%d", store.cursor, len(store.projections))
	}
}

func TestRunnerTombstonesUnpublishedDocumentBeforeAdvancingCursor(t *testing.T) {
	source := &fakeRevisionSource{changes: provider.KnowledgeChangePage{Items: []provider.KnowledgeChange{{
		Sequence: 9, FolderID: "folder-1", DocumentID: "doc-1", EventType: "document.unpublished",
	}}}}
	store := &memoryBridgeStore{projections: []Projection{{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", DocumentID: "doc-1", Revision: 1,
	}}}
	writer := &recordingGraphWriter{}
	runner := NewRunner(source, fakeSnapshot{
		links: []contextgraph.Link{{
			ID: "link-1", TenantID: "tenant-1", SourceStreamID: "knowledge_revision:doc-1",
		}},
		evidence: []contextgraph.Evidence{{
			ID: "evidence-1", TenantID: "tenant-1", SourceRef: contextgraph.Ref{Kind: "knowledge_revision", ID: "doc-1@1"},
		}},
	}, store, nil, NewGraphApplier(writer, allowLinkTypes{}))

	report, err := runner.Run(context.Background(), Binding{
		TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1",
	}, 20)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Events != 1 || report.Tombstoned != 1 || report.Projected != 0 || store.cursor != "9" {
		t.Fatalf("report/store = %+v cursor=%q", report, store.cursor)
	}
	if len(store.projections) != 0 || len(store.deleted) != 1 || store.deleted[0] != "doc-1" {
		t.Fatalf("deleted projections = %+v, deleted=%+v", store.projections, store.deleted)
	}
	if len(writer.deleted) != 1 || writer.deleted[0] != "link-1" || len(writer.deletedEvidence) != 1 || writer.deletedEvidence[0] != "evidence-1" {
		t.Fatalf("graph deletes = links:%+v evidence:%+v", writer.deleted, writer.deletedEvidence)
	}
}

func TestRunnerReconcilesExpiredCursorBeforeAdvancing(t *testing.T) {
	source := &fakeRevisionSource{err: provider.ErrChangeCursorExpired}
	store := &memoryBridgeStore{cursor: "5"}
	reconciler := &fakeReconciler{cursor: "9"}
	runner := NewRunner(source, fakeSnapshot{}, store, reconciler)

	report, err := runner.Run(context.Background(), Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1"}, 20)

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reconciler.called || !report.Reconciled || store.cursor != "9" {
		t.Fatalf("report/reconciler/cursor = %+v %v %q", report, reconciler.called, store.cursor)
	}
}

func TestRunnerReconcilesWhenOntologyEntityDictionaryChanges(t *testing.T) {
	store := &versionedMemoryBridgeStore{}
	reconciler := &fakeReconciler{cursor: "9"}
	runner := NewRunner(&fakeRevisionSource{}, fakeSnapshot{objects: []contextgraph.Object{{
		ID: "project-1", TenantID: "tenant-1", ObjectType: "project", Label: "Orrery",
	}}}, store, reconciler)
	binding := Binding{TenantID: "tenant-1", KnowledgeBaseID: "kb-1", FolderID: "folder-1"}

	report, err := runner.Run(context.Background(), binding, 20)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.Reconciled || !reconciler.called || store.cursor != "9" || store.dictionaryVersion == "" {
		t.Fatalf("report/store = %+v %+v", report, store)
	}
	reconciler.called = false
	report, err = runner.Run(context.Background(), binding, 20)
	if err != nil || report.Reconciled || reconciler.called {
		t.Fatalf("unchanged dictionary report=%+v reconciled=%t err=%v", report, reconciler.called, err)
	}
}
