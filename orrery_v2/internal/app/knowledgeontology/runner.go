package knowledgeontology

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/knowledge/provider"
)

var ErrRevisionMismatch = errors.New("knowledge revision evidence mismatch")

type Binding struct {
	TenantID        string                 `json:"tenant_id"`
	KnowledgeBaseID string                 `json:"knowledge_base_id"`
	FolderID        string                 `json:"folder_id"`
	SourceAccessRef contextgraph.AccessRef `json:"source_access_ref"`
	Enabled         bool                   `json:"enabled"`
	UpdatedAt       time.Time              `json:"updated_at"`
}

type GraphSnapshot interface {
	Snapshot(ctx context.Context, tenantID string) ([]contextgraph.Object, []contextgraph.Link, []contextgraph.Fact, []contextgraph.Evidence, error)
}

type BridgeStore interface {
	Cursor(ctx context.Context, binding Binding) (string, error)
	SaveProjectionAndCursor(ctx context.Context, binding Binding, projection Projection, cursor string) error
	AdvanceCursor(ctx context.Context, binding Binding, cursor string) error
	DeleteDocument(ctx context.Context, binding Binding, documentID string) error
	ListProjectedDocumentIDs(ctx context.Context, binding Binding) ([]string, error)
}

type OntologyDictionaryVersionStore interface {
	OntologyDictionaryVersion(context.Context, Binding) (string, error)
	SaveOntologyDictionaryVersion(context.Context, Binding, string) error
}

type Reconciler interface {
	Reconcile(ctx context.Context, binding Binding) (cursor string, err error)
}

type ProjectionApplier interface {
	ApplyProjection(ctx context.Context, binding Binding, projection Projection) error
}

type ProjectionTombstoneApplier interface {
	DeleteDocumentProjection(ctx context.Context, binding Binding, documentID string, links []contextgraph.Link, evidence []contextgraph.Evidence) error
}

type RunReport struct {
	Events     int  `json:"events"`
	Projected  int  `json:"projected"`
	Tombstoned int  `json:"tombstoned"`
	Reconciled bool `json:"reconciled"`
}

type Runner struct {
	source     provider.RevisionProvider
	graph      GraphSnapshot
	store      BridgeStore
	reconciler Reconciler
	applier    ProjectionApplier
}

func NewRunner(source provider.RevisionProvider, graph GraphSnapshot, store BridgeStore, reconciler Reconciler, appliers ...ProjectionApplier) *Runner {
	runner := &Runner{source: source, graph: graph, store: store, reconciler: reconciler}
	if len(appliers) > 0 {
		runner.applier = appliers[0]
	}
	return runner
}

func NewSynchronizedRunner(source provider.RevisionProvider, graph GraphSnapshot, store BridgeStore, docs DocumentLister, applier ProjectionApplier) *Runner {
	runner := NewRunner(source, graph, store, nil, applier)
	runner.reconciler = NewFullReconciler(source, docs, runner, store, applier)
	return runner
}

