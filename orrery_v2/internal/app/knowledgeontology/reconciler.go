package knowledgeontology

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	knowledgecore "orrery_v2/internal/knowledge/core"
	"orrery_v2/internal/knowledge/provider"
)

type DocumentLister interface {
	ListDocuments(context.Context, knowledgecore.DocumentQuery) (knowledgecore.DocumentPage, error)
}

type FullReconciler struct {
	source  provider.RevisionProvider
	docs    DocumentLister
	runner  *Runner
	applier ProjectionApplier
	store   BridgeStore
}

func NewFullReconciler(source provider.RevisionProvider, docs DocumentLister, runner *Runner, store BridgeStore, applier ProjectionApplier) *FullReconciler {
	return &FullReconciler{source: source, docs: docs, runner: runner, store: store, applier: applier}
}

func (r *FullReconciler) Reconcile(ctx context.Context, binding Binding) (string, error) {
	if r == nil || r.source == nil || r.docs == nil || r.runner == nil || r.store == nil {
		return "", fmt.Errorf("knowledge ontology reconciler dependencies are required")
	}
	// Capture the feed watermark BEFORE provider snapshots. Events arriving
	// during reconciliation must remain replayable on the next incremental run.
	watermark, err := r.latestCursor(ctx, binding.FolderID)
	if err != nil {
		return "", err
	}
	sequence, _ := strconv.ParseInt(watermark, 10, 64)
	documentIDs, err := r.documentIDs(ctx, binding)
	if err != nil {
		return "", err
	}
	for _, candidate := range documentIDs {
		documentID := strings.TrimSpace(candidate)
		if documentID == "" {
			continue
		}
		var expected *SourceHead
		if store, ok := r.runner.lifecycleStore(); ok {
			head, err := store.SourceHead(ctx, binding, documentID)
			if err != nil {
				return "", err
			}
			expected = &head
		}
		revisions, err := r.source.ListDocumentRevisions(ctx, provider.DocumentRevisionsRequest{DocumentID: documentID})
		if err != nil {
			return "", err
		}
		for _, revision := range revisions {
			if revision.PublicationStatus != "published" {
				continue
			}
			occurredAt := revision.CreatedAt
			if revision.PublishedAt != nil {
				occurredAt = *revision.PublishedAt
			}
			projection, err := r.runner.projectChange(ctx, binding, provider.KnowledgeChange{
				FolderID: binding.FolderID, DocumentID: documentID, Revision: revision.Revision,
				EventType: "document.published", ContentHash: revision.ContentHash, OccurredAt: occurredAt,
			})
			if err != nil {
				return "", err
			}
			if err := r.runner.applyAndSave(ctx, binding, projection, "", expected, sequence); err != nil {
				return "", err
			}
			if store, ok := r.runner.lifecycleStore(); ok {
				head, err := store.SourceHead(ctx, binding, documentID)
				if err != nil {
					return "", err
				}
				expected = &head
			}
		}
		if len(revisions) == 0 {
			indexedSource, ok := r.source.(provider.IndexedDocumentSourceProvider)
			if !ok {
				continue
			}
			snapshot, err := indexedSource.GetIndexedDocumentSource(ctx, provider.IndexedDocumentSourceRequest{DocumentID: documentID})
			if err != nil {
				return "", err
			}
			publishedAt := snapshot.Revision.CreatedAt
			if snapshot.Revision.PublishedAt != nil {
				publishedAt = *snapshot.Revision.PublishedAt
			}
			projection, err := r.runner.projectRevisionSource(ctx, binding, snapshot.Revision, snapshot.Source, publishedAt)
			if err != nil {
				return "", err
			}
			if err := r.runner.applyAndSave(ctx, binding, projection, "", expected, sequence); err != nil {
				return "", err
			}
		}
	}
	if _, err := r.runner.cleanupMissingDocuments(ctx, binding); err != nil {
		return "", err
	}
	return watermark, nil
}

func (r *FullReconciler) documentIDs(ctx context.Context, binding Binding) ([]string, error) {
	if lister, ok := r.source.(provider.FolderDocumentLister); ok {
		documents, err := lister.ListFolderDocuments(ctx, provider.FolderDocumentsRequest{FolderID: binding.FolderID})
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(documents))
		for _, document := range documents {
			if strings.TrimSpace(document.Status) != string(knowledgecore.DocumentIndexed) {
				continue
			}
			if id := strings.TrimSpace(document.DocumentID); id != "" {
				ids = append(ids, id)
			}
		}
		return ids, nil
	}
	page, err := r.docs.ListDocuments(ctx, knowledgecore.DocumentQuery{
		KnowledgeBaseID: knowledgecore.KnowledgeBaseID(binding.KnowledgeBaseID),
	})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(page.Items))
	for _, document := range page.Items {
		if id := strings.TrimSpace(document.ProviderDocumentID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (r *FullReconciler) latestCursor(ctx context.Context, folderID string) (string, error) {
	cursor := ""
	for {
		page, err := r.source.ListKnowledgeChanges(ctx, provider.KnowledgeChangesRequest{FolderID: folderID, Cursor: cursor, Limit: 200})
		if err != nil {
			return "", err
		}
		if len(page.Items) == 0 {
			return cursor, nil
		}
		last := page.Items[len(page.Items)-1].Sequence
		next := strings.TrimSpace(page.NextCursor)
		if next == "" {
			next = strconv.FormatInt(last, 10)
		}
		if next == cursor {
			return "", fmt.Errorf("PageIndex change feed cursor did not advance")
		}
		cursor = next
		if len(page.Items) < 200 {
			return cursor, nil
		}
	}
}