func (r *Runner) Run(ctx context.Context, binding Binding, limit int) (RunReport, error) {
	if r == nil || r.source == nil || r.graph == nil || r.store == nil {
		return RunReport{}, fmt.Errorf("knowledge ontology runner dependencies are required")
	}
	if strings.TrimSpace(binding.TenantID) == "" || strings.TrimSpace(binding.KnowledgeBaseID) == "" || strings.TrimSpace(binding.FolderID) == "" {
		return RunReport{}, fmt.Errorf("tenant, knowledge base, and PageIndex folder are required")
	}
	if limit < 1 || limit > 200 {
		return RunReport{}, fmt.Errorf("change page limit must be between 1 and 200")
	}
	if store, ok := r.lifecycleStore(); ok {
		if err := store.recoverSourceHeads(ctx, binding); err != nil {
			return RunReport{}, err
		}
	}
	if versionStore, ok := r.store.(OntologyDictionaryVersionStore); ok && r.reconciler != nil {
		objects, err := graphObjects(ctx, r.graph, binding.TenantID)
		if err != nil {
			return RunReport{}, err
		}
		currentVersion := knowledgeEntityDictionaryVersion(objects)
		storedVersion, err := versionStore.OntologyDictionaryVersion(ctx, binding)
		if err != nil {
			return RunReport{}, err
		}
		if storedVersion != currentVersion {
			reconciledCursor, err := r.reconciler.Reconcile(ctx, binding)
			if err != nil {
				return RunReport{}, err
			}
			if err := r.store.AdvanceCursor(ctx, binding, reconciledCursor); err != nil {
				return RunReport{}, err
			}
			if err := versionStore.SaveOntologyDictionaryVersion(ctx, binding, currentVersion); err != nil {
				return RunReport{}, err
			}
			return RunReport{Reconciled: true}, nil
		}
	}
	cursor, err := r.store.Cursor(ctx, binding)
	if err != nil {
		return RunReport{}, err
	}
	page, err := r.source.ListKnowledgeChanges(ctx, provider.KnowledgeChangesRequest{
		FolderID: binding.FolderID, Cursor: cursor, Limit: limit,
	})
	if errors.Is(err, provider.ErrChangeCursorExpired) {
		if r.reconciler == nil {
			return RunReport{}, fmt.Errorf("%w: full reconciliation is required", err)
		}
		reconciledCursor, reconcileErr := r.reconciler.Reconcile(ctx, binding)
		if reconcileErr != nil {
			return RunReport{}, reconcileErr
		}
		if err := r.store.AdvanceCursor(ctx, binding, reconciledCursor); err != nil {
			return RunReport{}, err
		}
		return RunReport{Reconciled: true}, nil
	}
	if err != nil {
		return RunReport{}, err
	}

	report := RunReport{}
	initialSequence, _ := strconv.ParseInt(cursor, 10, 64)
	for _, change := range page.Items {
		report.Events++
		if initialSequence > 0 && change.Sequence <= initialSequence {
			continue
		}
		eventCursor := strconv.FormatInt(change.Sequence, 10)
		if isDocumentTombstoneEvent(change.EventType) {
			if strings.TrimSpace(change.FolderID) != binding.FolderID || strings.TrimSpace(change.DocumentID) == "" {
				return report, ErrRevisionMismatch
			}
			if store, ok := r.lifecycleStore(); ok {
				applied, err := store.applyTombstone(ctx, binding, change)
				if err != nil {
					return report, err
				}
				if applied {
					report.Tombstoned++
				}
				if err := r.store.AdvanceCursor(ctx, binding, eventCursor); err != nil {
					return report, err
				}
				continue
			}
			if err := r.deleteDocumentProjection(ctx, binding, change.DocumentID); err != nil {
				return report, err
			}
			if err := r.store.AdvanceCursor(ctx, binding, eventCursor); err != nil {
				return report, err
			}
			report.Tombstoned++
			continue
		}
		var projection Projection
		var err error
		switch strings.TrimSpace(change.EventType) {
		case "document.published":
			projection, err = r.projectChange(ctx, binding, change)
		case "document.indexed":
			projection, err = r.projectIndexedChange(ctx, binding, change)
		default:
			if err := r.store.AdvanceCursor(ctx, binding, eventCursor); err != nil {
				return report, err
			}
			continue
		}
		if err != nil {
			return report, err
		}
		if err := r.applyAndSave(ctx, binding, projection, eventCursor, nil, 0); err != nil {
			return report, err
		}
		report.Projected++
	}
	removed, missing, err := r.synchronizeDocumentSet(ctx, binding)
	if err != nil {
		return report, err
	}
	report.Tombstoned += removed
	if missing && r.reconciler != nil {
		reconciledCursor, err := r.reconciler.Reconcile(ctx, binding)
		if err != nil {
			return report, err
		}
		if err := r.store.AdvanceCursor(ctx, binding, reconciledCursor); err != nil {
			return report, err
		}
		report.Reconciled = true
	}
	return report, nil
}

func isDocumentTombstoneEvent(eventType string) bool {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case "document.deleted", "document.unpublished":
		return true
	default:
		return false
	}
}

func (r *Runner) cleanupMissingDocuments(ctx context.Context, binding Binding) (int, error) {
	removed, _, err := r.synchronizeDocumentSet(ctx, binding)
	return removed, err
}

func (r *Runner) synchronizeDocumentSet(ctx context.Context, binding Binding) (int, bool, error) {
	lister, ok := r.source.(provider.FolderDocumentLister)
	if !ok {
		return 0, false, nil
	}
	listDocuments := lister.ListFolderDocuments
	if _, enabled := r.lifecycleStore(); enabled {
		inventory, ok := r.source.(provider.FolderDocumentInventoryProvider)
		if !ok {
			return 0, false, nil
		}
		listDocuments = inventory.ListFolderDocumentInventory
	}
	projected, err := r.store.ListProjectedDocumentIDs(ctx, binding)
	if err != nil {
		return 0, false, err
	}
	hasProjection := map[string]bool{}
	for _, id := range projected {
		hasProjection[id] = true
	}
	if store, ok := r.lifecycleStore(); ok {
		ids, err := store.liveSourceDocumentIDs(ctx, binding)
		if err != nil {
			return 0, false, err
		}
		for _, id := range ids {
			if !hasProjection[id] {
				projected = append(projected, id)
			}
		}
	}
	heads := map[string]SourceHead{}
	if store, ok := r.lifecycleStore(); ok {
		for _, id := range projected {
			h, err := store.SourceHead(ctx, binding, id)
			if err != nil {
				return 0, false, err
			}
			heads[id] = h
		}
	}
	documents, err := listDocuments(ctx, provider.FolderDocumentsRequest{FolderID: binding.FolderID})
	if err != nil {
		return 0, false, err
	}
	existing := make(map[string]struct{}, len(documents))
	indexed := make(map[string]struct{}, len(documents))
	for _, document := range documents {
		if _, enabled := r.lifecycleStore(); enabled && (strings.TrimSpace(document.FolderID) != binding.FolderID || strings.TrimSpace(document.DocumentID) == "") {
			return 0, false, ErrRevisionMismatch
		}
		if documentID := strings.TrimSpace(document.DocumentID); documentID != "" {
			existing[documentID] = struct{}{}
			if strings.EqualFold(strings.TrimSpace(document.Status), "indexed") {
				indexed[documentID] = struct{}{}
			}
		}
	}
	projectedSet := make(map[string]struct{}, len(projected))
	removed := 0
	for _, candidate := range projected {
		documentID := strings.TrimSpace(candidate)
		if documentID == "" {
			continue
		}
		if _, exists := existing[documentID]; exists {
			if hasProjection[documentID] {
				projectedSet[documentID] = struct{}{}
			}
			continue
		}
		if store, ok := r.lifecycleStore(); ok {
			expected := heads[documentID]
			applied, err := store.withdrawDocument(ctx, binding, documentID, "document.missing", 0, &expected)
			if err != nil {
				return removed, false, err
			}
			if applied {
				removed++
			}
			continue
		}
		if err := r.deleteDocumentProjection(ctx, binding, documentID); err != nil {
			return removed, false, err
		}
		removed++
	}
	for documentID := range indexed {
		if _, projected := projectedSet[documentID]; !projected {
			return removed, true, nil
		}
	}
	return removed, false, nil
}

func (r *Runner) lifecycleStore() (*PlatformStore, bool) {
	s, ok := r.store.(*PlatformStore)
	return s, ok && s.withdrawSource != nil
}

func (r *Runner) applyAndSave(ctx context.Context, b Binding, p Projection, cursor string, expected *SourceHead, watermark int64) error {
	if s, ok := r.lifecycleStore(); ok {
		return s.applyPublishedProjection(ctx, b, p, cursor, expected, watermark, r.applier)
	}
	if r.applier != nil {
		if err := r.applier.ApplyProjection(ctx, b, p); err != nil {
			return err
		}
	}
	return r.store.SaveProjectionAndCursor(ctx, b, p, cursor)
}

func (r *Runner) deleteDocumentProjection(ctx context.Context, binding Binding, documentID string) error {
	documentID = strings.TrimSpace(documentID)
	if documentID == "" {
		return fmt.Errorf("knowledge document id is required")
	}
	if r.applier != nil {
		deleter, ok := r.applier.(ProjectionTombstoneApplier)
		if !ok {
			return fmt.Errorf("knowledge projection applier does not support document tombstones")
		}
		links, evidence, err := graphDocumentRelations(ctx, r.graph, binding.TenantID, documentID)
		if err != nil {
			return err
		}
		if err := deleter.DeleteDocumentProjection(ctx, binding, documentID, links, evidence); err != nil {
			return err
		}
	}
	return r.store.DeleteDocument(ctx, binding, documentID)
}

func (r *Runner) projectChange(ctx context.Context, binding Binding, change provider.KnowledgeChange) (Projection, error) {
	revision, err := r.source.GetDocumentRevision(ctx, provider.DocumentRevisionRequest{
		DocumentID: change.DocumentID, Revision: change.Revision,
	})
	if err != nil {
		return Projection{}, err
	}
	source, err := r.source.GetSemanticSource(ctx, provider.SemanticSourceRequest{
		DocumentID: change.DocumentID, Revision: change.Revision,
	})
	if err != nil {
		return Projection{}, err
	}
	if revision.DocumentID != change.DocumentID || revision.Revision != change.Revision || revision.ContentHash != change.ContentHash {
		return Projection{}, ErrRevisionMismatch
	}
	publishedAt := change.OccurredAt
	if revision.PublishedAt != nil {
		publishedAt = *revision.PublishedAt
	}
	return r.projectRevisionSource(ctx, binding, revision, source, publishedAt)
}

func (r *Runner) projectIndexedChange(ctx context.Context, binding Binding, change provider.KnowledgeChange) (Projection, error) {
	if strings.TrimSpace(change.FolderID) != binding.FolderID || strings.TrimSpace(change.DocumentID) == "" {
		return Projection{}, ErrRevisionMismatch
	}
	indexedSource, ok := r.source.(provider.IndexedDocumentSourceProvider)
	if !ok {
		return Projection{}, fmt.Errorf("indexed document source provider is required")
	}
	snapshot, err := indexedSource.GetIndexedDocumentSource(ctx, provider.IndexedDocumentSourceRequest{DocumentID: change.DocumentID})
	if err != nil {
		return Projection{}, err
	}
	publishedAt := snapshot.Revision.CreatedAt
	if snapshot.Revision.PublishedAt != nil {
		publishedAt = *snapshot.Revision.PublishedAt
	}
	return r.projectRevisionSource(ctx, binding, snapshot.Revision, snapshot.Source, publishedAt)
}

func (r *Runner) projectRevisionSource(ctx context.Context, binding Binding, revision provider.DocumentRevision, source provider.SemanticSource, publishedAt time.Time) (Projection, error) {
	if revision.DocumentID == "" || source.DocumentID != revision.DocumentID ||
		revision.Revision < 1 || source.Revision != revision.Revision ||
		revision.FolderID != binding.FolderID || revision.PublicationStatus != "published" ||
		revision.ContentHash == "" || source.ContentHash != revision.ContentHash {
		return Projection{}, ErrRevisionMismatch
	}
	if source.ContentType != "text/markdown" {
		return Projection{}, fmt.Errorf("unsupported semantic source content type %q", source.ContentType)
	}
	extraction, err := ExtractFrontmatter(source.Content)
	if err != nil {
		return Projection{}, err
	}
	objects, links, facts, evidence, err := graphDocumentSnapshot(ctx, r.graph, binding.TenantID, revision.DocumentID)
	if err != nil {
		return Projection{}, err
	}
	return ProjectPublishedDocument(ProjectRequest{
		TenantID: binding.TenantID, KnowledgeBaseID: binding.KnowledgeBaseID,
		DocumentID: revision.DocumentID, Revision: revision.Revision,
		ContentHash: revision.ContentHash, PublishedAt: publishedAt,
		Source: source.Content, Extraction: extraction, Objects: objects, Links: links, Facts: facts, Evidence: evidence,
	}), nil
}
